package webassets

import (
	"net/http"
	"strings"
	"sync"
)

// IndexInjector intercepts "/" and "/index.html" to replace the __VPSM_BUILD__
// placeholder with the current BuildStamp. Without it the front-end would have
// to guess when the localStorage schema changed.
//
// Performance: the result is cached in memory — BuildStamp is constant for a
// run, so there is no reason to rescan ~1MB of HTML on every GET /. The cache
// is lazy on the first call (which avoids the cost at boot when /api/health is
// called first).
var (
	cachedIndexHTML []byte
	cachedIndexOnce sync.Once
)

// AquecePreCompressao assembles the index and kicks off the brotli compression
// BEFORE there is a user waiting on it. Without this the warm-up only starts on
// the first request after the deploy — and since brotliOnce does not block, that
// first visitor gets gzip. Called from boot, in a goroutine: by the time the
// browser arrives (seconds after a deploy), the 196 KB are already ready.
func AquecePreCompressao() {
	montaIndex()
	if cachedIndexHTML != nil {
		brotliOnce("index:"+BuildStamp, cachedIndexHTML)
	}
}

// montaIndex performs the BuildStamp substitution exactly once per run.
func montaIndex() {
	cachedIndexOnce.Do(func() {
		data, err := FS.ReadFile("web/index.html")
		if err != nil {
			return
		}
		cachedIndexHTML = []byte(strings.ReplaceAll(string(data), "__VPSM_BUILD__", BuildStamp))
	})
}

func IndexInjector(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if p != "/" && p != "/index.html" {
			next.ServeHTTP(w, r)
			return
		}
		montaIndex()
		if cachedIndexHTML == nil {
			next.ServeHTTP(w, r)
			return
		}
		// The document is revalidatable, not no-store. The content of the index only
		// changes when the build changes (the __VPSM_BUILD__ injected above is itself
		// part of the body), so ETag = BuildStamp describes the resource exactly.
		// `no-cache` keeps the old guarantee — the browser ALWAYS asks the server
		// before using its copy, it never serves a stale front-end — but when nothing
		// changed the answer is an empty 304 instead of ~260 KB gzipped. On a bad link
		// that is the difference between an 8s reload and a 300ms one. Swapping it for
		// no-store here reintroduces the full download.
		// The ETag distinguishes the encoding: a cache (or the browser itself) holding
		// the brotli variant must not revalidate against the gzip one and receive a 304
		// for a body it cannot read.
		etag := `"` + BuildStamp + `"`
		if aceitaBrotli(r) {
			etag = `"` + BuildStamp + `-br"`
		}
		w.Header().Set("ETag", etag)
		w.Header().Set("Cache-Control", "no-cache")
		if match := r.Header.Get("If-None-Match"); match != "" && etagMatches(match, etag) {
			// No Content-Type: the gzip middleware decides to compress from the CT and a
			// 304 has no body to compress (same pattern as vendorCached).
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		// Brotli at the maximum level, compressed once per build: 252 KB → 196 KB on
		// the first load. If the client does not accept it, the middleware's gzip goes
		// on handling the response as before.
		if serveBrotli(w, r, "index:"+BuildStamp, cachedIndexHTML) {
			return
		}
		_, _ = w.Write(cachedIndexHTML)
	})
}

// etagMatches implements RFC 9110's If-None-Match comparison: the header may
// carry "*", a comma-separated list, and weak entries (W/"x"). Weak comparison
// is the correct one for a conditional GET — without it a browser sending
// W/"123" would download the whole HTML again for nothing.
func etagMatches(header, etag string) bool {
	header = strings.TrimSpace(header)
	if header == "*" {
		return true
	}
	for _, cand := range strings.Split(header, ",") {
		cand = strings.TrimSpace(cand)
		cand = strings.TrimPrefix(cand, "W/")
		if cand == etag {
			return true
		}
	}
	return false
}

// HandleJoinPage serves /join — the public page where a guest joins with a PIN.
// The page is static (no Alpine), POSTs straight to /api/videocall/join-by-pin
// and redirects to the panel in guest mode.
func HandleJoinPage(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	data, err := FS.ReadFile("web/join.html")
	if err != nil {
		http.Error(w, "join page missing", http.StatusInternalServerError)
		return
	}
	_, _ = w.Write(data)
}
