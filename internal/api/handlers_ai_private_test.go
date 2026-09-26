package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// privAIReq fires a request at the full router (through auth.Middleware).
// user="" sends no token.
func privAIReq(t *testing.T, r *Router, method, path, body, user string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if user != "" {
		tok, _, err := r.auth.Issue(user, nil)
		if err != nil {
			t.Fatalf("auth.Issue(%q): %v", user, err)
		}
		req.AddCookie(&http.Cookie{Name: "vpsm_token", Value: tok})
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// The private-ai token routes must exist (never silently dropped) and gate on
// auth. The smoke router has no secrets vault configured, so an authenticated
// request degrades to 503 ("admin token not configured") rather than crashing.
func TestPrivateAITokenRoutesGatedAndDegrade(t *testing.T) {
	r := newSmokeRouter(t)

	paths := []struct{ method, path string }{
		{"GET", "/api/private-ai/tokens"},
		{"GET", "/api/private-ai/status"},
		{"POST", "/api/private-ai/tokens/1/revoke"},
	}

	for _, p := range paths {
		// No token → 401 (route exists; never 404 — guards against silent
		// route deletion on a future heal/refactor).
		if w := privAIReq(t, r, p.method, p.path, "", ""); w.Code != 401 {
			t.Fatalf("%s %s no-token: got %d, want 401; body=%s", p.method, p.path, w.Code, w.Body.String())
		}
		// Authed but no admin secret configured → 503 with a clear hint.
		w := privAIReq(t, r, p.method, p.path, "", "sam")
		if w.Code != 503 {
			t.Fatalf("%s %s authed: got %d, want 503; body=%s", p.method, p.path, w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "private_ai_admin_token") {
			t.Fatalf("%s %s 503 body should mention the secret key; got %s", p.method, p.path, w.Body.String())
		}
	}
}
