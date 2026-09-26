package api

// handlers_datasaver.go — the "Economia" panel (Segurança → Economia): controls
// the compression proxy (data saver). Same gated design as AdGuard/Dispositivos:
// the routes live on the `protected` sub-mux. The state lives in files on the host
// (/opt/datasaver/state), read/edited via internal/datasaver; the CA is served
// for download; changing the bypass list restarts the proxies (it is read at start).

import (
	"encoding/json"
	"net/http"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/datasaver"
)

func (r *Router) datasaverManager() *datasaver.Manager {
	return datasaver.New(r.cfg.DatasaverStateDir, r.cfg.DatasaverCAPath)
}

// restartDatasaverProxies restarts the proxy containers (needed when the bypass
// list changes — it is read at start). Best-effort; errors are returned for the
// caller to report.
func (r *Router) restartDatasaverProxies(req *http.Request) error {
	if r.docker == nil {
		return nil
	}
	for _, name := range r.cfg.DatasaverContainers {
		if err := r.docker.Restart(req.Context(), name); err != nil {
			return err
		}
	}
	return nil
}

// GET /api/datasaver/status — settings + bypass + bytes economizados + has_ca.
func (r *Router) handleDatasaverStatus(w http.ResponseWriter, req *http.Request) {
	if auth.UserFrom(req) == "" {
		writeErr(w, 401, "unauthorized")
		return
	}
	if req.Method != http.MethodGet {
		writeErr(w, 405, "method not allowed")
		return
	}
	writeJSON(w, r.datasaverManager().Status())
}

// POST /api/datasaver/settings — {enabled,quality,maxdim,strip_trackers,greyscale}.
// Hot-reloaded by the addon: no restart.
func (r *Router) handleDatasaverSettings(w http.ResponseWriter, req *http.Request) {
	if auth.UserFrom(req) == "" {
		writeErr(w, 401, "unauthorized")
		return
	}
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	var s datasaver.Settings
	if err := json.NewDecoder(req.Body).Decode(&s); err != nil {
		writeErr(w, 400, "invalid body: "+err.Error())
		return
	}
	mgr := r.datasaverManager()
	if err := mgr.SaveSettings(s); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	r.auditEvent(req, auth.UserFrom(req), "datasaver.settings", "")
	writeJSON(w, map[string]any{"ok": true, "settings": mgr.LoadSettings()})
}

// GET  /api/datasaver/bypass — list of hosts that pass through without MITM.
// POST /api/datasaver/bypass — {hosts:[...]} → writes and restarts the proxies.
func (r *Router) handleDatasaverBypass(w http.ResponseWriter, req *http.Request) {
	if auth.UserFrom(req) == "" {
		writeErr(w, 401, "unauthorized")
		return
	}
	mgr := r.datasaverManager()
	switch req.Method {
	case http.MethodGet:
		writeJSON(w, map[string]any{"bypass": mgr.Bypass()})
	case http.MethodPost:
		var body struct {
			Hosts []string `json:"hosts"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			writeErr(w, 400, "invalid body: "+err.Error())
			return
		}
		if err := mgr.SetBypass(body.Hosts); err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		// bypass is read at container start → restart the proxies.
		if err := r.restartDatasaverProxies(req); err != nil {
			writeErr(w, 502, "bypass saved, but restarting the proxies failed: "+err.Error())
			return
		}
		r.auditEvent(req, auth.UserFrom(req), "datasaver.bypass", "")
		writeJSON(w, map[string]any{"ok": true, "bypass": mgr.Bypass()})
	default:
		writeErr(w, 405, "method not allowed")
	}
}

// GET /api/datasaver/ca — baixa o certificado CA (para instalar nos aparelhos).
func (r *Router) handleDatasaverCA(w http.ResponseWriter, req *http.Request) {
	if auth.UserFrom(req) == "" {
		writeErr(w, 401, "unauthorized")
		return
	}
	if req.Method != http.MethodGet {
		writeErr(w, 405, "method not allowed")
		return
	}
	pem, err := r.datasaverManager().CACert()
	if err != nil {
		writeErr(w, 503, "CA unavailable: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/x-pem-file")
	w.Header().Set("Content-Disposition", `attachment; filename="vpsm-datasaver-ca.pem"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(pem)
}
