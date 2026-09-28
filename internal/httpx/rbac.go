package httpx

import (
	"net/http"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/config"
)

func AuditEvent(audit *auth.AuditLog, req *http.Request, user, action, target string) {
	if audit == nil {
		return
	}
	audit.Append(auth.Event{
		User:   user,
		Action: action,
		Target: target,
		IP:     auth.ClientIP(req),
	})
}

func IsAdmin(cfg *config.Config, user string) bool {
	if cfg == nil || user == "" {
		return false
	}
	return cfg.IsAdmin(user)
}

func IsPrimary(cfg *config.Config, user string) bool {
	return IsAdmin(cfg, user)
}

func MustPrimary(w http.ResponseWriter, req *http.Request, cfg *config.Config, audit *auth.AuditLog) (string, bool) {
	caller := auth.UserFrom(req)
	if caller == "" {
		WriteErr(w, 401, "unauthorized")
		return "", false
	}
	if !IsPrimary(cfg, caller) {
		AuditEvent(audit, req, caller, "rbac.denied", "admin-only endpoint "+req.URL.Path)
		WriteErr(w, 403, "admin only")
		return caller, false
	}
	return caller, true
}
