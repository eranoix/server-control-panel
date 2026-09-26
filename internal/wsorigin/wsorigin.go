// Package wsorigin centralizes the Origin-checking policy for WebSocket
// upgrades. Used by the handlers in api/, pty/, videocall/ and whatsapp/.
//
// Without it, gorilla/websocket accepts an upgrade from any origin — a malicious
// site could drive a WS to this instance using the logged-in user's
// cookie (cookies travel on the upgrade GET; SameSite=Lax mitigates but does not cover
// every browser/scenario).
package wsorigin

import (
	"net/http"
	"net/url"
	"strings"
)

// CheckSameHost validates that Origin (when present) matches Host. Non-browser
// clients (curl, native apps) do not send Origin — those are allowed.
func CheckSameHost(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return true
	}
	u, err := url.Parse(o)
	if err != nil {
		return false
	}
	if u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

// SecHeaders returns the security headers that need to travel in the
// WebSocket 101 handshake. The SecurityHeaders middleware sets
// X-Content-Type-Options on normal HTTP responses, but gorilla/websocket
// builds the 101 by hand and ignores w.Header() — so nosniff disappears on a
// successful upgrade (DevTools flags "missing x-content-type-options"). Pass
// this as responseHeader: Upgrade(w, r, wsorigin.SecHeaders()).
func SecHeaders() http.Header {
	return http.Header{"X-Content-Type-Options": []string{"nosniff"}}
}
