package mobilebff

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"server-control-panel/internal/auth"
)

func newAuthedRequest(method, target string, body []byte) *http.Request {
	var req *http.Request
	if body != nil {
		req = httptest.NewRequest(method, target, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, target, nil)
	}
	return req.WithContext(auth.WithUser(req.Context(), "sam"))
}

func TestFilesList_Success(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	Mount(mux, Deps{})

	req := newAuthedRequest(http.MethodGet, "/api/mobile/v1/files/list?path="+url.QueryEscape(dir), nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body FileListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, rec.Body.String())
	}
	if len(body.Entries) != 1 || body.Entries[0].Name != "a.txt" {
		t.Fatalf("entries = %+v, want [a.txt]", body.Entries)
	}
}

func TestFilesList_Unauthenticated(t *testing.T) {
	mux := http.NewServeMux()
	Mount(mux, Deps{})

	req := httptest.NewRequest(http.MethodGet, "/api/mobile/v1/files/list?path=/tmp", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (body=%s)", rec.Code, rec.Body.String())
	}
}

func TestFilesList_DeniedPath(t *testing.T) {
	mux := http.NewServeMux()
	Mount(mux, Deps{})

	req := newAuthedRequest(http.MethodGet, "/api/mobile/v1/files/list?path="+url.QueryEscape("/etc/shadow"), nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
	}
}

func TestFilesRead_Success_LanguageHint(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "main.go")
	if err := os.WriteFile(f, []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	Mount(mux, Deps{})

	req := newAuthedRequest(http.MethodGet, "/api/mobile/v1/files/read?path="+url.QueryEscape(f), nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body FileReadResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, rec.Body.String())
	}
	if body.Language != "go" {
		t.Errorf("language = %q, want go", body.Language)
	}
	if body.Content != "package main\n" {
		t.Errorf("content = %q", body.Content)
	}
	if body.Mtime <= 0 {
		t.Errorf("mtime = %d, want > 0", body.Mtime)
	}
}

func TestFilesRead_Binary_415(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "bin.dat")
	if err := os.WriteFile(f, []byte{0x01, 0x00, 0x02}, 0644); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	Mount(mux, Deps{})

	req := newAuthedRequest(http.MethodGet, "/api/mobile/v1/files/read?path="+url.QueryEscape(f), nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want 415 (body=%s)", rec.Code, rec.Body.String())
	}
}

func TestFilesRead_Missing_404(t *testing.T) {
	dir := t.TempDir()
	mux := http.NewServeMux()
	Mount(mux, Deps{})

	req := newAuthedRequest(http.MethodGet, "/api/mobile/v1/files/read?path="+url.QueryEscape(filepath.Join(dir, "nope.txt")), nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body=%s)", rec.Code, rec.Body.String())
	}
}

// TestFilesRead_Missing_404NoPathLeak proves that the 404 for a missing file
// must not carry the server's absolute path in the body — before the fix,
// mapFileErr echoed err.Error() of an *os.PathError, which embeds exactly
// that path.
func TestFilesRead_Missing_404NoPathLeak(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "nope.txt")
	mux := http.NewServeMux()
	Mount(mux, Deps{})

	req := newAuthedRequest(http.MethodGet, "/api/mobile/v1/files/read?path="+url.QueryEscape(missing), nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body=%s)", rec.Code, rec.Body.String())
	}
	if bytes.Contains(rec.Body.Bytes(), []byte(dir)) {
		t.Fatalf("404 body leaked the server path: %s", rec.Body.String())
	}
}

func TestFilesWrite_Success(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "edit.txt")
	if err := os.WriteFile(f, []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	Mount(mux, Deps{})

	reqBody, _ := json.Marshal(FileWriteRequest{Path: f, Content: "new content"})
	req := newAuthedRequest(http.MethodPost, "/api/mobile/v1/files/write", reqBody)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body FileWriteResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, rec.Body.String())
	}
	if !body.OK || body.Mtime <= 0 {
		t.Fatalf("body = %+v, want ok=true and mtime>0", body)
	}
	got, err := os.ReadFile(f)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new content" {
		t.Fatalf("file content = %q, want %q", got, "new content")
	}
}

// TestFilesWrite_StaleMtime_409WithServerContent is the central test of the
// no-silent-overwrite rule: a write with a stale expected_mtime is rejected
// with 409 and the body already carries the server's current content/mtime,
// so the app can offer reload/overwrite/cancel without a second request.
func TestFilesWrite_StaleMtime_409WithServerContent(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "edit.txt")
	if err := os.WriteFile(f, []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}
	stale := time.Now().Add(-1 * time.Hour).Truncate(time.Second)
	if err := os.Chtimes(f, stale, stale); err != nil {
		t.Fatal(err)
	}

	// A concurrent write changes the file on disk after the app "read"
	// the old mtime.
	if err := os.WriteFile(f, []byte("changed by someone else"), 0644); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	Mount(mux, Deps{})

	reqBody, _ := json.Marshal(FileWriteRequest{Path: f, Content: "my edit", ExpectedMtime: stale.Unix()})
	req := newAuthedRequest(http.MethodPost, "/api/mobile/v1/files/write", reqBody)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body=%s)", rec.Code, rec.Body.String())
	}
	var body FileConflictResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, rec.Body.String())
	}
	if body.ServerContent != "changed by someone else" {
		t.Fatalf("server_content = %q, want %q", body.ServerContent, "changed by someone else")
	}
	if body.ServerMtime <= 0 {
		t.Fatalf("server_mtime = %d, want > 0", body.ServerMtime)
	}

	// The file on disk must not have been touched by the rejected write.
	got, err := os.ReadFile(f)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "changed by someone else" {
		t.Fatalf("file content after rejected write = %q, want unchanged", got)
	}
}

func TestFilesWrite_ZeroMtime_UnconditionalSuccess(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "new.txt")

	mux := http.NewServeMux()
	Mount(mux, Deps{})

	reqBody, _ := json.Marshal(FileWriteRequest{Path: f, Content: "brand new"})
	req := newAuthedRequest(http.MethodPost, "/api/mobile/v1/files/write", reqBody)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}
