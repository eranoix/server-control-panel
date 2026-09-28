package mobilebff

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"server-control-panel/internal/auth"
	ptysvc "server-control-panel/internal/pty"
)

func newTestOwnership(t *testing.T) *ptysvc.Ownership {
	t.Helper()
	o, err := ptysvc.LoadOwnership(filepath.Join(t.TempDir(), "session-ownership.json"))
	if err != nil {
		t.Fatalf("LoadOwnership: %v", err)
	}
	return o
}

func TestTerminalSessions_Unauthenticated(t *testing.T) {
	mux := http.NewServeMux()
	Mount(mux, Deps{})

	req := httptest.NewRequest(http.MethodGet, "/api/mobile/v1/terminal/sessions", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (body=%s)", rec.Code, rec.Body.String())
	}
}

func TestTerminalSessions_Authenticated_EmptyIsGracefulNotError(t *testing.T) {
	mux := http.NewServeMux()
	Mount(mux, Deps{SessionOwn: newTestOwnership(t)})

	req := httptest.NewRequest(http.MethodGet, "/api/mobile/v1/terminal/sessions", nil)
	req = req.WithContext(auth.WithUser(req.Context(), "sam"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var body []TerminalSessionSummary
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, rec.Body.String())
	}
	if body == nil {
		t.Errorf("body must be [] and not null when there are no sessions")
	}
}

func TestTerminalWSTicket_Unauthenticated(t *testing.T) {
	mux := http.NewServeMux()
	Mount(mux, Deps{})

	req := httptest.NewRequest(http.MethodPost, "/api/mobile/v1/terminal/ws-ticket", strings.NewReader(`{"name":"main"}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (body=%s)", rec.Code, rec.Body.String())
	}
}

func TestTerminalWSTicket_NewSessionName_AllowsAnyAuthenticatedUser(t *testing.T) {
	own := newTestOwnership(t)
	mux := http.NewServeMux()
	Mount(mux, Deps{SessionOwn: own})

	req := httptest.NewRequest(http.MethodPost, "/api/mobile/v1/terminal/ws-ticket", strings.NewReader(`{"name":"never-existed"}`))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(auth.WithUser(req.Context(), "sam"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var body WSTicketResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, rec.Body.String())
	}
	if body.Ticket == "" {
		t.Errorf("empty ticket, expected a one-shot ticket")
	}
	if body.ExpiresIn != int(auth.WSTicketTTL.Seconds()) {
		t.Errorf("expires_in = %d, want %d", body.ExpiresIn, int(auth.WSTicketTTL.Seconds()))
	}
}

func TestTerminalWSTicket_OwnedByOtherUser_404NotFound(t *testing.T) {
	own := newTestOwnership(t)
	if err := own.Claim("jordans-private", "jordan"); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	mux := http.NewServeMux()
	Mount(mux, Deps{SessionOwn: own})

	req := httptest.NewRequest(http.MethodPost, "/api/mobile/v1/terminal/ws-ticket", strings.NewReader(`{"name":"jordans-private"}`))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(auth.WithUser(req.Context(), "sam"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body=%s)", rec.Code, rec.Body.String())
	}
}

func TestTerminalWSTicket_AudienceAllSession_AllowsAnyUser(t *testing.T) {
	own := newTestOwnership(t)
	if err := own.Assign("shared-room", ptysvc.AudienceAll); err != nil {
		t.Fatalf("Assign: %v", err)
	}
	mux := http.NewServeMux()
	Mount(mux, Deps{SessionOwn: own})

	req := httptest.NewRequest(http.MethodPost, "/api/mobile/v1/terminal/ws-ticket", strings.NewReader(`{"name":"shared-room"}`))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(auth.WithUser(req.Context(), "sam"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
}

func TestTerminalWSTicket_OwnSession_Allowed(t *testing.T) {
	own := newTestOwnership(t)
	if err := own.Claim("my-session", "sam"); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	mux := http.NewServeMux()
	Mount(mux, Deps{SessionOwn: own})

	req := httptest.NewRequest(http.MethodPost, "/api/mobile/v1/terminal/ws-ticket", strings.NewReader(`{"name":"my-session"}`))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(auth.WithUser(req.Context(), "sam"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
}

func TestTerminalScrollback_Unauthenticated(t *testing.T) {
	mux := http.NewServeMux()
	Mount(mux, Deps{})

	req := httptest.NewRequest(http.MethodGet, "/api/mobile/v1/terminal/scrollback?name=main", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (body=%s)", rec.Code, rec.Body.String())
	}
}

func TestTerminalScrollback_NotOwned_404NotFound(t *testing.T) {
	own := newTestOwnership(t)
	if err := own.Claim("jordans-session", "jordan"); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	mux := http.NewServeMux()
	Mount(mux, Deps{SessionOwn: own})

	req := httptest.NewRequest(http.MethodGet, "/api/mobile/v1/terminal/scrollback?name=jordans-session", nil)
	req = req.WithContext(auth.WithUser(req.Context(), "sam"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body=%s)", rec.Code, rec.Body.String())
	}
}

func TestTerminalRawLog_Unauthenticated(t *testing.T) {
	mux := http.NewServeMux()
	Mount(mux, Deps{})

	req := httptest.NewRequest(http.MethodGet, "/api/mobile/v1/terminal/raw-log?name=main", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (body=%s)", rec.Code, rec.Body.String())
	}
}

func TestTerminalRawLog_NotOwned_404NotFound(t *testing.T) {
	own := newTestOwnership(t)
	if err := own.Claim("jordans-session", "jordan"); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	mux := http.NewServeMux()
	Mount(mux, Deps{SessionOwn: own})

	req := httptest.NewRequest(http.MethodGet, "/api/mobile/v1/terminal/raw-log?name=jordans-session", nil)
	req = req.WithContext(auth.WithUser(req.Context(), "sam"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body=%s)", rec.Code, rec.Body.String())
	}
}
