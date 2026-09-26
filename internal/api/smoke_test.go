package api

// smoke_test.go — Safety net for the big architectural refactor.
//
// It does not test business logic. It tests that critical routes:
//   1. Exist (do not return 404)
//   2. Answer with the expected status for valid/invalid input
//   3. Do not panic
//
// Runs in <2s. Brings the Router up in memory via NewRouter(cfg), with a minimal
// config (no supabase, no real secrets vault) — the point is to confirm that
// routing and the middlewares keep answering after every commit of the
// refactor.
//
// If a test in here breaks during the refactor: STOP. It was not a regression in
// some detail — it was a route that vanished or a middleware that broke.

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"server-control-panel/internal/config"
)

// newSmokeRouter builds a Router through NewRouter with a minimal config — no
// supabase, secrets disabled, queue/scheduler/whatsapp/videocall all
// gracefully degraded. The focus is exercising the routing, not the features.
func newSmokeRouter(t *testing.T) *Router {
	t.Helper()
	dir, err := os.MkdirTemp("", "vpsm-smoke-")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	// Write a minimal config straight into the DataDir
	cfg := &config.Config{
		SchemaVersion: 2,
		Primary:       "sam",
		Listen:        ":0",
		DataDir:       dir,
		JWTSecret:     "smoke-test-secret-not-real-32-chars",
		Users: []config.User{
			{Username: "sam", PasswordHash: ""},
		},
	}
	// Create the expected directory structure
	if err := os.MkdirAll(filepath.Join(dir, "users", "sam"), 0o755); err != nil {
		t.Fatalf("mkdir users: %v", err)
	}

	r, err := NewRouter(cfg)
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	t.Cleanup(func() {
		// Shutdown cleans up the sessions store + videocall.
		// Uses context.Background to avoid contaminating the test with a timeout.
		// Idempotent — calling it twice does not break anything.
		// Note: NewRouter may have failed to initialize subsystems; Shutdown
		// is defensive.
		defer func() { _ = recover() }()
		r.Shutdown(nil)
	})
	return r
}

// TestSmokePublicRoutes — every public route (no JWT) must answer without
// a 404 or a 500. The status varies by route.
func TestSmokePublicRoutes(t *testing.T) {
	r := newSmokeRouter(t)

	cases := []struct {
		name       string
		method     string
		path       string
		body       string
		wantStatus []int // any of them accepted
	}{
		{"health", "GET", "/api/health", "", []int{200}},
		{"health-detailed", "GET", "/api/health/detailed", "", []int{200}},
		{"metrics", "GET", "/metrics", "", []int{200}},
		{"manifest", "GET", "/manifest.webmanifest", "", []int{200}},
		{"sw.js", "GET", "/sw.js", "", []int{200}},
		{"icon-192", "GET", "/icon-192.png", "", []int{200, 404}}, // no failure if the asset does not exist
		{"recovery page", "GET", "/recovery", "", []int{200}},
		{"forward-auth no token", "GET", "/api/forward-auth", "", []int{200}}, // 200 with no header
		{"index html", "GET", "/", "", []int{200}},
		{"login bad json", "POST", "/api/auth/login", "not json", []int{400}},
		{"login bad creds", "POST", "/api/auth/login", `{"username":"nope","password":"nope"}`, []int{401, 400, 423}},
		{"login method get", "GET", "/api/auth/login", "", []int{405}},
		// refresh-cookie is PUBLIC (outside the protected mux) — with no vpsm_refresh cookie
		// it answers 401 from the handler ITSELF (not from the middleware). A 404 here = route gone.
		{"refresh-cookie no cookie", "POST", "/api/auth/refresh-cookie", "", []int{401}},
		{"refresh-cookie method get", "GET", "/api/auth/refresh-cookie", "", []int{405}},
		// /_docs exists (not 404) and is gated: with no JWT → 401/403 via mustPrimary.
		// Protects handleDocsReport + the route against silent removal by /heal.
		{"docs gated unauth", "GET", "/_docs", "", []int{401, 403}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var body *strings.Reader
			if c.body != "" {
				body = strings.NewReader(c.body)
			} else {
				body = strings.NewReader("")
			}
			req := httptest.NewRequest(c.method, c.path, body)
			if c.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			gotStatus := w.Code
			ok := false
			for _, s := range c.wantStatus {
				if s == gotStatus {
					ok = true
					break
				}
			}
			if !ok {
				snippet := w.Body.String()
				if len(snippet) > 200 {
					snippet = snippet[:200] + "..."
				}
				t.Fatalf("%s %s: got %d, want any of %v; body=%s",
					c.method, c.path, gotStatus, c.wantStatus, snippet)
			}
		})
	}
}

