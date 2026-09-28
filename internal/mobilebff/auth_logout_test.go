package mobilebff

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/sessions"
)

func newTestSessionsStore(t *testing.T) *sessions.Store {
	t.Helper()
	store, err := sessions.Open(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatalf("sessions.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestMobileLogout_RevokesOnlyCallingSession(t *testing.T) {
	svc := auth.New("test-secret", []auth.Credential{{Username: "sam", PasswordHash: "x"}})
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
	tokenOther, jtiOther, err := svc.Issue("other-user", nil)
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

	req2 := httptest.NewRequest(http.MethodGet, "/api/mobile/v1/me", nil)
	req2.Header.Set("Authorization", "Bearer "+tokenA)
	rec2 := httptest.NewRecorder()
	protected.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusUnauthorized {
		t.Errorf("revoked token still authenticates: status = %d, body = %s", rec2.Code, rec2.Body.String())
	}

	req3 := httptest.NewRequest(http.MethodGet, "/api/mobile/v1/me", nil)
	req3.Header.Set("Authorization", "Bearer "+tokenB)
	rec3 := httptest.NewRecorder()
	protected.ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusOK {
		t.Errorf("token B (another device) should still authenticate: status = %d, body = %s", rec3.Code, rec3.Body.String())
	}

	_ = tokenOther
}

func TestMobileLogout_IdempotentOnAlreadyRevokedSession(t *testing.T) {
	svc := auth.New("test-secret", []auth.Credential{{Username: "sam", PasswordHash: "x"}})
	store := newTestSessionsStore(t)
	svc = svc.WithSessions(store)

	token, jti, err := svc.Issue("sam", nil)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	store.Revoke(jti)

	mux := http.NewServeMux()
	Mount(mux, Deps{Sessions: store})

	store.Revoke(jti)
	if !store.HasTombstone(jti) {
		t.Errorf("session should remain revoked after a duplicate Revoke")
	}
	_ = token
	_ = mux
}

func TestMobileLogout_NoSessionsStoreDegradesTo200(t *testing.T) {
	svc := auth.New("test-secret", []auth.Credential{{Username: "sam", PasswordHash: "x"}})

	token, _, err := svc.Issue("sam", nil)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	mux := http.NewServeMux()
	Mount(mux, Deps{})
	protected := svc.Middleware(mux)

	req := httptest.NewRequest(http.MethodPost, "/api/mobile/v1/auth/logout", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	protected.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s (a nil Sessions should not break the endpoint)", rec.Code, rec.Body.String())
	}
}
