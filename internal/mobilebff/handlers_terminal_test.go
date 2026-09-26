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

	req := httptest.NewRequest(http.MethodPost, "/api/mobile/v1/terminal/ws-ticket", strings.NewReader(`{"name":"nunca-existiu"}`))
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

// TestTerminalWSTicket_OwnedByOtherUser_404NotFound is the ownership test: a
// name already claimed by ANOTHER specific user (not AudienceAll) never
// gets a ticket — 404, never 403, so as not to leak existence.
func TestTerminalWSTicket_OwnedByOtherUser_404NotFound(t *testing.T) {
	own := newTestOwnership(t)
	if err := own.Claim("privado-do-jordan", "jordan"); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	mux := http.NewServeMux()
	Mount(mux, Deps{SessionOwn: own})

	req := httptest.NewRequest(http.MethodPost, "/api/mobile/v1/terminal/ws-ticket", strings.NewReader(`{"name":"privado-do-jordan"}`))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(auth.WithUser(req.Context(), "sam"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body=%s)", rec.Code, rec.Body.String())
	}
}

// TestTerminalWSTicket_AudienceAllSession_AllowsAnyUser proves that "Todos"
// stays attachable by any authenticated user — the exception the threat
// model's "(non-AudienceAll)" parenthesis asks for.
func TestTerminalWSTicket_AudienceAllSession_AllowsAnyUser(t *testing.T) {
	own := newTestOwnership(t)
	if err := own.Assign("sala-compartilhada", ptysvc.AudienceAll); err != nil {
		t.Fatalf("Assign: %v", err)
	}
	mux := http.NewServeMux()
	Mount(mux, Deps{SessionOwn: own})

	req := httptest.NewRequest(http.MethodPost, "/api/mobile/v1/terminal/ws-ticket", strings.NewReader(`{"name":"sala-compartilhada"}`))
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
	if err := own.Claim("minha-sessao", "sam"); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	mux := http.NewServeMux()
	Mount(mux, Deps{SessionOwn: own})

	req := httptest.NewRequest(http.MethodPost, "/api/mobile/v1/terminal/ws-ticket", strings.NewReader(`{"name":"minha-sessao"}`))
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
	if err := own.Claim("sessao-do-jordan", "jordan"); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	mux := http.NewServeMux()
	Mount(mux, Deps{SessionOwn: own})

	req := httptest.NewRequest(http.MethodGet, "/api/mobile/v1/terminal/scrollback?name=sessao-do-jordan", nil)
	req = req.WithContext(auth.WithUser(req.Context(), "sam"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body=%s)", rec.Code, rec.Body.String())
	}
}

// The raw log is the terminal's literal transcript — its ownership gate
// matters more than that of any other route in this file, which is why both
// of its sides are tested here instead of being left to the general gate.
func TestTerminalRawLog_Unauthenticated(t *testing.T) {
	mux := http.NewServeMux()
	Mount(mux, Deps{})

	req := httptest.NewRequest(http.MethodGet, "/api/mobile/v1/terminal/log-bruto?name=main", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (body=%s)", rec.Code, rec.Body.String())
	}
}

func TestTerminalRawLog_NotOwned_404NotFound(t *testing.T) {
	own := newTestOwnership(t)
	if err := own.Claim("sessao-do-jordan", "jordan"); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	mux := http.NewServeMux()
	Mount(mux, Deps{SessionOwn: own})

	req := httptest.NewRequest(http.MethodGet, "/api/mobile/v1/terminal/log-bruto?name=sessao-do-jordan", nil)
	req = req.WithContext(auth.WithUser(req.Context(), "sam"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body=%s)", rec.Code, rec.Body.String())
	}
}
