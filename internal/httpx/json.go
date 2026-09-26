// Package httpx — HTTP helpers shared by every domain handler. It holds no
// business logic; only the primitives every route uses (JSON, cookies, audit,
// RBAC checks).
//
// It imports leaf packages only (auth, config, scope) — never a domain
// package. Each domain imports httpx, not the other way around.
package httpx

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
)

// WriteJSON serializes v as application/json and sends it. If the encode fails
// (client closed the connection, invalid type) it logs but does not try to fix
// anything — the header has already gone out.
func WriteJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("httpx.WriteJSON encode err: %v", err)
	}
}

// WriteErr sends a JSON error `{"error": msg}` with the given status code.
// The convention followed by every handler in vps-manager.
func WriteErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// DecodeBody decodes JSON from the body into a destination. It returns an error
// ready to hand to WriteErr (400 "bad json"). The body is consumed in full;
// call it only once per request.
func DecodeBody(r io.Reader, dst interface{}) error {
	return json.NewDecoder(r).Decode(dst)
}
