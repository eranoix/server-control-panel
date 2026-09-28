package webassets

import (
	"net/http"
	"strings"
	"sync"
)

var (
	cachedIndexHTML []byte
	cachedIndexOnce sync.Once
)

func WarmPrecompression() {
	buildIndex()
	if cachedIndexHTML != nil {
		brotliOnce("index:"+BuildStamp, cachedIndexHTML)
	}
}

func buildIndex() {
	cachedIndexOnce.Do(func() {
		data, err := FS.ReadFile("web/index.html")
		if err != nil {
			return
		}
		cachedIndexHTML = []byte(strings.ReplaceAll(string(data), "__PANEL_BUILD__", BuildStamp))
	})
}

func IndexInjector(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if p != "/" && p != "/index.html" {
			next.ServeHTTP(w, r)
			return
		}
		buildIndex()
		if cachedIndexHTML == nil {
			next.ServeHTTP(w, r)
			return
		}
		etag := `"` + BuildStamp + `"`
		if acceptsBrotli(r) {
			etag = `"` + BuildStamp + `-br"`
		}
		w.Header().Set("ETag", etag)
		w.Header().Set("Cache-Control", "no-cache")
		if match := r.Header.Get("If-None-Match"); match != "" && etagMatches(match, etag) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if serveBrotli(w, r, "index:"+BuildStamp, cachedIndexHTML) {
			return
		}
		_, _ = w.Write(cachedIndexHTML)
	})
}

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
