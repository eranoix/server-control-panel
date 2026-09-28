package httpmw

import (
	"net/http"
	"strings"
)

func NoStoreHTML(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if p == "/" || p == "/index.html" {
			next.ServeHTTP(w, r)
			return
		}
		if strings.HasSuffix(p, ".html") {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}
