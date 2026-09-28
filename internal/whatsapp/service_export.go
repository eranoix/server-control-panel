package whatsapp

import (
	"errors"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type MessagesQuery struct {
	Before int64
	Limit  int
}

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
			newestLocal = msgs[0].TS
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
			s.backfillFromWAHA(jid, limit)
			s.requestHistoryGap(jid, 100)
			if fresh, ferr := s.Store.LoadMessages(jid, before, limit); ferr == nil {
				msgs = fresh
			}
		}
	}

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

func (s *Service) ListChats() []Chat {
	ptrs := s.Store.ListChats()
	out := make([]Chat, len(ptrs))
	for i, c := range ptrs {
		out[i] = *c
	}
	return out
}

func (s *Service) MarkRead(jid string) error {
	err := s.Client.MarkChatRead(jid)
	_ = s.Store.MarkChatRead(jid)
	return err
}

func (s *Service) ServeAvatar(w http.ResponseWriter, r *http.Request, jid string) {
	now := time.Now()

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

	if c := s.Store.GetChat(jid); c != nil && c.AvatarURL != "" {
		if s.tryServeAvatar(w, r, c.AvatarURL) {
			s.avatarMu.Lock()
			s.avatarCache[jid] = avatarEntry{url: c.AvatarURL, expires: now.Add(1 * time.Hour)}
			s.avatarMu.Unlock()
			return
		}
	}

	avatarURL, err := s.Client.GetProfilePicture(jid)
	if err == nil && avatarURL != "" && s.tryServeAvatar(w, r, avatarURL) {
		s.avatarMu.Lock()
		s.avatarCache[jid] = avatarEntry{url: avatarURL, expires: now.Add(1 * time.Hour)}
		s.avatarMu.Unlock()
		_ = s.Store.MergeChatAvatar(jid, avatarURL)
		return
	}

	s.avatarMu.Lock()
	s.avatarCache[jid] = avatarEntry{url: "", expires: now.Add(10 * time.Minute)}
	s.avatarMu.Unlock()
	avatarNoPhoto(w)
}

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

	var wahaMediaURL, wahaMime, wahaFilename string
	if _, isMeow := s.Client.(*meowClient); isMeow {
		wahaMediaURL = "/api/files/" + msgID
		wahaMime = existing.Media.MimeType
		wahaFilename = existing.Media.Filename
	} else {
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

func (s *Service) ServeMediaRel(w http.ResponseWriter, r *http.Request, rel string) {
	if !safeMediaPath(rel) {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	full := filepath.Join(s.Store.MediaRoot, rel)
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
