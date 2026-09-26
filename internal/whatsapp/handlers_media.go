package whatsapp

// handlers_media.go — serving media files + download by msgID
//
// Covers handleMedia (GET /api/whatsapp/media/<path>), which serves from
// MediaRoot with an anti-traversal check. handleMessageDownload (POST
// /api/whatsapp/messages/download) fetches media straight from WAHA when it
// is not on local disk (a cache miss). mediaExtFor and sanitizeMsgID are
// helpers.
//
// Extracted from api.go.

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// localMediaRel returns rel only when it points at a file that REALLY exists
// inside MediaRoot; otherwise "". The backfill and sync used to write
// Media.Path with WAHA's mediaUrl even without downloading the bytes, so the
// frontend rendered an <img src> and took a 404. With Path="" the frontend
// shows the "Download" button instead (on-demand download). http(s) URLs are
// preserved (the frontend consumes them directly).
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

// handleMedia serves files from MediaRoot under /api/whatsapp/media/<path>.
// path is validated against traversal; only files inside MediaRoot are served.
// A thin wrapper: it parses r.URL.Path and delegates to ServeMediaRel
// (service_export.go), which the mobile BFF reuses.
func (s *Service) handleMedia(w http.ResponseWriter, r *http.Request) {
	rel := strings.TrimPrefix(r.URL.Path, "/api/whatsapp/media/")
	s.ServeMediaRel(w, r, rel)
}

// handleMessageDownload is the on-demand media endpoint.
// Path: POST /api/whatsapp/messages/download/<msgID>?chat=<jid>
// — chat is required because the store indexes messages per chat (a msgID is
// globally unique, but the lookup goes through the chat hash). The frontend
// already has the active conversation's jid, so it passes it in the query.
//
// A thin wrapper: it parses the HTTP request and delegates the whole
// orchestration (cache hit/miss, backend choice, persistence) to
// DownloadMediaForMessage (service_export.go), which the mobile BFF reuses.
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

// mediaExtFor picks the file extension: 1) from the filename when there is
// one, 2) inferred from the mime, 3) extracted from the URL, 4) ".bin" as a
// last resort.
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
	// Fallback: extract it from the URL (WAHA serves as /api/files/<session>/<id>.<ext>)
	if i := strings.LastIndex(urlStr, "."); i > 0 {
		if e := urlStr[i:]; len(e) <= 8 && !strings.ContainsAny(e, "/?&") {
			return e
		}
	}
	return ".bin"
}

// sanitizeMsgID turns a msgID (which may carry @ . _ - characters) into a
// filename safe on any FS. It keeps alphanumerics and replaces the rest with _.
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
