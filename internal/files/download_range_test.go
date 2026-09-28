package files

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func requestDownload(t *testing.T, path string, header map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/download?path="+url.QueryEscape(path), nil)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, req)
	return rec
}

func testFile(t *testing.T) (string, []byte) {
	t.Helper()
	content := bytes.Repeat([]byte("0123456789"), 100)
	path := filepath.Join(t.TempDir(), "large.bin")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	return path, content
}

func TestDownload_Range_206(t *testing.T) {
	path, content := testFile(t)
	rec := requestDownload(t, path, map[string]string{"Range": "bytes=100-199"})

	if rec.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206 — the handler ignored the Range; body=%s", rec.Code, rec.Body.String())
	}
	want := fmt.Sprintf("bytes 100-199/%d", len(content))
	if got := rec.Header().Get("Content-Range"); got != want {
		t.Fatalf("Content-Range = %q, want %q", got, want)
	}
	if got := rec.Header().Get("Accept-Ranges"); got != "bytes" {
		t.Fatalf("Accept-Ranges = %q, want bytes", got)
	}
	if !bytes.Equal(rec.Body.Bytes(), content[100:200]) {
		t.Fatalf("a body of %d bytes is not the requested chunk", rec.Body.Len())
	}
}

func TestDownload_ResumeFromMiddle(t *testing.T) {
	path, content := testFile(t)
	rec := requestDownload(t, path, map[string]string{"Range": "bytes=600-"})
	if rec.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206", rec.Code)
	}
	if !bytes.Equal(rec.Body.Bytes(), content[600:]) {
		t.Fatalf("body = %d bytes; expected the 400-byte tail", rec.Body.Len())
	}
}

func TestDownload_NoRange_200Full(t *testing.T) {
	path, content := testFile(t)
	rec := requestDownload(t, path, nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !bytes.Equal(rec.Body.Bytes(), content) {
		t.Fatalf("body = %d bytes, want %d", rec.Body.Len(), len(content))
	}
	if got := rec.Header().Get("Content-Length"); got != fmt.Sprint(len(content)) {
		t.Fatalf("Content-Length = %q, want %d", got, len(content))
	}
	if got := rec.Header().Get("Content-Type"); got != "application/octet-stream" {
		t.Fatalf("Content-Type = %q — it has to force a download, not let the browser guess", got)
	}
	if got := rec.Header().Get("Content-Disposition"); got != `attachment; filename="large.bin"` {
		t.Fatalf("Content-Disposition = %q", got)
	}
}

func TestDownload_UnsatisfiableRange_416(t *testing.T) {
	path, _ := testFile(t)
	rec := requestDownload(t, path, map[string]string{"Range": "bytes=99999-199999"})
	if rec.Code != http.StatusRequestedRangeNotSatisfiable {
		t.Fatalf("status = %d, want 416", rec.Code)
	}
}

func TestDownload_ErrorsPreserved(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]struct {
		path   string
		status int
	}{
		"relative":  {"not/absolute", http.StatusBadRequest},
		"denylist":  {"/etc/shadow", http.StatusBadRequest},
		"directory": {dir, http.StatusBadRequest},
		"missing":   {filepath.Join(dir, "missing.bin"), http.StatusNotFound},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if rec := requestDownload(t, c.path, nil); rec.Code != c.status {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, c.status, rec.Body.String())
			}
		})
	}
}
