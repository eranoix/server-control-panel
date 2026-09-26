package caso

import "net/http"

// FP1 — NEGATIVE CONTROL, found in production.
//
// `Handle` is the type of the opaque token, and `tipo.Handle(x)` is a TYPE
// CONVERSION. A detector matching on the selector name would read this as a
// route declaration and fail it — a false positive, which is the most
// reliable way to get someone to switch a pin off.
//
// This file has to PASS: the only route declared is the allowed one.
type Handle string

type tipo struct{}

func (tipo) Handle(s string) Handle { return Handle(s) }

var conv tipo

func lê(r *http.Request) Handle {
	// A conversion / method call named `Handle` — not a route.
	return conv.Handle(r.PathValue("handle"))
}

func monta(mux *http.ServeMux, h http.HandlerFunc) {
	mux.HandleFunc("GET /healthz", h)
}
