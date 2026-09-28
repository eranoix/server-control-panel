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

func TestAssignSession_Unauthenticated(t *testing.T) {
	r := newAssignRouter(t)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/terminal/assign-session", strings.NewReader(`{"name":"s","target":"*"}`))
	r.handleTerminalAssignSession(w, req)
	if w.Code != 401 {
		t.Fatalf("unauth assign = %d, want 401", w.Code)
	}
}
