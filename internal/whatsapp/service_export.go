package whatsapp

// service_export.go — an exported wrapper so the mobile BFF reuses the panel's
// logic instead of ever duplicating it. (An earlier mobile surface forked the
// domain logic into a second implementation, and the two drifted apart in
// silence.)
//
// Every method here is a literal extraction of a body that used to live
// inline in an HTTP handler: handleMessagesSend (the text branch),
// handleMessagesList (backfill-on-open), handleMarkRead and handleAvatar. The
// legacy handlers now only decode the HTTP request and delegate here — no
// duplicated logic is left on either side.

import (
	"errors"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// MessagesQuery paginates a chat's history. It mirrors exactly the parameters
// handleMessagesList already read from r.URL.Query() (before, limit, with
// limit<=0 becoming 50) — no new pagination semantics.
type MessagesQuery struct {
	Before int64
	Limit  int
}

// SendTextDedup sends a text message, deduplicating by clientMsgID: two calls
// with the same clientMsgID (inside sendDedupeRemember's 60s window) return
// the SAME server_msg_id without re-sending to WhatsApp. Extracted from the
// JSON branch of handleMessagesSend.
func (s *Service) SendTextDedup(chatJID, text, quotedID, clientMsgID string) (string, error) {
	if existing, ok := s.sendDedupeCheck(clientMsgID); ok {
		return existing, nil
	}
	id, err := s.Client.SendText(chatJID, text, quotedID)
	if err != nil {
		return "", err
	}
	s.sendDedupeRemember(clientMsgID, id)
	return id, nil
}

// MessagesForDisplay returns a chat's history, firing the same
// backfill-on-open that handleMessagesList runs when the local store is
// behind WhatsApp (detected by timestamp, not by count — see
// backfillFromWAHA/requestHistoryGap). The returned bool says whether a
// backfill was fired on this call (the UI can use it to show "syncing").
//
// It also applies the same media-display guard handleMessagesList always
// applied: it only exposes Media.Path once the file exists locally, and
// enqueues a background download when it does not.
func (s *Service) MessagesForDisplay(jid string, opts MessagesQuery) ([]Message, bool, error) {
	before := opts.Before
	limit := opts.Limit
	if limit <= 0 {
		limit = 50
	}
	msgs, err := s.Store.LoadMessages(jid, before, limit)
	if err != nil {
		return nil, false, err
	}

	backfilling := false
	if before == 0 && s.Client != nil {
		var newestLocal int64
		if len(msgs) > 0 {
			newestLocal = msgs[0].TS // LoadMessages devolve newest-first
		}
		needBackfill := len(msgs) < limit
		if !needBackfill {
			for _, c := range s.Store.ListChats() {
				if c.JID == jid && c.LastMsgTS > newestLocal+1 {
					needBackfill = true
					break
				}
			}
		}
		if needBackfill {
			backfilling = true
			s.backfillFromWAHA(jid, limit) // no-op no daemon; mantido p/ backend WAHA
			s.requestHistoryGap(jid, 100)
			if fresh, ferr := s.Store.LoadMessages(jid, before, limit); ferr == nil {
				msgs = fresh
			}
		}
	}

	// Display guard: only expose Media.Path when the file really is in
	// MediaRoot (see the original handleMessagesList for the bug's history).
	var localMedia map[string]string
	if entries, rerr := os.ReadDir(filepath.Join(s.Store.MediaRoot, chatDir(jid))); rerr == nil {
		localMedia = make(map[string]string, len(entries))
		cd := chatDir(jid)
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			prefix := strings.TrimSuffix(name, filepath.Ext(name))
			localMedia[prefix] = filepath.Join(cd, name)
		}
	}
	for i := range msgs {
		if msgs[i].Media != nil {
			msgs[i].Media.Path = s.Store.localMediaRel(msgs[i].Media.Path)
			if msgs[i].Media.Path == "" && msgs[i].Type != "" && msgs[i].Type != "text" && localMedia != nil {
				if rel, ok := localMedia[sanitizeMsgID(msgs[i].ID)]; ok && safeMediaPath(rel) {
					msgs[i].Media.Path = rel
				}
			}
			if msgs[i].Media.Path == "" && msgs[i].Type != "" && msgs[i].Type != "text" {
				s.EnqueueDownload(jid, msgs[i].ID, "/api/files/"+msgs[i].ID, msgs[i].Media.MimeType, msgs[i].Media.Filename)
			}
		}
	}
	return msgs, backfilling, nil
}

