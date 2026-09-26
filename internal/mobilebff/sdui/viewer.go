package sdui

import (
	"server-control-panel/internal/config"
	"server-control-panel/internal/httpx"
)

// Viewer is the authenticated identity a Builder filters a screen against:
// who is asking, and what that person may see/do. It is the ONLY input a
// Builder may base a permission decision on — the decision happens in Go,
// inside the Builder, never as a condition shipped in the payload for the
// client to evaluate (a "hidden": true in the JSON is exactly the leak that
// filtering-by-omission exists to prevent).
//
// RBAC in this project is binary admin/non-admin (see internal/httpx/rbac.go);
// Viewer introduces neither a role enum nor a permission matrix — that would
// be inventing a second authorization model parallel to the panel's.
type Viewer struct {
	// Username is the authenticated user, or "" for an unknown identity
	// (never treated as admin — see ViewerFrom).
	Username string
	isAdmin  bool
}

// ViewerFrom builds a Viewer from the SAME admin check the rest of the panel
// uses (httpx.IsAdmin, which in turn delegates to cfg.IsAdmin). A deliberate
// single call site: if the package ever gains a more granular capability
// helper, it replaces this one line without any Builder having to change.
//
// An empty username returns a non-admin Viewer by default — an unknown
// identity is never privileged.
func ViewerFrom(cfg *config.Config, username string) Viewer {
	if username == "" {
		return Viewer{}
	}
	return Viewer{
		Username: username,
		isAdmin:  httpx.IsAdmin(cfg, username),
	}
}

// IsAdmin reports whether this Viewer has administrator privileges.
func (v Viewer) IsAdmin() bool { return v.isAdmin }

// Can reports whether this Viewer may exercise the named capability. Since
// this project's RBAC is binary, every named capability today is equivalent
// to "is admin" — Can exists as the extension point a Builder calls, so that
// a future granular capability list (nonexistent today) would not require
// rewriting any Builder, only this function.
func (v Viewer) Can(capability string) bool {
	_ = capability
	return v.isAdmin
}
