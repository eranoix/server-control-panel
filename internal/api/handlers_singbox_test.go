package api

import (
	"path/filepath"
	"strings"
	"testing"
)

// The tunnel device manager's routes have to exist (never vanish in a
// heal/refactor) and be gated. With a non-existent config path, an
// authenticated read degrades to 503 (tunnel not configured) instead of crashing.
func TestTunnelDeviceRoutesGatedAndDegrade(t *testing.T) {
	r := newSmokeRouter(t)
	// force a non-existent config path → List() fails deterministically
	r.cfg.SingboxConfigPath = filepath.Join(t.TempDir(), "nao-existe.json")

	// no token → 401 (the route exists; never 404)
	if w := privAIReq(t, r, "GET", "/api/tunnel/devices", "", ""); w.Code != 401 {
		t.Fatalf("GET devices without a token: got %d, want 401; body=%s", w.Code, w.Body.String())
	}
	if w := privAIReq(t, r, "DELETE", "/api/tunnel/devices/abc", "", ""); w.Code != 401 {
		t.Fatalf("DELETE device without a token: got %d, want 401; body=%s", w.Code, w.Body.String())
	}

	// autenticado, config ausente → 503 com dica
	w := privAIReq(t, r, "GET", "/api/tunnel/devices", "", "sam")
	if w.Code != 503 {
		t.Fatalf("GET devices autenticado: got %d, want 503; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "tunnel") {
		t.Fatalf("503 body should mention the tunnel; got %s", w.Body.String())
	}
}
