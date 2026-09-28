package api

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestTunnelDeviceRoutesGatedAndDegrade(t *testing.T) {
	r := newSmokeRouter(t)
	r.cfg.SingboxConfigPath = filepath.Join(t.TempDir(), "does-not-exist.json")

	if w := privAIReq(t, r, "GET", "/api/tunnel/devices", "", ""); w.Code != 401 {
		t.Fatalf("GET devices without a token: got %d, want 401; body=%s", w.Code, w.Body.String())
	}
	if w := privAIReq(t, r, "DELETE", "/api/tunnel/devices/abc", "", ""); w.Code != 401 {
		t.Fatalf("DELETE device without a token: got %d, want 401; body=%s", w.Code, w.Body.String())
	}

	w := privAIReq(t, r, "GET", "/api/tunnel/devices", "", "sam")
	if w.Code != 503 {
		t.Fatalf("GET devices authenticated: got %d, want 503; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "tunnel") {
		t.Fatalf("503 body should mention the tunnel; got %s", w.Body.String())
	}
}
