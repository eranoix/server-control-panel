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

// TestCompress_RangeRequest_NotGzipped proves, with a real HTTP request (not by
// reading the code), that a response to a Range request for a compressible
// Content-Type (text/plain) is NOT wrapped in gzip by the middleware — even when
// the client sends Accept-Encoding: gzip.
//
// Why it matters: http.ServeContent computes Content-Range from the byte
// positions of the ORIGINAL file. If this middleware compressed the response
// body, the Content-Range would go on describing the uncompressed file while the
// actual body would be gzip bytes — the client (a resumable download in the
// Android app) would receive data inconsistent with the header that says where
// it belongs in the final file.
func TestCompress_RangeRequest_NotGzipped(t *testing.T) {
	content := strings.Repeat("conteudo-compressivel-de-verdade ", 200) // > minGzipBytes
	handler := Compress(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "arquivo.txt", time.Now(), strings.NewReader(content))
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

// TestCompress_NonRangeRequest_StillGzipped makes sure the bypass above is
// Range-specific — an ordinary compressible response goes on being
// gzip-compressed as always.
func TestCompress_NonRangeRequest_StillGzipped(t *testing.T) {
	content := strings.Repeat("conteudo-compressivel-de-verdade ", 200)
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

// TestMaxBody_DefaultLimitRejectsOver25MiB proves the default ceiling with a real
// body larger than 25 MiB: the handler never gets to read it all, the Read returns
// MaxBytesReader's error first.
func TestMaxBody_DefaultLimitRejectsOver25MiB(t *testing.T) {
	body := bytes.Repeat([]byte("a"), 26<<20) // 26 MiB > the 25 MiB ceiling
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

// TestMaxBody_RegisterLargeBody_OverridesForMatchedRoute proves, with a real
// 30 MiB body (larger than the default ceiling), that a route registered through
// RegisterLargeBody can read the whole body — and that ANY other route (not
// registered) stays pinned to the 25 MiB ceiling, i.e. the exception is a one-off
// and does not leak into the rest of the server.
func TestMaxBody_RegisterLargeBody_OverridesForMatchedRoute(t *testing.T) {
	body := bytes.Repeat([]byte("b"), 30<<20) // 30 MiB
	RegisterLargeBody(func(r *http.Request) bool {
		return r.URL.Path == "/rota-grande"
	}, 100<<20)

	var gotN int64
	var gotErr error
	handler := MaxBody(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotN, gotErr = io.Copy(io.Discard, r.Body)
	}))

	req := httptest.NewRequest(http.MethodPost, "/rota-grande", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if gotErr != nil {
		t.Fatalf("error reading a 30 MiB body on the registered route: %v", gotErr)
	}
	if gotN != int64(len(body)) {
		t.Fatalf("read %d bytes, want %d (whole body)", gotN, len(body))
	}

	// Another route, not registered, stays at the 25 MiB ceiling — the exception
	// did not leak outside the match.
	var otherErr error
	otherHandler := MaxBody(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, otherErr = io.Copy(io.Discard, r.Body)
	}))
	req2 := httptest.NewRequest(http.MethodPost, "/outra-rota", bytes.NewReader(body))
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
