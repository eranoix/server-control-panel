package api

// handlers_fdroid_test.go — one load-bearing constraint is what this
// suite exists to prove: the F-Droid client never follows an HTTP 3xx, so
// every path it walks under /fdroid/repo/ must answer directly (200/400/404),
// never a redirect — not even the implicit trailing-slash/dot-segment
// redirect Go's own http.ServeMux issues for "unclean" paths, which is
// exactly the kind of redirect http.FileServer/http.ServeMux would produce
// if used here instead of http.ServeContent + a dedicated path check.
//
// fdroidserver is not installed on this host, so the fixture repo tree
// below is built by hand (real F-Droid repo layout, real index/APK byte
// shapes) rather than by running `fdroid update`.

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// fdroidFixtureRouter builds a smoke router and populates its
// data/fdroid/repo directory with a synthetic repo tree matching the real
// F-Droid v1+v2 layout: index-v1.jar (legacy signed index), index-v2.json +
// entry.json (current index format), an icons/ subdirectory, and a fixture
// APK.
func fdroidFixtureRouter(t *testing.T) (*Router, []byte) {
	t.Helper()
	r := newSmokeRouter(t)
	repoDir := filepath.Join(r.cfg.DataDir, "fdroid", "repo")
	if err := os.MkdirAll(filepath.Join(repoDir, "icons"), 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}

	write := func(rel string, data []byte) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(repoDir, rel), data, 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	write("index-v1.jar", append([]byte("PK\x03\x04"), bytes.Repeat([]byte{0x11}, 512)...)) // JAR = ZIP container, PK magic
	write("index-v2.json", []byte(`{"repo":{"name":"vps-manager","timestamp":1234567890},"packages":{}}`))
	write("entry.json", []byte(`{"timestamp":1234567890,"version":20002,"index":{"name":"/index-v2.json"}}`))
	write("icons/br.tech.vpsmanager.app.1.png", bytes.Repeat([]byte{0x89, 0x50, 0x4E, 0x47}, 16)) // PNG-ish filler

	apkBytes := append([]byte("PK\x03\x04"), bytes.Repeat([]byte{0xAB}, 8192)...) // real APKs are ZIPs too
	write("app-release.apk", apkBytes)

	// A file OUTSIDE the served repo root, at the same DataDir level as
	// "fdroid/", playing the part of a real secret a traversal attempt
	// must never reach.
	if err := os.WriteFile(filepath.Join(r.cfg.DataDir, "secrets.vault"), []byte("SECRET-DO-NOT-SERVE"), 0o600); err != nil {
		t.Fatalf("write secrets.vault: %v", err)
	}

	return r, apkBytes
}

func assertNoRedirect(t *testing.T, w *httptest.ResponseRecorder, path string) {
	t.Helper()
	if w.Code >= 300 && w.Code < 400 {
		t.Fatalf("GET %s answered with a redirect (status %d, Location=%q) — the F-Droid client does not follow 3xx (Pitfall 14)", path, w.Code, w.Header().Get("Location"))
	}
	if loc := w.Header().Get("Location"); loc != "" {
		t.Fatalf("GET %s has a Location header (%q) even though status is %d — must never be present on this route", path, loc, w.Code)
	}
}

// TestFdroidRepoIndexV2 — Test 1: index-v2.json served with exact bytes and
// Content-Type application/json, never a redirect.
func TestFdroidRepoIndexV2(t *testing.T) {
	r, _ := fdroidFixtureRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/fdroid/repo/index-v2.json", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assertNoRedirect(t, w, req.URL.Path)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	want := `{"repo":{"name":"vps-manager","timestamp":1234567890},"packages":{}}`
	if w.Body.String() != want {
		t.Fatalf("body mismatch:\n got=%s\nwant=%s", w.Body.String(), want)
	}
}

// TestFdroidRepoAPK — Test 2: a fixture APK is served byte-identical with
// the Android package-archive Content-Type.
func TestFdroidRepoAPK(t *testing.T) {
	r, apkBytes := fdroidFixtureRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/fdroid/repo/app-release.apk", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assertNoRedirect(t, w, req.URL.Path)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body len=%d)", w.Code, w.Body.Len())
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/vnd.android.package-archive" {
		t.Fatalf("Content-Type = %q, want application/vnd.android.package-archive", ct)
	}
	if !bytes.Equal(w.Body.Bytes(), apkBytes) {
		t.Fatalf("APK body not byte-identical: got %d bytes, want %d bytes", w.Body.Len(), len(apkBytes))
	}
}

// TestFdroidRepoPathTraversal — Test 3: a traversal attempt must never
// escape the repo root, and must never be answered with a redirect either
// (Go's own http.ServeMux would 301 a request containing ".." straight to
// its cleaned form if this route were registered directly on r.mux —
// exactly the kind of redirect this whole route exists to avoid).
func TestFdroidRepoPathTraversal(t *testing.T) {
	r, _ := fdroidFixtureRouter(t)

	for _, target := range []string{
		"/fdroid/repo/../../secrets.vault",
		"/fdroid/repo/..%2f..%2fsecrets.vault",
		"/fdroid/repo/icons/../../../secrets.vault",
	} {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assertNoRedirect(t, w, target)
		if w.Code != http.StatusBadRequest && w.Code != http.StatusNotFound {
			t.Fatalf("GET %s: status = %d, want 400 or 404", target, w.Code)
		}
		if bytes.Contains(w.Body.Bytes(), []byte("SECRET-DO-NOT-SERVE")) {
			t.Fatalf("GET %s leaked the secret file's contents!", target)
		}
	}
}

