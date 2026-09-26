package caso

import "net/http"

// M1 — the obvious form: a free execution route, declared literally.
// Caught by P1a (route outside the allowlist). A textual grep would also catch
// this one; it is here as a floor, not as the hard case.
func monta(mux *http.ServeMux, h http.HandlerFunc) {
	mux.HandleFunc("GET /healthz", h)
	mux.HandleFunc("POST /exec", h)
}
