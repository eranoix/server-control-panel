// Package httpmw — the control plane's generic HTTP middlewares. Each file
// is ONE middleware (or one closely related family). It imports leaf packages
// only; never a domain package.
package httpmw

import "net/http"

// SecurityHeaders sets CSP/HSTS/X-Frame/Referrer/Permissions-Policy.
// The CSP allows:
//   - script-src 'unsafe-inline'/'unsafe-eval' (Alpine bindings, Monaco worker)
//   - style-src 'unsafe-inline' (Alpine x-bind)
//   - img-src the WhatsApp CDN + Atlassian (avatars), gravatar, GitHub (real
//     avatars on the Git / GitGraph page)
//   - connect-src ws/wss/http/https (the tunneled browser does arbitrary fetches)
//
// HSTS only when TLS is active (the browser ignores it over HTTP, but emitting
// it would be semantically wrong).
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		// X-Frame-Options is omitted on purpose: it is legacy and DevTools flags it
		// ("use CSP frame-ancestors"). The CSP below already carries `frame-ancestors
		// 'self'`, the modern, equivalent mechanism (it blocks third-party embedding
		// and allows same-origin). Do not reintroduce the header.
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Permissions-Policy",
			"camera=(self), microphone=(self), geolocation=(self), "+
				"payment=(), usb=(), accelerometer=(), gyroscope=(), "+
				"magnetometer=(), interest-cohort=()")
		if r.TLS != nil {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		h.Set("Content-Security-Policy",
			"default-src 'self'; "+
				"script-src 'self' 'unsafe-inline' 'unsafe-eval' blob:; "+
				"style-src 'self' 'unsafe-inline'; "+
				"img-src 'self' data: blob: https://*.whatsapp.net https://*.cdninstagram.com https://*.atlassian.net https://*.atl-paas.net https://secure.gravatar.com https://*.wp.com https://*.githubusercontent.com; "+
				"font-src 'self' data:; "+
				// blob: in connect-src: bare-mux (Ultraviolet) opens blob:
				// channels for communication between the Service Worker and the
				// SharedWorker. Without it the proxied iframe's fetch fails with
				// "Refused to connect because it violates the document's CSP" —
				// seen on the ChatGPT tab (chatgpt.com through UV). The Browser
				// tab works with a looser CSP because it browses a single origin.
				"connect-src 'self' ws: wss: http: https: blob:; "+
				"worker-src 'self' blob:; "+
				// frame-src/media-src need blob: for the file preview: a PDF opens
				// in an <iframe src="blob:..."> and video/audio in a
				// <video/audio src="blob:...">. Without it both inherit
				// default-src 'self', which does NOT cover blob:, and the browser
				// blocks the preview ("blocked content") — only images got through
				// (img-src already had blob:). 'self' kept: the tunneled browser
				// (UV) and the Grafana embed go on working.
				"frame-src 'self' blob:; "+
				"media-src 'self' blob:; "+
				"frame-ancestors 'self'; "+
				"base-uri 'self'")
		next.ServeHTTP(w, r)
	})
}

// WithLogging is a placeholder — it currently logs nothing. Reserved for future
// duration/method/status instrumentation without having to rewrap the ServeMux.
func WithLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { next.ServeHTTP(w, r) })
}
