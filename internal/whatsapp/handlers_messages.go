package whatsapp

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

const maxSendFileBytes = 100 << 20

func init() {
	httpmw.RegisterLargeBody(isPanelSendFileUpload, maxSendFileBytes)
}

func isPanelSendFileUpload(r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
		return false
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/whatsapp/chats/")
	if rest == r.URL.Path {
		return false
	}
	parts := strings.SplitN(rest, "/", 2)
	return len(parts) == 2 && parts[1] == "messages"
}

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
	msgs, _, err := s.MessagesForDisplay(jid, MessagesQuery{Before: before, Limit: limit})
	if err != nil {
		writeErrResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSONResp(w, map[string]any{"messages": msgs})
}

func (s *Service) backfillFromWAHA(jid string, limit int) {
	_ = s.backfillFromWAHACounted(jid, limit)
}

func (s *Service) requestHistoryGap(jid string, count int) {
	if s.Client == nil || jid == "" || jid == "status@broadcast" {
		return
	}
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

func (s *Service) backfillFromWAHACounted(jid string, limit int) int {
	if limit <= 0 {
		limit = 50
	}
	const pageSize = 200
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
			break
		}
		any = true
		if len(msgs) == 0 {
			break
		}
		for _, wm := range msgs {
			if wm.ID == "" {
				continue
			}
			rawType := wm.Type
			if rawType == "" && wm.Data != nil && wm.Data.Info != nil && wm.Data.Info.MediaType != "" {
				rawType = wm.Data.Info.MediaType
			}
			if rawType == "" && wm.HasMedia {
				rawType = "media"
			}
			finalType := normalizeType(rawType)
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
				FromJID:  s.CanonicalChatJIDLazy(wm.From),
				FromMe:   wm.FromMe,
				TS:       ts,
				Type:     finalType,
				Body:     body,
				QuotedID: wm.QuotedID,
				Ack:      wm.Ack,
			}
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
			break
		}
	}
	return added
}

func (s *Service) handleMessagesSend(w http.ResponseWriter, r *http.Request, jid string, audit func(*http.Request, string, string)) {
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
	id, err := s.SendTextDedup(jid, body.Text, body.QuotedID, body.ClientMsgID)
	if err != nil {
		writeErrResp(w, http.StatusBadGateway, err.Error())
		return
	}
	audit(r, "whatsapp.send", jid)
	writeJSONResp(w, map[string]any{"id": id})
}

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
	respType := msgType
	if respType == "" {
		respType = guessMsgType(header.Filename, mimeType)
	}
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
	_ = s.MarkRead(jid)
	writeJSONResp(w, map[string]any{"ok": true})
}
