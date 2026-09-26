package mobilebff

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/config"
	"server-control-panel/internal/mobilebff/sdui"
)

func screensTestCfg() *config.Config {
	return &config.Config{
		SchemaVersion: config.CurrentSchemaVersion,
		Primary:       "screenadmin",
		Users: []config.User{
			{Username: "screenadmin", PasswordHash: "h"},
			{Username: "screenviewer", PasswordHash: "h"},
		},
	}
}

// Two test screens, registered once for the whole package:
//   - "test.screens.basic": every authenticated Viewer sees the same screen —
//     used to prove the happy path (200, sdui_version, screen.id, every
//     component in a single call).
//   - "test.screens.adminonly": returns sdui.ErrScreenNotFound for a non-admin
//     Viewer — used to prove that "does not exist" and "exists but is not
//     yours" are indistinguishable over HTTP.
func init() {
	sdui.Register("test.screens.basic", func(ctx context.Context, v sdui.Viewer) (*sdui.Envelope, error) {
		return &sdui.Envelope{
			Screen: sdui.Screen{
				ID:    "test.screens.basic",
				Title: "Basic Test Screen",
				Components: []sdui.Component{
					sdui.ActionComponent{
						ComponentBase: sdui.ComponentBase{Type: sdui.ComponentTypeAction, ID: "a1"},
						Label:         "Run",
						ActionID:      "test.basic.action",
					},
					sdui.DetailComponent{
						ComponentBase: sdui.ComponentBase{Type: sdui.ComponentTypeDetail, ID: "d1"},
						DataSource:    sdui.DataSource{Endpoint: "/detail"},
						Fields:        []sdui.DetailField{{Key: "k", Label: "K"}},
					},
				},
			},
		}, nil
	})

	sdui.Register("test.screens.adminonly", func(ctx context.Context, v sdui.Viewer) (*sdui.Envelope, error) {
		if !v.IsAdmin() {
			return nil, sdui.ErrScreenNotFound
		}
		return &sdui.Envelope{
			Screen: sdui.Screen{ID: "test.screens.adminonly", Title: "Admin Only"},
		}, nil
	})
}

// Test 1: an unauthenticated request returns 401 — inherited from the protected
// mux (requireAuth), not reimplemented here.
func TestHandleScreen_Unauthenticated(t *testing.T) {
	mux := http.NewServeMux()
	Mount(mux, Deps{Cfg: screensTestCfg()})

	req := httptest.NewRequest(http.MethodGet, "/api/mobile/v1/screens/test.screens.basic", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d (body=%s)", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}
}

// Test 2: an authenticated GET of a registered screen returns 200,
// Content-Type: application/json, the current sdui_version and a matching screen.id.
func TestHandleScreen_Authenticated_ReturnsFilteredEnvelope(t *testing.T) {
	mux := http.NewServeMux()
	Mount(mux, Deps{Cfg: screensTestCfg()})

	req := httptest.NewRequest(http.MethodGet, "/api/mobile/v1/screens/test.screens.basic", nil)
	req = req.WithContext(auth.WithUser(req.Context(), "screenviewer"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}

	var env sdui.Envelope
	body := rec.Body.Bytes()
	parsed, err := sdui.UnmarshalScreen(body)
	if err != nil {
		t.Fatalf("UnmarshalScreen: %v (body=%s)", err, body)
	}
	env = *parsed

	if env.SDUIVersion != sdui.CurrentSDUIVersion {
		t.Errorf("sdui_version = %d, want %d", env.SDUIVersion, sdui.CurrentSDUIVersion)
	}
	if env.Screen.ID != "test.screens.basic" {
		t.Errorf("screen.id = %q, want %q", env.Screen.ID, "test.screens.basic")
	}
}

// Test 3: a GET of an unregistered id returns 404 with the body
// {"error":"screen_not_found"}.
func TestHandleScreen_UnregisteredID_404(t *testing.T) {
	mux := http.NewServeMux()
	Mount(mux, Deps{Cfg: screensTestCfg()})

	req := httptest.NewRequest(http.MethodGet, "/api/mobile/v1/screens/test.screens.does.not.exist", nil)
	req = req.WithContext(auth.WithUser(req.Context(), "screenviewer"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body=%s)", rec.Code, rec.Body.String())
	}
	wantBody := `{"error":"screen_not_found"}` + "\n"
	if rec.Body.String() != wantBody {
		t.Errorf("body = %q, want %q", rec.Body.String(), wantBody)
	}
}

// Test 4 (anti-enumeration): a screen registered with an admin-only builder
// returns, for a non-admin Viewer, a 404 byte-identical to the one for an id
// that never existed — the response must not distinguish "does not exist" from
// "is not yours". This is the proof of the 404-not-403 property.
func TestHandleScreen_NotPermitted_Is404IdenticalToUnknown(t *testing.T) {
	mux := http.NewServeMux()
	Mount(mux, Deps{Cfg: screensTestCfg()})

	doGet := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req = req.WithContext(auth.WithUser(req.Context(), "screenviewer")) // non-admin
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	unknownRec := doGet("/api/mobile/v1/screens/test.screens.does.not.exist")
	notPermittedRec := doGet("/api/mobile/v1/screens/test.screens.adminonly")

	if unknownRec.Code != http.StatusNotFound || notPermittedRec.Code != http.StatusNotFound {
		t.Fatalf("status = %d (unknown) / %d (not permitted), want both 404",
			unknownRec.Code, notPermittedRec.Code)
	}
	if unknownRec.Body.String() != notPermittedRec.Body.String() {
		t.Errorf("different bodies — unknown id: %q, not-permitted id: %q",
			unknownRec.Body.String(), notPermittedRec.Body.String())
	}
	if got, want := unknownRec.Header().Get("Content-Type"), notPermittedRec.Header().Get("Content-Type"); got != want {
		t.Errorf("Content-Type diverges — unknown id: %q, not-permitted id: %q", got, want)
	}

	// Non-vacuity proof: an admin Viewer DOES see the same screen.
	req := httptest.NewRequest(http.MethodGet, "/api/mobile/v1/screens/test.screens.adminonly", nil)
	req = req.WithContext(auth.WithUser(req.Context(), "screenadmin"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin: status = %d, want 200 (body=%s) — without this, the test above would be vacuous", rec.Code, rec.Body.String())
	}
}

// Test 5: the whole screen arrives in a SINGLE request — no redirect, with
// every component the builder produced present in the body.
func TestHandleScreen_WholeScreenInOneCall(t *testing.T) {
	mux := http.NewServeMux()
	Mount(mux, Deps{Cfg: screensTestCfg()})

	req := httptest.NewRequest(http.MethodGet, "/api/mobile/v1/screens/test.screens.basic", nil)
	req = req.WithContext(auth.WithUser(req.Context(), "screenviewer"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code >= 300 && rec.Code < 400 {
		t.Fatalf("status = %d, expected a direct response with no redirect", rec.Code)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{`"id":"a1"`, `"id":"d1"`, "test.basic.action"} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %q — not every component from the builder arrived in a single call: %s", want, body)
		}
	}
}
