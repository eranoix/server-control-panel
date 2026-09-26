package httpmw

import (
	"net/http"
	"strings"
)

// NoStoreHTML keeps the SPA document out of cache. After every deploy, each load
// fetches fresh HTML — killing the whole "stale frontend" class of bug.
// Other assets (tailwind.css and friends) keep the default cache.
func NoStoreHTML(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		// "/" and "/index.html" are out of here. They are served by the
		// IndexInjector, which knows the BuildStamp and can do better than
		// forbidding cache: ETag + no-cache, which always revalidates (the same
		// guarantee of never serving a stale front end) but returns an empty 304
		// when nothing changed. Other .html pages have no stamp and stay no-store.
		if p == "/" || p == "/index.html" {
			next.ServeHTTP(w, r)
			return
		}
		if strings.HasSuffix(p, ".html") {
			// no-store alone already prevents any caching. must-revalidate is a
			// no-op alongside no-store; Pragma/Expires are deprecated HTTP/1.0
			// legacy (DevTools flags both). Cache-Control is enough.
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}
