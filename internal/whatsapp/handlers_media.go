package whatsapp

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func (s *Store) localMediaRel(rel string) string {
	if rel == "" {
		return ""
	}
	if strings.HasPrefix(rel, "http://") || strings.HasPrefix(rel, "https://") {
		return rel
	}
	if !safeMediaPath(rel) {
		return ""
	}
	full := filepath.Join(s.MediaRoot, rel)
	if fi, err := os.Stat(full); err == nil && !fi.IsDir() {
		return rel
	}
	return ""
}

func (s *Service) handleMedia(w http.ResponseWriter, r *http.Request) {
	rel := strings.TrimPrefix(r.URL.Path, "/api/whatsapp/media/")
	s.ServeMediaRel(w, r, rel)
}

func (s *Service) handleMessageDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	msgID := strings.TrimPrefix(r.URL.Path, "/api/whatsapp/messages/download/")
	chatJID := r.URL.Query().Get("chat")
	if msgID == "" || chatJID == "" {
		writeErrResp(w, http.StatusBadRequest, "missing msg id or chat jid")
		return
	}

	rel, mimeType, _, size, err := s.DownloadMediaForMessage(chatJID, msgID)
	if err != nil {
		status := http.StatusBadGateway
		var de *DownloadMediaError
		if errors.As(err, &de) {
			status = de.Status
		}
		writeErrResp(w, status, err.Error())
		return
	}

	writeJSONResp(w, map[string]any{
		"ok":   true,
		"path": rel,
		"mime": mimeType,
		"size": size,
		"url":  "/api/whatsapp/media/" + rel,
	})
}

func mediaExtFor(mimeType, filename, urlStr string) string {
	if filename != "" {
		if e := filepath.Ext(filename); e != "" && len(e) <= 8 {
			return e
		}
	}
	switch mimeType {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	case "video/mp4":
		return ".mp4"
	case "video/webm":
		return ".webm"
	case "audio/ogg", "audio/ogg; codecs=opus":
		return ".ogg"
	case "audio/mpeg":
		return ".mp3"
	case "audio/mp4", "audio/aac":
		return ".m4a"
	case "audio/wav":
		return ".wav"
	case "application/pdf":
		return ".pdf"
	}
	if i := strings.LastIndex(urlStr, "."); i > 0 {
		if e := urlStr[i:]; len(e) <= 8 && !strings.ContainsAny(e, "/?&") {
			return e
		}
	}
	return ".bin"
}

func sanitizeMsgID(id string) string {
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '_' || r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := b.String()
	if len(out) > 96 {
		out = out[:96]
	}
	return out
}
