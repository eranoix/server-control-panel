package api

// handlers_adguard.go — server-side proxy for AdGuard Home (Segurança → AdGuard).
//
// Same design as the private-ai integration (handlers_ai.go): it resolves the
// credentials from the vault, builds a thin client that talks to the local admin
// API on 127.0.0.1, and hands the result to the browser — the credentials never
// leave the server. The routes are registered on the `protected` sub-mux, so they
// are already gated by auth.

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

// adguardClient builds an AdGuard admin API client with the URL from the config
// and the credentials resolved from the vault. Returns ok=false when credentials
// are missing — the caller answers 503 with a clear warning.
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

// adguardCreds reads the AdGuard username and password from the secrets vault.
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

// handleAdguardStatus — GET: combined status (protection on/off + 24h stats).
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

// handleAdguardProtection — POST {enabled, duration_ms}: turns the filter on/off.
// duration_ms>0 with enabled=false pauses for that long and re-enables itself.
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