// TestRefreshCookieIsPublicAndWired — /api/auth/refresh-cookie MUST be public
// (it renews the JWT through an HttpOnly cookie once the access token has
// expired; that is what stops auto-logout tearing down the session/terminal in an
// idle tab). With no vpsm_refresh cookie, the handler ITSELF answers 401 "no
// refresh session" — not the middleware's "unauthorized". A 404 here = route gone
// (a regression that would reopen the bug of the terminal dropping on its own).
func TestRefreshCookieIsPublicAndWired(t *testing.T) {
	r := newSmokeRouter(t)

	req := httptest.NewRequest("POST", "/api/auth/refresh-cookie", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code == 404 {
		t.Fatalf("/api/auth/refresh-cookie returned 404 — route disappeared (regression)")
	}
	if w.Code != 401 {
		t.Fatalf("without cookie expected 401, got %d; body=%s", w.Code, w.Body.String())
	}
	// Proves it was OUR handler (public route) and not the protected middleware:
	// the body carries the handler's own specific message.
	if body := w.Body.String(); !strings.Contains(body, "no refresh session") {
		t.Fatalf("expected handler body ('no refresh session'), got: %s", body)
	}
}

// TestSmokeDocsGated — the /_docs route (technical report) is gated by
// mustPrimary: with no JWT it must answer 401/403, NEVER 404 (404 = route gone,
// e.g. deleted by /heal) and never 200 (200 without auth = the gate leaked, and
// the report is exposed to anyone).
//
// This test is the target of the comment-armor in handlers_docs.go and api.go
// ("Do not remove without updating TestSmokeDocsGated"). It did NOT use to exist
// as a function of its own — the real coverage was only the "docs gated unauth"
// subtest inside TestSmokePublicRoutes, so the armor pointed at a ghost
// (`go test -list` did not list it). Now the citation is true.
func TestSmokeDocsGated(t *testing.T) {
	r := newSmokeRouter(t)

	req := httptest.NewRequest("GET", "/_docs", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	switch w.Code {
	case 401, 403:
		// expected: the mustPrimary gate refused the unauthenticated access.
	case 404:
		t.Fatalf("GET /_docs: 404 — the /_docs route disappeared (deleted by /heal? handler removed?)")
	default:
		snippet := w.Body.String()
		if len(snippet) > 200 {
			snippet = snippet[:200] + "..."
		}
		t.Fatalf("GET /_docs without token: got %d, want 401/403 (mustPrimary gate leaked?); body=%s",
			w.Code, snippet)
	}
}

// TestSmokeGraphGated — the /_graph route (viewer for the Graphify knowledge
// base) is gated by mustPrimary just like /_docs: with no JWT it must answer
// 401/403, NEVER 404 (route gone, e.g. deleted by /heal) and never 200 (gate
// leaked, exposing the graphs to anyone). Target of the comment-armor in
// handlers_docs.go and api.go ("Do not remove without updating TestSmokeGraphGated").
func TestSmokeGraphGated(t *testing.T) {
	r := newSmokeRouter(t)

	req := httptest.NewRequest("GET", "/_graph?project=vps-manager", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	switch w.Code {
	case 401, 403:
		// expected: the mustPrimary gate refused the unauthenticated access.
	case 404:
		t.Fatalf("GET /_graph: 404 — the /_graph route disappeared (deleted by /heal? handler removed?)")
	default:
		snippet := w.Body.String()
		if len(snippet) > 200 {
			snippet = snippet[:200] + "..."
		}
		t.Fatalf("GET /_graph without token: got %d, want 401/403 (mustPrimary gate leaked?); body=%s",
			w.Code, snippet)
	}
}

// TestSmokeCodeGated — the /_code route (native VSCode / code-server) is gated by
// mustPrimary just like /_docs and /_graph: with no JWT it must answer 401/403,
// NEVER 404 (route gone, e.g. deleted by /heal) and never 200 (gate leaked — and
// here the leak would be CRITICAL: /_code exposes a ROOT SHELL over the web). The
// gate refuses before dialling the upstream, so the test does not depend on the
// service being up. Target of the comment-armor in handlers_code.go and api.go
// ("Do not remove without updating TestSmokeCodeGated").
func TestSmokeCodeGated(t *testing.T) {
	r := newSmokeRouter(t)

	req := httptest.NewRequest("GET", "/_code/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	switch w.Code {
	case 401, 403:
		// expected: the mustPrimary gate refused the unauthenticated access.
	case 404:
		t.Fatalf("GET /_code/: 404 — the /_code route disappeared (deleted by /heal? handler removed?)")
	default:
		snippet := w.Body.String()
		if len(snippet) > 200 {
			snippet = snippet[:200] + "..."
		}
		t.Fatalf("GET /_code/ without token: got %d, want 401/403 (mustPrimary gate leaked? shell root exposed!); body=%s",
			w.Code, snippet)
	}
}

// TestSmokeProtectedRoutesRequire401 — protected routes must return 401
// with no JWT. Confirms auth.Middleware is active.
func TestSmokeProtectedRoutesRequire401(t *testing.T) {
	r := newSmokeRouter(t)

	protectedPaths := []string{
		"/api/auth/me",
		"/api/telemetry",
		"/api/auth/refresh",
		"/api/auth/logout",
		"/api/auth/sessions",
		"/api/auth/totp/status",
		"/api/auth/mfa/status",
		"/api/system/stats",
		"/api/system/listening",
		"/api/system/connections",
		"/api/system/units",
		"/api/docker/info",
		"/api/docker/containers",
		"/api/docker/images",
		"/api/docker/volumes",
		"/api/docker/networks",
		"/api/jira/config",
		"/api/jira/health",
		"/api/jira/projects",
		"/api/queue",
		"/api/scheduler/jobs",
		"/api/todos",
		"/api/procs",
		"/api/users",
		"/api/audit/tail",
		"/api/audit/actions",
		"/api/claude/overview",
		"/api/config",
		"/api/vpsm/health",
		"/api/session/bandwidth",
		"/api/files/list",
		"/api/secrets/list",
		"/api/admin/alerting",
		"/api/user/prefs",
		"/api/metrics/rules",
		"/api/terminal/state",
		"/api/terminal/sessions",
		"/api/terminal/backups",
		"/api/browser-instances",
	}

	for _, p := range protectedPaths {
		t.Run(p, func(t *testing.T) {
			req := httptest.NewRequest("GET", p, nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != 401 {
				snippet := w.Body.String()
				if len(snippet) > 200 {
					snippet = snippet[:200] + "..."
				}
				t.Fatalf("GET %s without token: got %d, want 401 (route disappeared? middleware broke?); body=%s",
					p, w.Code, snippet)
			}
		})
	}
}

// TestSmokeHealthDetailedShape — confirms /api/health/detailed answers
// structured JSON. Breaking that shape would break the dashboard.
func TestSmokeHealthDetailedShape(t *testing.T) {
	r := newSmokeRouter(t)

	req := httptest.NewRequest("GET", "/api/health/detailed", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status %d, want 200", w.Code)
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// /api/health/detailed currently returns {subsystems: {...}, time: ...}.
	// We lock that shape — the frontend depends on it.
	if _, ok := resp["subsystems"]; !ok {
		t.Fatalf("missing subsystems field; got %v", resp)
	}
}

// TestSmokeHealthMinimalShape — `/api/health` has to return ok:true or
// ok:false and carry "checks". The frontend depends on it to show the status bar.
func TestSmokeHealthMinimalShape(t *testing.T) {
	r := newSmokeRouter(t)

	req := httptest.NewRequest("GET", "/api/health", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status %d, want 200", w.Code)
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := resp["ok"]; !ok {
		t.Fatalf("missing ok field; got %v", resp)
	}
}

func TestSmokeSecurityHeaders(t *testing.T) {
	r := newSmokeRouter(t)

	req := httptest.NewRequest("GET", "/api/health", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	for _, h := range []string{"X-Content-Type-Options", "Referrer-Policy"} {
		if w.Header().Get(h) == "" {
			t.Errorf("missing security header %q (securityHeaders middleware quebrou?)", h)
		}
	}
}

// TestSmokeTelemetryWired: the 401 above proves /api/telemetry is NOT anonymous,
// but it does NOT prove it EXISTS — auth.Middleware answers 401 BEFORE the
// protected mux looks for the pattern, so a route that was never registered would
// pass that test. A false green on a missing route would cost 14 days of an empty
// file and leave the later analysis with no input (the same lesson an earlier plan
// already taught).
//
// The sink is assigned in the SAME `else` that registers the route, so
// telSink != nil implies the route is registered.
func TestSmokeTelemetryWired(t *testing.T) {
	r := newSmokeRouter(t)
	if r.telSink == nil {
		t.Fatal("telSink nil: the sink was not created, so /api/telemetry was NOT registered")
	}
	dir := filepath.Join(r.cfg.DataDir, "telemetry")
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("telemetry directory missing: %v", err)
	}
	if !fi.IsDir() {
		t.Fatalf("%s exists but is not a directory", dir)
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(ents) != 0 {
		t.Fatalf("expected=0 files observed=%d — nothing can be written without a session", len(ents))
	}
}
