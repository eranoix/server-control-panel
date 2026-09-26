package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/config"
	ptysvc "server-control-panel/internal/pty"
)

// newAssignRouter builds a minimal Router with cfg (sam=primary, jordan=admin,
// rando=plain) and an Ownership in a tempdir, enough for the gate + Assign.
func newAssignRouter(t *testing.T) *Router {
	t.Helper()
	own, err := ptysvc.LoadOwnership(filepath.Join(t.TempDir(), "own.json"))
	if err != nil {
		t.Fatalf("LoadOwnership: %v", err)
	}
	cfg := &config.Config{
		Primary: "sam",
		Users: []config.User{
			{Username: "jordan", Admin: true},
			{Username: "rando", Admin: false},
		},
	}
	return &Router{cfg: cfg, sessionOwn: own}
}

func assignReq(user, jsonBody string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/terminal/assign-session", strings.NewReader(jsonBody))
	return req.WithContext(auth.WithUser(req.Context(), user))
}

// TestAssignSession_AdminToAll: an admin reassigns to "Todos" → 200 and
// ownership becomes "*".
func TestAssignSession_AdminToAll(t *testing.T) {
	r := newAssignRouter(t)
	w := httptest.NewRecorder()
	r.handleTerminalAssignSession(w, assignReq("sam", `{"name":"sess","target":"*"}`))
	if w.Code != 200 {
		t.Fatalf("admin assign '*' = %d, want 200 (body %s)", w.Code, w.Body.String())
	}
	if got := r.sessionOwn.Owner("sess"); got != "*" {
		t.Errorf("owner after assign = %q, want '*'", got)
	}
}

// TestAssignSession_AdminToUser: an admin reassigns to another valid user → 200.
func TestAssignSession_AdminToUser(t *testing.T) {
	r := newAssignRouter(t)
	w := httptest.NewRecorder()
	r.handleTerminalAssignSession(w, assignReq("jordan", `{"name":"sess","target":"rando"}`))
	if w.Code != 200 {
		t.Fatalf("admin assign user = %d, want 200 (body %s)", w.Code, w.Body.String())
	}
	if got := r.sessionOwn.Owner("sess"); got != "rando" {
		t.Errorf("owner = %q, want rando", got)
	}
}

// TestAssignSession_NonAdminForbidden: non-admin → 403 and nothing changes.
func TestAssignSession_NonAdminForbidden(t *testing.T) {
	r := newAssignRouter(t)
	w := httptest.NewRecorder()
	r.handleTerminalAssignSession(w, assignReq("rando", `{"name":"sess","target":"*"}`))
	if w.Code != 403 {
		t.Fatalf("non-admin assign = %d, want 403", w.Code)
	}
	if got := r.sessionOwn.Owner("sess"); got != "" {
		t.Errorf("non-admin assign mutated ownership to %q", got)
	}
}

// TestAssignSession_InvalidTarget: a target that is neither a user nor "*" → 400.
func TestAssignSession_InvalidTarget(t *testing.T) {
	r := newAssignRouter(t)
	w := httptest.NewRecorder()
	r.handleTerminalAssignSession(w, assignReq("sam", `{"name":"sess","target":"ghost"}`))
	if w.Code != 400 {
		t.Fatalf("invalid target = %d, want 400", w.Code)
	}
	if got := r.sessionOwn.Owner("sess"); got != "" {
		t.Errorf("invalid target still wrote ownership %q", got)
	}
}

// TestAssignSession_Unauthenticated: no user in the context → 401.
func TestAssignSession_Unauthenticated(t *testing.T) {
	r := newAssignRouter(t)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/terminal/assign-session", strings.NewReader(`{"name":"s","target":"*"}`))
	r.handleTerminalAssignSession(w, req)
	if w.Code != 401 {
		t.Fatalf("unauth assign = %d, want 401", w.Code)
	}
}
