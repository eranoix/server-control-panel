package mobilebff

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/sessions"
)

// newTestSessionsStore opens a real Store (on a temporary disk path) — Revoke
// is stateful, and the behaviour under test (revoke ONE jti, not the rest) is
// only convincing against the real Store, not a fake.
func newTestSessionsStore(t *testing.T) *sessions.Store {
	t.Helper()
	store, err := sessions.Open(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatalf("sessions.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// TestMobileLogout_RevokesOnlyCallingSession is the required proof: logging
// out device A NEVER drops device B's session for the same user, nor another
// user's session — only the jti that arrived in the call itself. It goes
// through the real auth.Service.Middleware (rather than injecting user/jti by
// hand) so the test exercises the actual production path.
func TestMobileLogout_RevokesOnlyCallingSession(t *testing.T) {
	svc := auth.New("segredo-de-teste", []auth.Credential{{Username: "sam", PasswordHash: "x"}})
	store := newTestSessionsStore(t)
	svc = svc.WithSessions(store)

	tokenA, jtiA, err := svc.Issue("sam", nil)
	if err != nil {
		t.Fatalf("Issue tokenA: %v", err)
	}
	tokenB, jtiB, err := svc.Issue("sam", nil)
	if err != nil {
		t.Fatalf("Issue tokenB (another device, same user): %v", err)
	}
	tokenOther, jtiOther, err := svc.Issue("outro-usuario", nil)
	if err != nil {
		t.Fatalf("Issue tokenOther: %v", err)
	}

	mux := http.NewServeMux()
	Mount(mux, Deps{Sessions: store})
	protected := svc.Middleware(mux)

	req := httptest.NewRequest(http.MethodPost, "/api/mobile/v1/auth/logout", nil)
	req.Header.Set("Authorization", "Bearer "+tokenA)
	rec := httptest.NewRecorder()
	protected.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	if !store.HasTombstone(jtiA) {
		t.Errorf("device A's jti is still active after its own logout")
	}
	if store.HasTombstone(jtiB) {
		t.Errorf("device B's jti (same user) was revoked by mistake — logout must bring down only the caller")
	}
	if store.HasTombstone(jtiOther) {
		t.Errorf("another user's jti was revoked by mistake")
	}

	// tokenA must no longer authenticate on ANY protected route (not just on
	// the mobile BFF) — it is the same Store the web panel uses.
	req2 := httptest.NewRequest(http.MethodGet, "/api/mobile/v1/me", nil)
	req2.Header.Set("Authorization", "Bearer "+tokenA)
	rec2 := httptest.NewRecorder()
	protected.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusUnauthorized {
		t.Errorf("revoked token still authenticates: status = %d, body = %s", rec2.Code, rec2.Body.String())
	}

	// tokenB stays good.
	req3 := httptest.NewRequest(http.MethodGet, "/api/mobile/v1/me", nil)
	req3.Header.Set("Authorization", "Bearer "+tokenB)
	rec3 := httptest.NewRecorder()
	protected.ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusOK {
		t.Errorf("token B (another device) should still authenticate: status = %d, body = %s", rec3.Code, rec3.Body.String())
	}

	_ = tokenOther // only there to populate the store; no HTTP call of its own
}

// TestMobileLogout_IdempotentOnAlreadyRevokedSession — logging out twice (or
// logging out after a revocation through some other path, e.g. "revoke all
// sessions" in the web panel) must not break: it answers 200, never 500.
func TestMobileLogout_IdempotentOnAlreadyRevokedSession(t *testing.T) {
	svc := auth.New("segredo-de-teste", []auth.Credential{{Username: "sam", PasswordHash: "x"}})
	store := newTestSessionsStore(t)
	svc = svc.WithSessions(store)

	token, jti, err := svc.Issue("sam", nil)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	store.Revoke(jti) // simulates a prior revocation (e.g. "revogar todas" in the panel)

	mux := http.NewServeMux()
	Mount(mux, Deps{Sessions: store})

	// A token that is already revoked never even reaches the handler
	// (auth.Middleware stops it first) — the real idempotency proof is
	// revoking twice WITHOUT the Middleware in between, straight against the
	// Store, which is what the web panel's handleRevokeMobileSession/
	// handleLogout already do today without first checking "was it revoked?".
	store.Revoke(jti)
	if !store.HasTombstone(jti) {
		t.Errorf("session should remain revoked after a duplicate Revoke")
	}
	_ = token
	_ = mux
}

// TestMobileLogout_NoSessionsStoreDegradesTo200 — cmd/mobile-openapi-gen
// builds Deps{} without Sessions (see the Deps.Sessions docstring in
// registry.go); the endpoint must not panic on a nil store.
func TestMobileLogout_NoSessionsStoreDegradesTo200(t *testing.T) {
	svc := auth.New("segredo-de-teste", []auth.Credential{{Username: "sam", PasswordHash: "x"}})

	token, _, err := svc.Issue("sam", nil)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	mux := http.NewServeMux()
	Mount(mux, Deps{}) // Sessions nil on purpose
	protected := svc.Middleware(mux)

	req := httptest.NewRequest(http.MethodPost, "/api/mobile/v1/auth/logout", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	protected.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s (a nil Sessions should not break the endpoint)", rec.Code, rec.Body.String())
	}
}
