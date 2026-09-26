package api

// uvleak.go — recovers assets that "leak" out of the tunneled browser (Ultraviolet).
//
// Pages proxied by UV sometimes request assets by a root-relative path
// (e.g. a Next.js app asking for /_next/static/...). UV is supposed to rewrite those
// URLs to /browser/uv/service/<enc>, but some escape (preloaded fonts,
// CSS url(), dynamic imports) and resolve against OUR origin → they hit
// /_next/... on the panel and take a 404 (polluting the console).
//
// Since the leaked request carries the proxied page's Referer
// (https://host/browser/uv/service/<enc-of-the-page>), we can: decode the page's
// URL (UV's xor codec), resolve the asset against its origin, re-encode
// and redirect to the correct proxied path. The browser follows the 302 and UV
// serves the asset. Generic — it catches any root-relative asset that leaked.

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

const uvServicePrefix = "/browser/uv/service/"

// uvLeakRedirect intercepts requests whose Referer is a page of the tunneled
// browser and redirects them to the proxied path. Any other request passes
// straight through (gated on the Referer — only UV-proxied pages carry it).
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
		// Do not touch anything that is already ours (panel assets, /api, /browser…) —
		// a leaked asset is always a path that does NOT start with those.
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
		// Resolve the leaked (root-relative) asset against the PAGE's origin.
		assetURL := pu.Scheme + "://" + pu.Host + r.URL.RequestURI()
		http.Redirect(w, r, uvServicePrefix+uvXorEncode(assetURL), http.StatusFound)
	})
}

// uvXorEncode/Decode mirror Ultraviolet.codec.xor: XOR 2 on odd-index chars,
// wrapped by encode/decodeURIComponent.
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
	dec, err := url.PathUnescape(s) // %XX → byte, does not convert '+' (same as decodeURIComponent)
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

// encodeURIComponentASCII replicates encodeURIComponent (does not escape
// A-Za-z0-9-_.!~*'() ). The proxied URLs are ASCII.
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
