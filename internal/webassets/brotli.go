package webassets

import (
	"bytes"
	"net/http"
	"strings"
	"sync"

	"github.com/andybalholm/brotli"
)

var (
	brCache    sync.Map
	brInFlight sync.Map
)

func acceptsBrotli(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		if strings.EqualFold(strings.TrimSpace(strings.SplitN(part, ";", 2)[0]), "br") {
			return true
		}
	}
	return false
}

func brotliOnce(key string, body []byte) []byte {
	if v, ok := brCache.Load(key); ok {
		b, _ := v.([]byte)
		return b
	}
	if _, alreadyRunning := brInFlight.LoadOrStore(key, true); alreadyRunning {
		return nil
	}
	dados := append([]byte(nil), body...)
	go func() {
		defer brInFlight.Delete(key)
		var buf bytes.Buffer
		w := brotli.NewWriterLevel(&buf, brotli.BestCompression)
		if _, err := w.Write(dados); err != nil {
			_ = w.Close()
			return
		}
		if err := w.Close(); err != nil {
			return
		}
		out := buf.Bytes()
		if len(out) >= len(dados) {
			brCache.Store(key, []byte(nil))
			return
		}
		brCache.Store(key, out)
	}()
	return nil
}

func serveBrotli(w http.ResponseWriter, r *http.Request, key string, body []byte) bool {
	if !acceptsBrotli(r) {
		return false
	}
	compressed := brotliOnce(key, body)
	if compressed == nil {
		return false
	}
	w.Header().Set("Content-Encoding", "br")
	w.Header().Add("Vary", "Accept-Encoding")
	_, _ = w.Write(compressed)
	return true
}

func ServeBrotliAsset(w http.ResponseWriter, r *http.Request, key string, body []byte) bool {
	return serveBrotli(w, r, key, body)
}

func AcceptsBrotli(r *http.Request) bool { return acceptsBrotli(r) }
