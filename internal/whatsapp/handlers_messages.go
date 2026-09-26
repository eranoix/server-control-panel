package whatsapp

// handlers_messages.go — per-JID message routes
//
// Covers handleChatRoutes (the GET/POST dispatcher for /chats/{jid}/messages
// and /chats/{jid}/read), handleMessagesList (with an on-demand backfill when
// the local store has no messages), backfillFromWAHA (and its Counted
// variant), handleMessagesSend (text via Client), handleSendFile (multipart
// upload), guessMsgType (the type-inference helper) and handleMarkRead.
//
// Extracted from api.go.

import (
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"server-control-panel/internal/httpmw"
)

// maxSendFileBytes is the panel's media upload ceiling — the same value the
// frontend announces to the user ("Arquivo maior que 100MB (limite do
// WhatsApp)", 00-shell.js) and the same one the mobile BFF's twin route uses
// (handlers_whatsapp_media.go). httpmw.MaxBody applies 25 MiB by default on
// every route; without the RegisterLargeBody in init() below, any media
// between 25 and 100 MiB was cut off by the global MaxBytesReader BEFORE
// reaching here — the ParseMultipartForm(100<<20) just below could never
// deliver what it promised, because MaxBytesReader cannot loosen a smaller
// limit already applied by an earlier wrapper on the same r.Body.
const maxSendFileBytes = 100 << 20

func init() {
	httpmw.RegisterLargeBody(isPanelSendFileUpload, maxSendFileBytes)
}

// isPanelSendFileUpload matches exactly POST
// /api/whatsapp/chats/<jid>/messages with a multipart Content-Type — the
// variant of handleMessagesSend that dispatches to handleSendFile. The text
// variant (application/json) does not need the larger ceiling; leaving it out
// avoids accidentally widening the body of some other request that lands on
// the same path.
func isPanelSendFileUpload(r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
		return false
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/whatsapp/chats/")
	if rest == r.URL.Path {
		return false // does not start with the expected prefix
	}
	parts := strings.SplitN(rest, "/", 2)
	return len(parts) == 2 && parts[1] == "messages"
}

// handleChatRoutes dispatches /api/whatsapp/chats/{jid}/messages and read.
func (s *Service) handleChatRoutes(audit func(*http.Request, string, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rest := strings.TrimPrefix(r.URL.Path, "/api/whatsapp/chats/")
		parts := strings.SplitN(rest, "/", 2)
		if len(parts) < 2 {
			writeErrResp(w, http.StatusBadRequest, "expected /chats/{jid}/messages or /read")
			return
		}
		jid := parts[0]
		action := parts[1]
		switch action {
		case "messages":
			if r.Method == http.MethodGet {
				s.handleMessagesList(w, r, jid)
			} else if r.Method == http.MethodPost {
				s.handleMessagesSend(w, r, jid, audit)
			} else {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			}
		case "read":
			s.handleMarkRead(w, r, jid)
		default:
			writeErrResp(w, http.StatusBadRequest, "unknown action: "+action)
		}
	}
}