// TestFdroidRepoBareDirectory — Test 4: a bare directory request (repo
// root, or a subdirectory like icons/) must answer 404 — no listing, and
// critically no redirect to a normalized path (the exact behavior
// http.FileServer would produce and the no-redirect rule forbids).
func TestFdroidRepoBareDirectory(t *testing.T) {
	r, _ := fdroidFixtureRouter(t)

	for _, target := range []string{
		"/fdroid/repo/",
		"/fdroid/repo",
		"/fdroid/repo/icons/",
		"/fdroid/repo/icons",
	} {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assertNoRedirect(t, w, target)
		if w.Code != http.StatusNotFound {
			t.Fatalf("GET %s: status = %d, want 404 (body=%s)", target, w.Code, w.Body.String())
		}
	}
}

// TestFdroidRepoNeverRedirectsAcrossClientWalkedPaths covers the full set
// of paths a real F-Droid client walks during install/update (legacy jar
// index, v2 index + entry, icons, the APK itself) plus a battery of path
// variants that would normally trigger Go's automatic path-canonicalization
// redirect (doubled slashes, a trailing dot segment) — none of them may
// ever answer with a 3xx, on this route.
func TestFdroidRepoNeverRedirectsAcrossClientWalkedPaths(t *testing.T) {
	r, _ := fdroidFixtureRouter(t)

	paths := []string{
		"/fdroid/repo/index-v1.jar",
		"/fdroid/repo/index-v2.json",
		"/fdroid/repo/entry.json",
		"/fdroid/repo/icons/br.tech.vpsmanager.app.1.png",
		"/fdroid/repo/app-release.apk",
		// Canonicalization-triggering variants — a bare http.FileServer or
		// http.ServeMux registration would 301 every one of these.
		"/fdroid/repo//index-v2.json",
		"/fdroid/repo/./index-v2.json",
		"/fdroid/repo/icons/../index-v2.json",
	}
	for _, p := range paths {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assertNoRedirect(t, w, p)
		if w.Code >= 500 {
			t.Fatalf("GET %s: unexpected server error %d (body=%s)", p, w.Code, w.Body.String())
		}
	}
}

// TestFileServerWouldRedirectButOursDoesNot demonstrates, with a real
// request against a real http.FileServer over the exact same fixture
// directory, the redirect Pitfall 14 forbids — proving the contrast is not
// hypothetical. This does NOT exercise production wiring (handleFdroidRepo
// is never swapped for FileServer there); it isolates the stdlib behavior
// this route is built to avoid.
func TestFileServerWouldRedirectButOursDoesNot(t *testing.T) {
	r, _ := fdroidFixtureRouter(t)
	repoDir := filepath.Join(r.cfg.DataDir, "fdroid", "repo")

	fsHandler := http.StripPrefix("/fdroid/repo/", http.FileServer(http.Dir(repoDir)))

	// A subdirectory request missing its trailing slash ("icons" instead of
	// "icons/") is the textbook case: http.FileServer resolves it to a real
	// directory and 301-redirects to add the "/", per net/http's own
	// serveFile→localRedirect logic.
	req := httptest.NewRequest(http.MethodGet, "/fdroid/repo/icons", nil)
	w := httptest.NewRecorder()
	fsHandler.ServeHTTP(w, req)
	if w.Code < 300 || w.Code >= 400 {
		t.Fatalf("expected http.FileServer to 3xx-redirect a subdirectory request without a trailing slash, got %d — if this assertion ever fails, re-verify the comparison still demonstrates the gotcha", w.Code)
	}
	if loc := w.Header().Get("Location"); loc == "" {
		t.Fatalf("expected http.FileServer's redirect to carry a Location header")
	}

	// Our own route, same fixture, same bare-subdirectory request: no redirect.
	req2 := httptest.NewRequest(http.MethodGet, "/fdroid/repo/icons", nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	assertNoRedirect(t, w2, req2.URL.Path)
	if w2.Code != http.StatusNotFound {
		t.Fatalf("our route: status = %d, want 404 for the same subdirectory request", w2.Code)
	}
}

// TestFdroidRepoMethodNotAllowed — only GET/HEAD are meaningful for a
// static repo; anything else is rejected without touching the filesystem.
func TestFdroidRepoMethodNotAllowed(t *testing.T) {
	r, _ := fdroidFixtureRouter(t)

	req := httptest.NewRequest(http.MethodPost, "/fdroid/repo/index-v2.json", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assertNoRedirect(t, w, req.URL.Path)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", w.Code)
	}
}

// TestFdroidRepoUnauthenticated — no session cookie, no Authorization
// header: the F-Droid client presents neither, so this route must never
// answer 401/403.
func TestFdroidRepoUnauthenticated(t *testing.T) {
	r, _ := fdroidFixtureRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/fdroid/repo/index-v2.json", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code == http.StatusUnauthorized || w.Code == http.StatusForbidden {
		t.Fatalf("fdroid repo route required authentication (status %d) — the F-Droid client has no session cookie to present", w.Code)
	}
}
