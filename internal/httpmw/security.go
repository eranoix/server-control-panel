package httpmw

import "net/http"

func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
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
				"connect-src 'self' ws: wss: http: https: blob:; "+
				"worker-src 'self' blob:; "+
				"frame-src 'self' blob:; "+
				"media-src 'self' blob:; "+
				"frame-ancestors 'self'; "+
				"base-uri 'self'")
		next.ServeHTTP(w, r)
	})
}

func WithLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { next.ServeHTTP(w, r) })
}
