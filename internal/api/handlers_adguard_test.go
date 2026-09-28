package api

import (
	"strings"
	"testing"
)

func TestAdguardRoutesGatedAndDegrade(t *testing.T) {
	r := newSmokeRouter(t)

	paths := []struct{ method, path string }{
		{"GET", "/api/adguard/status"},
		{"POST", "/api/adguard/protection"},
	}

	for _, p := range paths {
		if w := privAIReq(t, r, p.method, p.path, "", ""); w.Code != 401 {
			t.Fatalf("%s %s without token: got %d, want 401; body=%s", p.method, p.path, w.Code, w.Body.String())
		}
		w := privAIReq(t, r, p.method, p.path, `{"enabled":true}`, "sam")
		if w.Code != 503 {
			t.Fatalf("%s %s authenticated: got %d, want 503; body=%s", p.method, p.path, w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "adguard_password") {
			t.Fatalf("%s %s 503 body should mention the secret; got %s", p.method, p.path, w.Body.String())
		}
	}
}
