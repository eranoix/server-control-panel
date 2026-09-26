package webassets

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The SPA document has to be REVALIDATABLE, not forbidden from caching. Both
// properties have to hold at once: never serve a stale front-end (hence no-cache
// + the build ETag, not max-age) and not re-download ~260 KB on every reload
// when nothing changed (hence 304, not no-store). A cheap reload is what makes
// the tab usable on a bad link.
func servidorDoIndex() http.Handler {
	return IndexInjector(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "index não deveria cair no next", http.StatusNotFound)
	}))
}

func TestIndexRevalidaComETagEmVezDeProibirCache(t *testing.T) {
	rec := httptest.NewRecorder()
	servidorDoIndex().ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

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

func TestIndexDevolve304QuandoOBuildNaoMudou(t *testing.T) {
	primeira := httptest.NewRecorder()
	servidorDoIndex().ServeHTTP(primeira, httptest.NewRequest("GET", "/", nil))
	etag := primeira.Header().Get("ETag")
	if etag == "" {
		t.Fatal("the first response came with no ETag")
	}

	for _, enviado := range []string{etag, "W/" + etag, `"outro", ` + etag, "*"} {
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("If-None-Match", enviado)
		rec := httptest.NewRecorder()
		servidorDoIndex().ServeHTTP(rec, req)

		if rec.Code != http.StatusNotModified {
			t.Errorf("If-None-Match %q → %d, wanted 304", enviado, rec.Code)
		}
		if rec.Body.Len() != 0 {
			t.Errorf("If-None-Match %q returned %d bytes of body — a 304 has no body", enviado, rec.Body.Len())
		}
	}
}

func TestIndexRebaixaOCorpoQuandoOETagEDeOutroBuild(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("If-None-Match", `"build-de-ontem"`)
	rec := httptest.NewRecorder()
	servidorDoIndex().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || rec.Body.Len() == 0 {
		t.Fatalf("old ETag → %d with %d bytes; wanted 200 with the new HTML", rec.Code, rec.Body.Len())
	}
}

// Brotli: the first load is the one that hurts on a bad link, and it is
// dominated by this document. The measured gain is 22% over gzip — but it only
// counts if the compressed body really is smaller, if the client that does NOT
// ask for br still gets the original, and if the ETag distinguishes the two
// variants (otherwise a cache returns a 304 for a body the client cannot read).
// pedeComBrotli insists until the background compression is ready. It fails the
// test if it never arrives — a brotli that never warms up is an optimization
// that does not exist, and passing like that would hide it.
func pedeComBrotli(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	prazo := time.Now().Add(20 * time.Second)
	for {
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Accept-Encoding", "gzip, deflate, br, zstd")
		rec := httptest.NewRecorder()
		servidorDoIndex().ServeHTTP(rec, req)
		if rec.Header().Get("Content-Encoding") == "br" {
			return rec
		}
		if time.Now().After(prazo) {
			t.Fatal("brotli was never ready: the background compression did not warm up")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestIndexServeBrotliQuandoOClienteAceita(t *testing.T) {
	// The compression runs off the request path (otherwise the first load after
	// each deploy would pay ~2.3 s), so the test waits for the warm-up instead of
	// assuming the first response already comes compressed.
	comBr := pedeComBrotli(t)

	semBr := httptest.NewRecorder()
	servidorDoIndex().ServeHTTP(semBr, httptest.NewRequest("GET", "/", nil))

	if enc := comBr.Header().Get("Content-Encoding"); enc != "br" {
		t.Fatalf("Content-Encoding = %q, wanted \"br\"", enc)
	}
	if semBr.Header().Get("Content-Encoding") != "" {
		t.Error("a client that did not ask for br received an encoded body")
	}
	if comBr.Body.Len() >= semBr.Body.Len() {
		t.Errorf("brotli shrank nothing: %d vs %d bytes", comBr.Body.Len(), semBr.Body.Len())
	}
	if !strings.Contains(comBr.Header().Get("Vary"), "Accept-Encoding") {
		t.Error("no Vary: Accept-Encoding — an intermediate cache would serve br to someone who does not accept it")
	}
	if comBr.Header().Get("ETag") == semBr.Header().Get("ETag") {
		t.Error("the two variants share an ETag — a cross revalidation would return 304 for an unreadable body")
	}
}

func TestIndexBrotliRevalidaContraOProprioETag(t *testing.T) {
	etag := pedeComBrotli(t).Header().Get("ETag")

	segunda := httptest.NewRequest("GET", "/", nil)
	segunda.Header.Set("Accept-Encoding", "br")
	segunda.Header.Set("If-None-Match", etag)
	rec2 := httptest.NewRecorder()
	servidorDoIndex().ServeHTTP(rec2, segunda)
	if rec2.Code != http.StatusNotModified {
		t.Fatalf("br revalidation → %d, wanted 304", rec2.Code)
	}

	// And the br variant's ETag must NOT be worth a 304 to a client asking for gzip.
	cruzada := httptest.NewRequest("GET", "/", nil)
	cruzada.Header.Set("If-None-Match", etag)
	rec3 := httptest.NewRecorder()
	servidorDoIndex().ServeHTTP(rec3, cruzada)
	if rec3.Code == http.StatusNotModified {
		t.Error("the brotli ETag returned 304 for a client with no br — an unreadable body")
	}
}