// ListChats returns the user's chats in the same order the panel already uses
// (pinned-then-recency, computed by Store.ListChats — no re-sorting here).
// The wrapper is necessary because ListChats is only exported on *Store, and
// the mobile BFF sees *Service solely through the whatsappSvc interface
// (methods, not fields).
func (s *Service) ListChats() []Chat {
	ptrs := s.Store.ListChats()
	out := make([]Chat, len(ptrs))
	for i, c := range ptrs {
		out[i] = *c
	}
	return out
}

// MarkRead marks a chat as read: a passthrough to the Backend (real WhatsApp)
// plus the local store. Unlike handleMarkRead (which is best effort and
// always answers {"ok":true}), this method returns the Backend's error
// without swallowing it — the caller (BFF or panel) decides what to do with it.
func (s *Service) MarkRead(jid string) error {
	err := s.Client.MarkChatRead(jid)
	_ = s.Store.MarkChatRead(jid)
	return err
}

// ServeAvatar serves the profile picture of contact or group `jid`, with the
// same in-memory cache (1h positive, 10min negative) and anti-SSRF proxy
// handleAvatar always used — extracted from it, callable directly with the
// jid as a parameter instead of parsed out of r.URL.Path, for routes that
// already resolve the jid from a path param (such as the mobile BFF).
func (s *Service) ServeAvatar(w http.ResponseWriter, r *http.Request, jid string) {
	now := time.Now()

	// 1) In-memory cache. On a positive hit, try to serve it; if the cached URL
	//    has already expired on the CDN, INVALIDATE and fall through to a fresh
	//    fetch (self-healing).
	s.avatarMu.RLock()
	entry, ok := s.avatarCache[jid]
	s.avatarMu.RUnlock()
	if ok && now.Before(entry.expires) {
		if entry.url == "" {
			avatarNoPhoto(w)
			return
		}
		if s.tryServeAvatar(w, r, entry.url) {
			return
		}
		s.avatarMu.Lock()
		delete(s.avatarCache, jid)
		s.avatarMu.Unlock()
	}

	// 2) The URL persisted in the store (seeded / pre-warmed).
	if c := s.Store.GetChat(jid); c != nil && c.AvatarURL != "" {
		if s.tryServeAvatar(w, r, c.AvatarURL) {
			s.avatarMu.Lock()
			s.avatarCache[jid] = avatarEntry{url: c.AvatarURL, expires: now.Add(1 * time.Hour)}
			s.avatarMu.Unlock()
			return
		}
	}

	// 3) A fresh on-demand fetch.
	avatarURL, err := s.Client.GetProfilePicture(jid)
	if err == nil && avatarURL != "" && s.tryServeAvatar(w, r, avatarURL) {
		s.avatarMu.Lock()
		s.avatarCache[jid] = avatarEntry{url: avatarURL, expires: now.Add(1 * time.Hour)}
		s.avatarMu.Unlock()
		_ = s.Store.MergeChatAvatar(jid, avatarURL)
		return
	}

	// No picture (or even the fresh URL failed): short negative cache + 204.
	s.avatarMu.Lock()
	s.avatarCache[jid] = avatarEntry{url: "", expires: now.Add(10 * time.Minute)}
	s.avatarMu.Unlock()
	avatarNoPhoto(w)
}

// DownloadMediaError signals a DownloadMediaForMessage failure together with
// the HTTP status both callers (the legacy JSON handler and the mobile BFF)
// should answer with — extracted from the responses handleMessageDownload
// always returned (404 no such message, 400 no media, 404 media not yet
// available in WAHA, 502/500 download or I/O failure). It implements the
// huma.StatusError interface (GetStatus() int; Error() string): when a mobile
// BFF handler returns it directly, huma uses GetStatus() to pick the HTTP
// code and serialises the error itself as the response body — hence the
// lowercase json tags (the same convention as the BFF's other responses, see
// UploadIncompleteResponse) — without the BFF having to translate the error
// all over again.
type DownloadMediaError struct {
	Status int    `json:"status"`
	Msg    string `json:"error"`
}

func (e *DownloadMediaError) Error() string  { return e.Msg }
func (e *DownloadMediaError) GetStatus() int { return e.Status }

type downloadMediaResult struct {
	rel, mimeType, filename string
	size                    int64
}

