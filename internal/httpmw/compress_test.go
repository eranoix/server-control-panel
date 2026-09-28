package httpmw

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCompress_RangeRequest_NotGzipped(t *testing.T) {
	content := strings.Repeat("really-compressible-content ", 200)
	handler := Compress(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "file.txt", time.Now(), strings.NewReader(content))
	}))

	req := httptest.NewRequest(http.MethodGet, "/download", nil)
	req.Header.Set("Range", "bytes=10-19")
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206 (body=%q)", rec.Code, rec.Body.String())
	}
	if enc := rec.Header().Get("Content-Encoding"); enc != "" {
		t.Fatalf("Content-Encoding = %q, want empty — a Range response cannot be gzip-compressed", enc)
	}
	wantRange := "bytes 10-19/" + itoa(len(content))
	if got := rec.Header().Get("Content-Range"); got != wantRange {
		t.Fatalf("Content-Range = %q, want %q", got, wantRange)
	}
	if got := rec.Body.String(); got != content[10:20] {
		t.Fatalf("body = %q, want %q (10 bytes of the requested range)", got, content[10:20])
	}
}

func TestCompress_NonRangeRequest_StillGzipped(t *testing.T) {
	content := strings.Repeat("really-compressible-content ", 200)
	handler := Compress(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(content))
	}))

	req := httptest.NewRequest(http.MethodGet, "/full", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if enc := rec.Header().Get("Content-Encoding"); enc != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip (a request without Range should still be compressed)", enc)
	}
}

func TestMaxBody_DefaultLimitRejectsOver25MiB(t *testing.T) {
	body := bytes.Repeat([]byte("a"), 26<<20)
	var readErr error
	handler := MaxBody(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, readErr = io.Copy(io.Discard, r.Body)
	}))

	req := httptest.NewRequest(http.MethodPost, "/upload", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if readErr == nil {
		t.Fatal("readErr = nil, want an oversized-body error (25 MiB ceiling)")
	}
}

func TestMaxBody_RegisterLargeBody_OverridesForMatchedRoute(t *testing.T) {
	body := bytes.Repeat([]byte("b"), 30<<20)
	RegisterLargeBody(func(r *http.Request) bool {
		return r.URL.Path == "/large-route"
	}, 100<<20)

	var gotN int64
	var gotErr error
	handler := MaxBody(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotN, gotErr = io.Copy(io.Discard, r.Body)
	}))

	req := httptest.NewRequest(http.MethodPost, "/large-route", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if gotErr != nil {
		t.Fatalf("error reading a 30 MiB body on the registered route: %v", gotErr)
	}
	if gotN != int64(len(body)) {
		t.Fatalf("read %d bytes, want %d (whole body)", gotN, len(body))
	}

	var otherErr error
	otherHandler := MaxBody(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, otherErr = io.Copy(io.Discard, r.Body)
	}))
	req2 := httptest.NewRequest(http.MethodPost, "/other-route", bytes.NewReader(body))
	rec2 := httptest.NewRecorder()
	otherHandler.ServeHTTP(rec2, req2)
	if otherErr == nil {
		t.Fatal("otherErr = nil, want an oversized-body error — an unregistered route must not inherit the higher ceiling")
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