func (s *Service) handleMessagesList(w http.ResponseWriter, r *http.Request, jid string) {
	before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	// Backfill-on-open (gap detection by timestamp), the media display guard
	// and the download enqueue all live in MessagesForDisplay, reused by the
	// mobile BFF (see service_export.go — never duplicate).
	msgs, _, err := s.MessagesForDisplay(jid, MessagesQuery{Before: before, Limit: limit})
	if err != nil {
		writeErrResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSONResp(w, map[string]any{"messages": msgs})
}

// backfillFromWAHA pulls the last `limit` messages from WAHA and
// AppendMessages them into the store. AppendMessage does not deduplicate — it
// trusts the caller. Here we check FindMessage by ID before writing to avoid
// duplicates. It does not download media (downloadMedia=false), only
// metadata; the user presses the download button to bring the bytes into
// MediaRoot when they want them.
//
// Best effort: failures are logged but do not fail the response — the user
// still gets whatever is in the local store.
func (s *Service) backfillFromWAHA(jid string, limit int) {
	_ = s.backfillFromWAHACounted(jid, limit)
}

// requestHistoryGap fires an ON-DEMAND history sync for the chat, anchored on
// the newest local message (BuildHistorySyncRequest pulls the `count` messages
// BEFORE it). It is the ONLY path that recovers missing messages on the
// whatsmeow backend: the daemon's GetChatMessages* is a stub (whatsmeow does
// not archive history), so backfillFromWAHA brings back nothing. The answer
// comes back asynchronously as events.HistorySync on the daemon ->
// onHistorySync re-emits each message -> handleMessage inserts the missing
// ones into the Store and broadcasts (deduped by ID). This covers gaps such
// as the ones left by a spell of webhook 401s (a diverged hmac). Debounced
// per chat (one burst every 2min) so it does not trip the primary device's
// anti-spam.
func (s *Service) requestHistoryGap(jid string, count int) {
	if s.Client == nil || jid == "" || jid == "status@broadcast" {
		return
	}
	// The anchor is the newest local message. With no anchor (a chat with no
	// local messages at all) BuildHistorySyncRequest has no reference point, so
	// there is nothing to ask for.
	newest, err := s.Store.LoadMessages(jid, 0, 1)
	if err != nil || len(newest) == 0 {
		return
	}
	key := "hist:" + jid
	now := time.Now().Unix()
	s.recoverMu.Lock()
	if s.recoverAt == nil {
		s.recoverAt = map[string]int64{}
	}
	if last := s.recoverAt[key]; now-last < 120 {
		s.recoverMu.Unlock()
		return
	}
	s.recoverAt[key] = now
	s.recoverMu.Unlock()
	if count <= 0 {
		count = 100
	}
	n := newest[0]
	_ = s.Client.RequestHistory(jid, n.ID, n.FromMe, n.TS, count)
}

// backfillFromWAHACounted is backfillFromWAHA but returns how many messages
// were actually added (>=0). It returns -1 when the call to WAHA failed — the
// caller uses that to count chats with a sync error.
//
// It paginates automatically when `limit > 200` (WAHA's per-call cap).
// It detects the correct media type via `_data.Info.MediaType` (the GOWS
// engine returns an empty top-level `type` even for image/video/audio).
func (s *Service) backfillFromWAHACounted(jid string, limit int) int {
	if limit <= 0 {
		limit = 50
	}
	const pageSize = 200 // WAHA's effective limit per call
	// IMPORTANT: we use the requested (canonicalised) jid as the ChatJID of
	// every message. The `wm.From`/`wm.To` WAHA returns from this endpoint come
	// in @lid form (e.g. 100000000000001@lid) even when the official chat is
	// @c.us — deriving from them filed inbound messages into the @lid's shadow
	// directory (one chat ended up with 143 messages in the @lid dir and 7 in
	// the @c.us one).
	canonChat := s.CanonicalChatJID(jid)
	added := 0
	offset := 0
	remaining := limit
	any := false
	for remaining > 0 {
		batch := remaining
		if batch > pageSize {
			batch = pageSize
		}
		msgs, err := s.Client.GetChatMessagesPaged(jid, batch, offset)
		if err != nil {
			if !any {
				return -1
			}
			break // failure mid-pagination → stop, but return what already came in
		}
		any = true
		if len(msgs) == 0 {
			break // end of history
		}
		for _, wm := range msgs {
			if wm.ID == "" {
				continue
			}
			// GOWS quirk: the real type is in _data.Info.MediaType. Fall back to the
			// top-level Type (the WEBJS engine) and, failing that, mark it "media"
			// for hasMedia (normalizeType turns it into "document").
			rawType := wm.Type
			if rawType == "" && wm.Data != nil && wm.Data.Info != nil && wm.Data.Info.MediaType != "" {
				rawType = wm.Data.Info.MediaType
			}
			if rawType == "" && wm.HasMedia {
				rawType = "media"
			}
			finalType := normalizeType(rawType)
			// Already there? Reclassify the type (text -> image, etc.) and move on.
			if ex, _ := s.Store.FindMessage(canonChat, wm.ID); ex != nil {
				_ = s.Store.ReclassifyMessage(canonChat, wm.ID, finalType)
				continue
			}
			ts := wm.Timestamp
			if ts == 0 {
				ts = time.Now().Unix()
			}
			body := wm.Body
			if body == "" {
				body = wm.Caption
			}
			stored := Message{
				ID:       wm.ID,
				ChatJID:  canonChat,
				FromJID:  s.CanonicalChatJIDLazy(wm.From), // group: senderLabel needs @c.us to find a known contact
				FromMe:   wm.FromMe,
				TS:       ts,
				Type:     finalType,
				Body:     body,
				QuotedID: wm.QuotedID,
				Ack:      wm.Ack,
			}
			// RawJSON for parity with the webhook — truncated at 4KB (the same rule).
			// It lets us reprocess types and metadata when we find a new bug and want
			// to derive extra fields without pulling from WAHA again.
			if raw := wm.RawJSON; len(raw) > 0 {
				if len(raw) > 4096 {
					stored.RawJSON = string(raw[:4096]) + "...(trunc)"
				} else {
					stored.RawJSON = string(raw)
				}
			}
			var enqueueURL, enqueueMime, enqueueFilename string
			if wm.HasMedia {
				mime := wm.MimeType
				filename := wm.Filename
				url := wm.MediaURL
				if wm.Media != nil {
					if mime == "" {
						mime = wm.Media.MimeType
					}
					if filename == "" {
						filename = wm.Media.Filename
					}
					if url == "" {
						url = wm.Media.URL
					}
				}
				// Path left empty — the worker fills it in. Same pattern as the webhook.
				stored.Media = &Media{MimeType: mime, Filename: filename, Path: ""}
				enqueueURL, enqueueMime, enqueueFilename = url, mime, filename
			}
			if err := s.Store.AppendMessage(stored); err == nil {
				added++
				if enqueueURL != "" {
					s.EnqueueDownload(canonChat, wm.ID, enqueueURL, enqueueMime, enqueueFilename)
				}
			}
		}
		offset += len(msgs)
		remaining -= len(msgs)
		if len(msgs) < batch {
			break // WAHA returned fewer than requested → end of history
		}
	}
	return added
}

func (s *Service) handleMessagesSend(w http.ResponseWriter, r *http.Request, jid string, audit func(*http.Request, string, string)) {
	// We support 2 content types:
	//  - application/json: {text, quoted_id?}
	//  - multipart/form-data: file upload + caption + type
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "multipart/") {
		s.handleSendFile(w, r, jid, audit)
		return
	}
	var body struct {
		Text        string `json:"text"`
		QuotedID    string `json:"quoted_id"`
		ClientMsgID string `json:"client_msg_id,omitempty"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeErrResp(w, http.StatusBadRequest, "bad json")
		return
	}
	if body.Text == "" {
		writeErrResp(w, http.StatusBadRequest, "empty text")
		return
	}
	// Server-side dedup: a client_msg_id seen recently returns the same
	// server_msg_id without re-sending to WhatsApp. It defends against a
	// double-click and against the frontend's idempotent retry. See
	// SendTextDedup in service_export.go — the same logic the mobile BFF reuses.
	id, err := s.SendTextDedup(jid, body.Text, body.QuotedID, body.ClientMsgID)
	if err != nil {
		writeErrResp(w, http.StatusBadGateway, err.Error())
		return
	}
	audit(r, "whatsapp.send", jid)
	writeJSONResp(w, map[string]any{"id": id})
}

// handleSendFile does the HTTP-specific part of sending media (multipart
// parsing, reading the bytes) and delegates the rest — dedup, the call to
// Client.SendFile, persisting the sent bytes locally — to SendFileDedup
// (service_export.go), which the mobile BFF reuses too.
func (s *Service) handleSendFile(w http.ResponseWriter, r *http.Request, jid string, audit func(*http.Request, string, string)) {
	if err := r.ParseMultipartForm(maxSendFileBytes); err != nil {
		writeErrResp(w, http.StatusBadRequest, "parse: "+err.Error())
		return
	}
	msgType := r.FormValue("type")
	caption := r.FormValue("caption")
	quoted := r.FormValue("quoted_id")
	clientMsgID := r.FormValue("client_msg_id")

	file, header, err := r.FormFile("file")
	if err != nil {
		writeErrResp(w, http.StatusBadRequest, "missing file")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxSendFileBytes))
	if err != nil {
		writeErrResp(w, http.StatusBadRequest, "read file: "+err.Error())
		return
	}
	mimeType := header.Header.Get("Content-Type")
	// respType exists only to echo back, in the response JSON, the type
	// SendFileDedup will actually use (it applies the same default
	// internally) — it does not duplicate the decision of which type to send.
	respType := msgType
	if respType == "" {
		respType = guessMsgType(header.Filename, mimeType)
	}
	// Dedup, same as handleSendText — file uploads are re-tryable too.
	id, err := s.SendFileDedup(jid, msgType, header.Filename, mimeType, caption, quoted, clientMsgID, data)
	if err != nil {
		writeErrResp(w, http.StatusBadGateway, err.Error())
		return
	}
	audit(r, "whatsapp.send.media", jid)
	writeJSONResp(w, map[string]any{"id": id, "type": respType})
}

func guessMsgType(filename, ct string) string {
	ct = strings.ToLower(ct)
	switch {
	case strings.HasPrefix(ct, "image/"):
		return "image"
	case strings.HasPrefix(ct, "video/"):
		return "video"
	case strings.HasPrefix(ct, "audio/"):
		// We can't tell voice-note from regular audio purely from MIME;
		// caller can override via type form field.
		return "voice"
	}
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp":
		return "image"
	case ".mp4", ".mov", ".webm":
		return "video"
	case ".mp3", ".ogg", ".m4a", ".opus":
		return "voice"
	}
	return "document"
}

func (s *Service) handleMarkRead(w http.ResponseWriter, _ *http.Request, jid string) {
	// Best effort; if WAHA or the daemon is down, at least clear it locally. See
	// MarkRead in service_export.go — the same logic the mobile BFF reuses,
	// which (unlike here) propagates the error to the caller instead of
	// swallowing it.
	_ = s.MarkRead(jid)
	writeJSONResp(w, map[string]any{"ok": true})
}