// DownloadMediaForMessage returns the path of a message's media bytes,
// relative to Store.MediaRoot, downloading on demand when they are not on
// disk yet — extracted literally from the body of handleMessageDownload
// (never duplicate: it is reused by the panel AND the mobile BFF).
//
// Cache hit: if Media.Path already points at an existing file, it returns
// immediately, WITHOUT touching the network (the same flow as the original
// "already downloaded?" check).
// Cache miss: it downloads through the whatsmeow daemon (/api/files/<msgID>,
// decrypted by the daemon) or through WAHA (GetChatMessagesWithMedia +
// DownloadFile), depending on the active Backend, and writes to
// <chatDir>/<safeID>.<ext>.
//
// Concurrent calls for the SAME (chatJID,msgID) collapse into a single
// in-flight download via singleflight — without it, N requests for the same
// not-yet-cached media (the app opening the same image on two screens, say)
// would fire N identical downloads against WhatsApp.
func (s *Service) DownloadMediaForMessage(chatJID, msgID string) (rel, mimeType, filename string, size int64, err error) {
	key := chatJID + "|" + msgID
	v, err, _ := s.downloadSF.Do(key, func() (any, error) {
		return s.downloadMediaForMessageOnce(chatJID, msgID)
	})
	if err != nil {
		return "", "", "", 0, err
	}
	res := v.(downloadMediaResult)
	return res.rel, res.mimeType, res.filename, res.size, nil
}

