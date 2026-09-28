package api

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

const uvServicePrefix = "/browser/uv/service/"

func uvLeakRedirect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			next.ServeHTTP(w, r)
			return
		}
		ref := r.Header.Get("Referer")
		idx := strings.Index(ref, uvServicePrefix)
		if idx < 0 {
			next.ServeHTTP(w, r)
			return
		}
		p := r.URL.Path
		if strings.HasPrefix(p, "/browser") || strings.HasPrefix(p, "/api/") ||
			strings.HasPrefix(p, "/ws/") || strings.HasPrefix(p, "/vendor/") ||
			strings.HasPrefix(p, "/recovery") || p == "/" || p == "/index.html" {
			next.ServeHTTP(w, r)
			return
		}
		blob := ref[idx+len(uvServicePrefix):]
		if i := strings.IndexAny(blob, "?#"); i >= 0 {
			blob = blob[:i]
		}
		pageURL := uvXorDecode(blob)
		pu, err := url.Parse(pageURL)
		if err != nil || pu.Scheme == "" || pu.Host == "" {
			next.ServeHTTP(w, r)
			return
		}
		assetURL := pu.Scheme + "://" + pu.Host + r.URL.RequestURI()
		http.Redirect(w, r, uvServicePrefix+uvXorEncode(assetURL), http.StatusFound)
	})
}

func uvXorEncode(s string) string {
	b := []byte(s)
	for i := range b {
		if i%2 == 1 {
			b[i] ^= 2
		}
	}
	return encodeURIComponentASCII(string(b))
}

func uvXorDecode(s string) string {
	dec, err := url.PathUnescape(s)
	if err != nil {
		dec = s
	}
	b := []byte(dec)
	for i := range b {
		if i%2 == 1 {
			b[i] ^= 2
		}
	}
	return string(b)
}

func encodeURIComponentASCII(s string) string {
	const safe = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_.!~*'()"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if strings.IndexByte(safe, c) >= 0 {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}
