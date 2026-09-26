package httpx

import (
	"net/http"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/config"
)

// AuditEvent records an event in the audit log. No-op when audit == nil (the
// configuration where boot failed to open the file).
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

// IsAdmin reports whether the user has system administrator privileges: the
// config.Primary account OR any user carrying the Admin flag. It is the
// privileged predicate behind every admin-only gate (shell runner, reboot,
// scheduler as root, user management, AI prompts…).
// Empty cfg or empty user → false.
func IsAdmin(cfg *config.Config, user string) bool {
	if cfg == nil || user == "" {
		return false
	}
	return cfg.IsAdmin(user)
}

// IsPrimary is kept as an alias of IsAdmin for the stability of the call sites:
// in this code "primary" has always meant "the privileged account", and the
// model now admits several admins (the primary is always one of them). Prefer
// IsAdmin in new code.
func IsPrimary(cfg *config.Config, user string) bool {
	return IsAdmin(cfg, user)
}

// MustPrimary is the canonical RBAC gate for admin-only endpoints.
// Returns (caller, true) when the caller is authenticated AND is the primary.
// Writes 401/403 and returns ok=false otherwise.
//
// Audit: every denied call is logged with action "rbac.denied" so the /audit
// page surfaces privilege-escalation attempts.
//
// Idiomatic use:
//
//	caller, ok := httpx.MustPrimary(w, req, cfg, audit)
//	if !ok { return }
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
