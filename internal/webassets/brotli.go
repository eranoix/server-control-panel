package webassets

import (
	"bytes"
	"net/http"
	"strings"
	"sync"

	"github.com/andybalholm/brotli"
)

// Brotli compression with an in-memory cache.
//
// The middleware's gzip compresses the SAME response on every request, at the
// default level. For the files that dominate the first load — the 1.2 MB index
// and the app bundle — that is expensive twice over: it burns CPU over and over
// and ships more bytes than necessary. Brotli at the maximum level takes 22%
// off gzip on the index (252 KB → 196 KB) and 18% on the bundle (154 KB →
// 125 KB); on a bad link that is seconds less of white screen, and the cost is
// paid ONCE per build.
//
// These files are immutable within a run (the index is assembled once with the
// BuildStamp; the assets come from the embed.FS), so caching the compressed
// form is safe by construction — there is no way to serve a stale version of
// content that does not change.
var (
	brCache   sync.Map // key (with the build baked in) -> compressed []byte
	brEmCurso sync.Map // key -> compression in flight (one compressor per key)
)

// aceitaBrotli is deliberately simple: "br" in the list is enough. An explicit
// q=0 is rare enough not to be worth a quality parser here — and the worst case
// of getting it wrong is serving brotli to someone who asked for gzip first,
// which every browser that advertises br knows how to read.
func aceitaBrotli(r *http.Request) bool {
	for _, parte := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		if strings.EqualFold(strings.TrimSpace(strings.SplitN(parte, ";", 2)[0]), "br") {
			return true
		}
	}
	return false
}

// brotliOnce returns the compressed body IF it is already ready. If it is not,
// it kicks the compression off in the background and returns nil — the caller
// serves the original on this request and the next one already gets the
// compressed form.
//
// Not blocking is the whole point: at the maximum level, compressing the 1.2 MB
// index takes ~2.3 s. Doing that on the request path would mean the first load
// after EVERY deploy — and there are dozens a day — waiting 2.3 s longer, i.e.
// the optimization would worsen exactly the case it exists to improve. This way
// the cost leaves the critical path and the maximum level comes for free: q=11
// gives 196 KB against 226 KB for q=5 and 252 KB for gzip (measured on the real index).
func brotliOnce(chave string, corpo []byte) []byte {
	if v, ok := brCache.Load(chave); ok {
		b, _ := v.([]byte)
		return b
	}
	// LoadOrStore guarantees a single compressor per key even with N simultaneous
	// requests right after a deploy.
	if _, jaRodando := brEmCurso.LoadOrStore(chave, true); jaRodando {
		return nil
	}
	// Copy: the caller may be handing us a buffer it reuses.
	dados := append([]byte(nil), corpo...)
	go func() {
		defer brEmCurso.Delete(chave)
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
		// Content that does not compress (already compressed, or tiny) is no gain:
		// storing nil avoids recompressing it on every request for nothing.
		if len(out) >= len(dados) {
			brCache.Store(chave, []byte(nil))
			return
		}
		brCache.Store(chave, out)
	}()
	return nil
}

// serveBrotli writes the compressed body if the client accepts it and the
// compression was worth it. Returns false when the caller should follow the
// normal path (no encoding), and in that case NOTHING was written to the response.
func serveBrotli(w http.ResponseWriter, r *http.Request, chave string, corpo []byte) bool {
	if !aceitaBrotli(r) {
		return false
	}
	comprimido := brotliOnce(chave, corpo)
	if comprimido == nil {
		return false
	}
	w.Header().Set("Content-Encoding", "br")
	// Without Vary, an intermediate cache can hand the brotli body to a client
	// that never advertised br.
	w.Header().Add("Vary", "Accept-Encoding")
	_, _ = w.Write(comprimido)
	return true
}

// ServeBrotliAsset exposes the brotli path to whoever serves assets outside
// this package (the /vendor handler in the router). Same contract: it returns
// false without having written anything when the caller should take the normal path.
func ServeBrotliAsset(w http.ResponseWriter, r *http.Request, chave string, corpo []byte) bool {
	return serveBrotli(w, r, chave, corpo)
}

// AceitaBrotli reports whether the client advertised support — used to vary the
// ETag by encoding before the file is even read.
func AceitaBrotli(r *http.Request) bool { return aceitaBrotli(r) }
