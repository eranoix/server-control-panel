package fixture

import "net/http"

// M2 — the form GREP DOES NOT SEE: the route has another name, and argv enters
// through the handler's signature. It is the proof that the AST pin is what protects.
type request struct {
	Argv []string `json:"argv"`
}

func run(argv []string) error { return nil }

func mount(mux *http.ServeMux, h http.HandlerFunc) {
	mux.HandleFunc("GET /healthz", h)
	mux.HandleFunc("POST /run", h)
}
