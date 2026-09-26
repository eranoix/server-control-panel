package alert

import (
	"encoding/json"
	"log"
	"net"
	"net/http"
	"strings"

	"server-control-panel/internal/config"
	"server-control-panel/internal/scope"
	"server-control-panel/internal/whatsapp"
)

// Handler serves POST /_internal/alert.
//
// Flow: loopback check → parse Alertmanager payload → check config (enabled,
// min_severity, from_user, chat_jid) → resolve the user's Service via
// whatsapp.Manager → send via Client.SendText.
//
// Errors answered:
//   - 405 method not allowed (non-POST)
//   - 403 forbidden (non-loopback origin)
//   - 400 bad payload (invalid JSON)
//   - 204 no content (empty or filtered out)
//   - 503 service unavailable (missing config or WhatsApp unavailable)
//   - 502 bad gateway (failed to send via WAHA)
//   - 200 ok (message dispatched)
type Handler struct {
	wamgr *whatsapp.Manager
	cfg   *config.Alerting
}

// NewHandler builds an http.HandlerFunc. wamgr and cfg must not be nil.
func NewHandler(wamgr *whatsapp.Manager, cfg *config.Alerting) http.HandlerFunc {
	h := &Handler{wamgr: wamgr, cfg: cfg}
	return h.ServeHTTP
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !isLoopback(req.RemoteAddr) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	var p Payload
	if err := json.NewDecoder(req.Body).Decode(&p); err != nil {
		log.Printf("alert: bad payload: %v", err)
		http.Error(w, "bad payload", http.StatusBadRequest)
		return
	}
	if len(p.Alerts) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if h.cfg == nil || !h.cfg.Enabled {
		log.Printf("alert: discarding (alerting disabled): groupKey=%s alerts=%d", p.GroupKey, len(p.Alerts))
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if !PassesFilter(&p, h.cfg.MinSeverity) {
		log.Printf("alert: filtered out (min=%s): groupKey=%s alerts=%d", h.cfg.MinSeverity, p.GroupKey, len(p.Alerts))
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if h.cfg.FromUser == "" {
		log.Printf("alert: from_user not configured")
		http.Error(w, "from_user not configured", http.StatusServiceUnavailable)
		return
	}
	if h.cfg.ChatJID == "" {
		log.Printf("alert: chat_jid not configured")
		http.Error(w, "chat_jid not configured", http.StatusServiceUnavailable)
		return
	}

	user, err := scope.New(h.cfg.FromUser)
	if err != nil {
		log.Printf("alert: invalid from_user %q: %v", h.cfg.FromUser, err)
		http.Error(w, "invalid from_user", http.StatusServiceUnavailable)
		return
	}
	svc, err := h.wamgr.ForUser(user)
	if err != nil {
		log.Printf("alert: ForUser(%s) failed: %v", user, err)
		http.Error(w, "whatsapp account not available", http.StatusServiceUnavailable)
		return
	}

	msg := FormatMessage(&p)
	if msg == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	id, err := svc.Client.SendText(h.cfg.ChatJID, msg, "")
	if err != nil {
		log.Printf("alert: SendText failed (chat=%s): %v", h.cfg.ChatJID, err)
		http.Error(w, "send failed", http.StatusBadGateway)
		return
	}
	log.Printf("alert: dispatched (msgID=%s chat=%s alerts=%d)", id, h.cfg.ChatJID, len(p.Alerts))
	w.WriteHeader(http.StatusOK)
}

// isLoopback accepts 127.0.0.1, ::1, and any other IsLoopback() address.
// The server always listens on loopback (cfg.Listen), so this is
// defence in depth — if a misconfiguration exposed the port, the handler still
// refuses non-loopback requests.
func isLoopback(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	host = strings.Trim(host, "[]")
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}
