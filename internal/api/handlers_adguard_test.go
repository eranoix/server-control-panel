package api

import (
	"strings"
	"testing"
)

// The AdGuard routes have to exist (never vanish in a heal/refactor) and be
// gated by auth. The smoke router has no vault with credentials, so an
// authenticated request degrades to 503 (with a hint about the secret) instead of crashing.
func TestAdguardRoutesGatedAndDegrade(t *testing.T) {
	r := newSmokeRouter(t)

	paths := []struct{ method, path string }{
		{"GET", "/api/adguard/status"},
		{"POST", "/api/adguard/protection"},
	}

	for _, p := range paths {
		// No token → 401 (the route exists; never 404 — this guards against silent
		// removal in a future refactor).
		if w := privAIReq(t, r, p.method, p.path, "", ""); w.Code != 401 {
			t.Fatalf("%s %s without token: got %d, want 401; body=%s", p.method, p.path, w.Code, w.Body.String())
		}
		// Authenticated but with no credential in the vault → 503 with a clear hint.
		w := privAIReq(t, r, p.method, p.path, `{"enabled":true}`, "sam")
		if w.Code != 503 {
			t.Fatalf("%s %s autenticado: got %d, want 503; body=%s", p.method, p.path, w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "adguard_password") {
			t.Fatalf("%s %s 503 body should mention the secret; got %s", p.method, p.path, w.Body.String())
		}
	}
}
