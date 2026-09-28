package httpx

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
)

func WriteJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("httpx.WriteJSON encode err: %v", err)
	}
}

func WriteErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func DecodeBody(r io.Reader, dst interface{}) error {
	return json.NewDecoder(r).Decode(dst)
}
