package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/config"
)

// Regression sanity check for the persistent browser proxy.
//
// Historical bug: loadBrowserInstances() read /opt/panel/data/browser-instances.json
// (the legacy path). The v1→v2 migration moved the file to <DataDir>/users/<user>/browser-instances.json
// (per-user) and the whole feature started answering 404. These tests pin the
// per-user path and the error message so that nobody regresses the read back to
// the global path.

func newBrowserTestRouter(t *testing.T) (*Router, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "vpsm-browser-test-")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return &Router{cfg: &config.Config{DataDir: dir}}, dir
}

func writeInstancesJSON(t *testing.T, dataDir, user, body string) {
	t.Helper()
	udir := filepath.Join(dataDir, "users", user)
	if err := os.MkdirAll(udir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(udir, "browser-instances.json"), []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func reqWithUser(method, target, user string) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	req = req.WithContext(auth.WithUser(context.Background(), user))
	return req
}

func TestBrowserPersistentProxy_404WhenFileMissing(t *testing.T) {
	r, _ := newBrowserTestRouter(t)
	rec := httptest.NewRecorder()
	r.browserPersistentProxy().ServeHTTP(rec, reqWithUser(http.MethodGet, "/browser-persistent/", "jordan"))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body=%q)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "instance 'default' is not configured") {
		t.Fatalf("body = %q, want a message with 'default is not configured'", rec.Body.String())
	}
}

func TestBrowserPersistentProxy_404WhenInstanceUnknown(t *testing.T) {
	r, dataDir := newBrowserTestRouter(t)
	writeInstancesJSON(t, dataDir, "jordan", `{"instances":[{"name":"pro","port":1}]}`)
	rec := httptest.NewRecorder()
	r.browserPersistentProxy().ServeHTTP(rec, reqWithUser(http.MethodGet, "/browser-persistent/", "jordan"))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body=%q)", rec.Code, rec.Body.String())
	}
}

func TestBrowserPersistentProxy_RoutesToInstance(t *testing.T) {
	// Upstream fake — registra o path recebido.
	var gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		gotPath = req.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("upstream-ok"))
	}))
	t.Cleanup(upstream.Close)
	u, _ := url.Parse(upstream.URL)
	port := u.Port()

	r, dataDir := newBrowserTestRouter(t)
	writeInstancesJSON(t, dataDir, "jordan",
		`{"instances":[{"name":"pro","port":`+port+`}]}`)

	rec := httptest.NewRecorder()
	r.browserPersistentProxy().ServeHTTP(rec, reqWithUser(http.MethodGet, "/browser-persistent/pro/vnc.html", "jordan"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%q)", rec.Code, rec.Body.String())
	}
	if gotPath != "/vnc.html" {
		t.Fatalf("upstream got path %q, want %q", gotPath, "/vnc.html")
	}
}

func TestBrowserPersistentProxy_PerUserIsolation(t *testing.T) {
	// sam has a "pro" instance but jordan does not → jordan's request must give
	// a 404 even if sam has a working config.
	r, dataDir := newBrowserTestRouter(t)
	writeInstancesJSON(t, dataDir, "sam", `{"instances":[{"name":"pro","port":6902}]}`)

	rec := httptest.NewRecorder()
	r.browserPersistentProxy().ServeHTTP(rec, reqWithUser(http.MethodGet, "/browser-persistent/pro/", "jordan"))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("jordan reading sam's instances: status = %d, want 404", rec.Code)
	}
}

func TestBrowserPersistentProxy_NoAuthReturns404(t *testing.T) {
	// With no user in the context (auth.Middleware missing or disabled) there is
	// no way to resolve an instance. It must land on 404, never reach /opt/panel/data/.
	r, _ := newBrowserTestRouter(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/browser-persistent/", nil)
	r.browserPersistentProxy().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
