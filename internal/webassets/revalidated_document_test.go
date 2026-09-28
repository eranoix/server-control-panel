package webassets

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func indexServer() http.Handler {
	return IndexInjector(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "index should not fall through to next", http.StatusNotFound)
	}))
}

func TestIndexRevalidatesWithETagInsteadOfNoCache(t *testing.T) {
	rec := httptest.NewRecorder()
	indexServer().ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("GET / = %d, wanted 200", rec.Code)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control = %q, wanted \"no-cache\" (no-store downgrades the whole HTML on every reload)", cc)
	}
	if rec.Header().Get("ETag") == "" {
		t.Error("no ETag: the browser has no way to ask \"did it change?\" and always re-downloads the body")
	}
	if rec.Body.Len() == 0 {
		t.Error("empty body on the 200")
	}
}

func TestIndexReturns304WhenBuildUnchanged(t *testing.T) {
	first := httptest.NewRecorder()
	indexServer().ServeHTTP(first, httptest.NewRequest("GET", "/", nil))
	etag := first.Header().Get("ETag")
	if etag == "" {
		t.Fatal("the first response came with no ETag")
	}

	for _, sent := range []string{etag, "W/" + etag, `"other", ` + etag, "*"} {
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("If-None-Match", sent)
		rec := httptest.NewRecorder()
		indexServer().ServeHTTP(rec, req)

		if rec.Code != http.StatusNotModified {
			t.Errorf("If-None-Match %q → %d, wanted 304", sent, rec.Code)
		}
		if rec.Body.Len() != 0 {
			t.Errorf("If-None-Match %q returned %d bytes of body — a 304 has no body", sent, rec.Body.Len())
		}
	}
}

func TestIndexResendsBodyWhenETagIsFromAnotherBuild(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("If-None-Match", `"yesterdays-build"`)
	rec := httptest.NewRecorder()
	indexServer().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || rec.Body.Len() == 0 {
		t.Fatalf("old ETag → %d with %d bytes; wanted 200 with the new HTML", rec.Code, rec.Body.Len())
	}
}

func requestWithBrotli(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Accept-Encoding", "gzip, deflate, br, zstd")
		rec := httptest.NewRecorder()
		indexServer().ServeHTTP(rec, req)
		if rec.Header().Get("Content-Encoding") == "br" {
			return rec
		}
		if time.Now().After(deadline) {
			t.Fatal("brotli was never ready: the background compression did not warm up")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestIndexServesBrotliWhenClientAccepts(t *testing.T) {
	withBr := requestWithBrotli(t)

	noBr := httptest.NewRecorder()
	indexServer().ServeHTTP(noBr, httptest.NewRequest("GET", "/", nil))

	if enc := withBr.Header().Get("Content-Encoding"); enc != "br" {
		t.Fatalf("Content-Encoding = %q, wanted \"br\"", enc)
	}
	if noBr.Header().Get("Content-Encoding") != "" {
		t.Error("a client that did not ask for br received an encoded body")
	}
	if withBr.Body.Len() >= noBr.Body.Len() {
		t.Errorf("brotli shrank nothing: %d vs %d bytes", withBr.Body.Len(), noBr.Body.Len())
	}
	if !strings.Contains(withBr.Header().Get("Vary"), "Accept-Encoding") {
		t.Error("no Vary: Accept-Encoding — an intermediate cache would serve br to someone who does not accept it")
	}
	if withBr.Header().Get("ETag") == noBr.Header().Get("ETag") {
		t.Error("the two variants share an ETag — a cross revalidation would return 304 for an unreadable body")
	}
}

func TestIndexBrotliRevalidatesAgainstItsOwnETag(t *testing.T) {
	etag := requestWithBrotli(t).Header().Get("ETag")

	second := httptest.NewRequest("GET", "/", nil)
	second.Header.Set("Accept-Encoding", "br")
	second.Header.Set("If-None-Match", etag)
	rec2 := httptest.NewRecorder()
	indexServer().ServeHTTP(rec2, second)
	if rec2.Code != http.StatusNotModified {
		t.Fatalf("br revalidation → %d, wanted 304", rec2.Code)
	}

	crossed := httptest.NewRequest("GET", "/", nil)
	crossed.Header.Set("If-None-Match", etag)
	rec3 := httptest.NewRecorder()
	indexServer().ServeHTTP(rec3, crossed)
	if rec3.Code == http.StatusNotModified {
		t.Error("the brotli ETag returned 304 for a client with no br — an unreadable body")
	}
}
