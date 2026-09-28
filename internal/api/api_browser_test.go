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

func newBrowserTestRouter(t *testing.T) (*Router, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "panel-browser-test-")
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
	r, dataDir := newBrowserTestRouter(t)
	writeInstancesJSON(t, dataDir, "sam", `{"instances":[{"name":"pro","port":6902}]}`)

	rec := httptest.NewRecorder()
	r.browserPersistentProxy().ServeHTTP(rec, reqWithUser(http.MethodGet, "/browser-persistent/pro/", "jordan"))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("jordan reading sam's instances: status = %d, want 404", rec.Code)
	}
}

func TestBrowserPersistentProxy_NoAuthReturns404(t *testing.T) {
	r, _ := newBrowserTestRouter(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/browser-persistent/", nil)
	r.browserPersistentProxy().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