func (s *Service) downloadMediaForMessageOnce(chatJID, msgID string) (downloadMediaResult, error) {
	existing, _ := s.Store.FindMessage(chatJID, msgID)
	if existing == nil {
		return downloadMediaResult{}, &DownloadMediaError{http.StatusNotFound, "message not found in store"}
	}
	if existing.Media == nil {
		return downloadMediaResult{}, &DownloadMediaError{http.StatusBadRequest, "message has no media"}
	}
	// Already downloaded? Confirm on disk and return without fetching again.
	if existing.Media.Path != "" && safeMediaPath(existing.Media.Path) {
		full := filepath.Join(s.Store.MediaRoot, existing.Media.Path)
		if st, statErr := os.Stat(full); statErr == nil && !st.IsDir() {
			return downloadMediaResult{
				rel:      existing.Media.Path,
				mimeType: existing.Media.MimeType,
				filename: existing.Media.Filename,
				size:     existing.Media.Size,
			}, nil
		}
	}

	// Resolve where the media comes from, according to the active backend:
	var wahaMediaURL, wahaMime, wahaFilename string
	if _, isMeow := s.Client.(*meowClient); isMeow {
		// whatsmeow daemon: the media is downloaded on demand at the
		// /api/files/<id> marker (meowClient.DownloadFile points back at the
		// daemon, which decrypts it via client.Download). The message is already
		// in the Store (confirmed above) and the daemon keeps the original in its
		// stash.
		wahaMediaURL = "/api/files/" + msgID
		wahaMime = existing.Media.MimeType
		wahaFilename = existing.Media.Filename
	} else {
		// WAHA: force the internal download (which pulls ~50 messages) and find the usable URL.
		msgs, gErr := s.Client.GetChatMessagesWithMedia(chatJID, 50)
		if gErr != nil {
			return downloadMediaResult{}, &DownloadMediaError{http.StatusBadGateway, "waha: " + gErr.Error()}
		}
		for _, m := range msgs {
			if m.ID != msgID {
				continue
			}
			wahaMediaURL = m.MediaURL
			wahaMime = m.MimeType
			wahaFilename = m.Filename
			break
		}
		if wahaMediaURL == "" {
			return downloadMediaResult{}, &DownloadMediaError{http.StatusNotFound, "media not yet available — try scrolling to load this message first"}
		}
	}

	// Work out the destination under MediaRoot: <chatDir>/<safeID>.<ext>
	ext := mediaExtFor(wahaMime, wahaFilename, wahaMediaURL)
	rel := filepath.Join(chatDir(chatJID), sanitizeMsgID(msgID)+ext)
	full := filepath.Join(s.Store.MediaRoot, rel)
	if mkErr := os.MkdirAll(filepath.Dir(full), 0o700); mkErr != nil {
		return downloadMediaResult{}, &DownloadMediaError{http.StatusInternalServerError, "mkdir: " + mkErr.Error()}
	}
	out, openErr := os.OpenFile(full, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if openErr != nil {
		return downloadMediaResult{}, &DownloadMediaError{http.StatusInternalServerError, "open: " + openErr.Error()}
	}
	n, ctype, dlErr := s.Client.DownloadFile(wahaMediaURL, out)
	closeErr := out.Close()
	if dlErr != nil {
		_ = os.Remove(full)
		return downloadMediaResult{}, &DownloadMediaError{http.StatusBadGateway, "download: " + dlErr.Error()}
	}
	if closeErr != nil {
		return downloadMediaResult{}, &DownloadMediaError{http.StatusInternalServerError, "close: " + closeErr.Error()}
	}
	if wahaMime == "" {
		wahaMime = ctype
	}
	_ = s.Store.UpdateMessageMedia(chatJID, msgID, rel, wahaMime, wahaFilename, n)

	return downloadMediaResult{rel: rel, mimeType: wahaMime, filename: wahaFilename, size: n}, nil
}

// ServeMediaRel serves the file relative to Store.MediaRoot with
// Range/Content-Type/anti-traversal handling exactly as handleMedia always
// did — extracted from it and parameterised by a `rel` the caller has already
// resolved (the BFF route resolves it from a path param instead of parsing
// r.URL.Path). Range support comes from http.ServeContent, reused verbatim —
// no hand-rolled Range parsing.
func (s *Service) ServeMediaRel(w http.ResponseWriter, r *http.Request, rel string) {
	if !safeMediaPath(rel) {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	full := filepath.Join(s.Store.MediaRoot, rel)
	// Re-anchor check: filepath.Join cleans the path, so verify after.
	if !strings.HasPrefix(full, filepath.Clean(s.Store.MediaRoot)+string(os.PathSeparator)) {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	f, err := os.Open(full)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if ct := mime.TypeByExtension(filepath.Ext(full)); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.Header().Set("Cache-Control", "private, max-age=86400")
	http.ServeContent(w, r, stat.Name(), stat.ModTime(), f)
}

// SendFileDedup sends a file (image/video/audio/document), deduplicating by
// clientMsgID the way SendTextDedup does — two calls with the same
// clientMsgID return the same server_msg_id without re-sending to WhatsApp —
// and persists the sent bytes under MediaRoot so the media shows up inline
// after a reload with no "Download" button, exactly as handleSendFile always
// did. Extracted from the multipart-independent part of handleSendFile:
// reading the multipart/form-data stays in the HTTP handler (panel or mobile
// BFF), which calls this method with the bytes already in hand.
//
// An empty msgType/mimeType is inferred here (guessMsgType / the filename's
// extension) — the same defaulting handleSendFile always applied — so the
// mobile BFF does not have to reimplement that inference (guessMsgType is
// unexported).
func (s *Service) SendFileDedup(chatJID, msgType, filename, mimeType, caption, quotedID, clientMsgID string, data []byte) (string, error) {
	if existing, ok := s.sendDedupeCheck(clientMsgID); ok {
		return existing, nil
	}
	if msgType == "" {
		msgType = guessMsgType(filename, mimeType)
	}
	if mimeType == "" {
		mimeType = mime.TypeByExtension(filepath.Ext(filename))
	}
	id, err := s.Client.SendFile(chatJID, msgType, filename, mimeType, caption, quotedID, data)
	if err != nil {
		return "", err
	}
	s.sendDedupeRemember(clientMsgID, id)
	// Save the sent bytes under MediaRoot (at a deterministic path keyed by
	// message id) so the image/video/etc YOU sent shows up inline after a
	// reload or re-sync, with no "Download" button — the server already has
	// the bytes right here. Best effort: an I/O failure does not fail the send
	// (it has already gone out).
	if msgType != "" && msgType != "text" && s.Store != nil && s.Store.MediaRoot != "" {
		ext := mediaExtFor(mimeType, filename, "")
		rel := filepath.Join(chatDir(chatJID), sanitizeMsgID(id)+ext)
		if safeMediaPath(rel) {
			full := filepath.Join(s.Store.MediaRoot, rel)
			if mkErr := os.MkdirAll(filepath.Dir(full), 0o700); mkErr == nil {
				if wErr := os.WriteFile(full, data, 0o600); wErr == nil {
					_ = s.Store.UpdateMessageMedia(chatJID, id, rel, mimeType, filename, int64(len(data)))
				}
			}
		}
	}
	return id, nil
}
