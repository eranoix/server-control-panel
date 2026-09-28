package api

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"server-control-panel/internal/config"
)

func newSmokeRouter(t *testing.T) *Router {
	t.Helper()
	dir, err := os.MkdirTemp("", "panel-smoke-")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

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
	if err := os.MkdirAll(filepath.Join(dir, "users", "sam"), 0o755); err != nil {
		t.Fatalf("mkdir users: %v", err)
	}

	r, err := NewRouter(cfg)
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	t.Cleanup(func() {
		defer func() { _ = recover() }()
		r.Shutdown(nil)
	})
	return r
}

func TestSmokePublicRoutes(t *testing.T) {
	r := newSmokeRouter(t)

	cases := []struct {
		name       string
		method     string
		path       string
		body       string
		wantStatus []int
	}{
		{"health", "GET", "/api/health", "", []int{200}},
		{"health-detailed", "GET", "/api/health/detailed", "", []int{200}},
		{"metrics", "GET", "/metrics", "", []int{200}},
		{"manifest", "GET", "/manifest.webmanifest", "", []int{200}},
		{"sw.js", "GET", "/sw.js", "", []int{200}},
		{"icon-192", "GET", "/icon-192.png", "", []int{200, 404}},
		{"recovery page", "GET", "/recovery", "", []int{200}},
		{"forward-auth no token", "GET", "/api/forward-auth", "", []int{200}},
		{"index html", "GET", "/", "", []int{200}},
		{"login bad json", "POST", "/api/auth/login", "not json", []int{400}},
		{"login bad creds", "POST", "/api/auth/login", `{"username":"nope","password":"nope"}`, []int{401, 400, 423}},
		{"login method get", "GET", "/api/auth/login", "", []int{405}},
		{"refresh-cookie no cookie", "POST", "/api/auth/refresh-cookie", "", []int{401}},
		{"refresh-cookie method get", "GET", "/api/auth/refresh-cookie", "", []int{405}},
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
	if body := w.Body.String(); !strings.Contains(body, "no refresh session") {
		t.Fatalf("expected handler body ('no refresh session'), got: %s", body)
	}
}

func TestSmokeDocsGated(t *testing.T) {
	r := newSmokeRouter(t)

	req := httptest.NewRequest("GET", "/_docs", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	switch w.Code {
	case 401, 403:
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

func TestSmokeGraphGated(t *testing.T) {
	r := newSmokeRouter(t)

	req := httptest.NewRequest("GET", "/_graph?project=server-control-panel", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	switch w.Code {
	case 401, 403:
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

func TestSmokeCodeGated(t *testing.T) {
	r := newSmokeRouter(t)

	req := httptest.NewRequest("GET", "/_code/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	switch w.Code {
	case 401, 403:
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
		"/api/panel/health",
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
	if _, ok := resp["subsystems"]; !ok {
		t.Fatalf("missing subsystems field; got %v", resp)
	}
}

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
			t.Errorf("missing security header %q (is the securityHeaders middleware broken?)", h)
		}
	}
}

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
