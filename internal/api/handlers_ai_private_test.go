package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func privAIReq(t *testing.T, r *Router, method, path, body, user string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if user != "" {
		tok, _, err := r.auth.Issue(user, nil)
		if err != nil {
			t.Fatalf("auth.Issue(%q): %v", user, err)
		}
		req.AddCookie(&http.Cookie{Name: "panel_token", Value: tok})
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestPrivateAITokenRoutesGatedAndDegrade(t *testing.T) {
	r := newSmokeRouter(t)

	paths := []struct{ method, path string }{
		{"GET", "/api/private-ai/tokens"},
		{"GET", "/api/private-ai/status"},
		{"POST", "/api/private-ai/tokens/1/revoke"},
	}

	for _, p := range paths {
		if w := privAIReq(t, r, p.method, p.path, "", ""); w.Code != 401 {
			t.Fatalf("%s %s no-token: got %d, want 401; body=%s", p.method, p.path, w.Code, w.Body.String())
		}
		w := privAIReq(t, r, p.method, p.path, "", "sam")
		if w.Code != 503 {
			t.Fatalf("%s %s authed: got %d, want 503; body=%s", p.method, p.path, w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "private_ai_admin_token") {
			t.Fatalf("%s %s 503 body should mention the secret key; got %s", p.method, p.path, w.Body.String())
		}
	}
}
