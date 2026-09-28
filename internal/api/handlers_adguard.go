package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"server-control-panel/internal/adguard"
	"server-control-panel/internal/auth"
)

const (
	adguardUserSecret = "adguard_user"
	adguardPassSecret = "adguard_password"
)

func (r *Router) adguardClient() (*adguard.Client, bool) {
	user, pass := r.adguardCreds()
	if strings.TrimSpace(pass) == "" {
		return nil, false
	}
	if strings.TrimSpace(user) == "" {
		user = "sam"
	}
	return adguard.New(r.cfg.AdGuardURL, user, pass), true
}

func (r *Router) adguardCreds() (user, pass string) {
	if r.secrets == nil {
		return "", ""
	}
	if v, ok := r.secrets.Get(adguardUserSecret); ok {
		user = strings.TrimSpace(v)
	}
	if v, ok := r.secrets.Get(adguardPassSecret); ok {
		pass = strings.TrimSpace(v)
	}
	return user, pass
}

func (r *Router) handleAdguardStatus(w http.ResponseWriter, req *http.Request) {
	if auth.UserFrom(req) == "" {
		writeErr(w, 401, "unauthorized")
		return
	}
	if req.Method != http.MethodGet {
		writeErr(w, 405, "method not allowed")
		return
	}
	cli, ok := r.adguardClient()
	if !ok {
		writeErr(w, 503, "AdGuard credentials not found (set the secrets "+adguardUserSecret+" e "+adguardPassSecret+")")
		return
	}
	st, err := cli.Status(req.Context())
	if err != nil {
		writeErr(w, 502, "AdGuard unreachable: "+err.Error())
		return
	}
	writeJSON(w, st)
}

func (r *Router) handleAdguardProtection(w http.ResponseWriter, req *http.Request) {
	if auth.UserFrom(req) == "" {
		writeErr(w, 401, "unauthorized")
		return
	}
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	cli, ok := r.adguardClient()
	if !ok {
		writeErr(w, 503, "AdGuard credentials not found (set the secrets "+adguardUserSecret+" e "+adguardPassSecret+")")
		return
	}
	var body struct {
		Enabled    bool `json:"enabled"`
		DurationMs int  `json:"duration_ms"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeErr(w, 400, "invalid body: "+err.Error())
		return
	}
	if _, err := cli.SetProtection(req.Context(), body.Enabled, body.DurationMs); err != nil {
		writeErr(w, 502, "AdGuard refused the change: "+err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true, "protection_enabled": body.Enabled})
}
