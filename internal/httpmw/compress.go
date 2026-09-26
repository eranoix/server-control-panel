// middleware_compress.go — HTTP gzip compression middleware + a global body
// limit. Both applied in the mux chain BEFORE any handler.
//
// We do not compress:
//   - WebSocket upgrades (Upgrade: websocket)
//   - Server-Sent Events (text/event-stream — streaming, gzip gets in the way)
//   - Small responses (≤ minGzipBytes) — overhead > gain
//   - Already-compressed types (image, video, audio, gz, br)
//
// MaxBody applies MaxBytesReader on non-WS routes, keeping an upload without a
// Content-Length from consuming unbounded RAM. Routes that need more (a large
// file upload) call RegisterLargeBody at setup, BEFORE the first request — a
// handler cannot widen the limit on its own (MaxBytesReader does not unlock once
// applied).
package httpmw

import (
	"bufio"
	"compress/gzip"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
)

const (
	defaultMaxBodyBytes = 25 << 20 // 25 MiB — modest uploads; raise it with an override
	minGzipBytes        = 1024     // not worth compressing < 1KB
)

// largeBodyRule is a one-off exception to MaxBody's global ceiling — used by
// specific routes that legitimately need more than 25 MiB (e.g. media upload from
// the mobile BFF, the same 100 MiB ceiling the panel uses). match runs per
// request; if it matches AND max is larger than the current ceiling, max wins.
type largeBodyRule struct {
	match func(*http.Request) bool
	max   int64
}

var (
	largeBodyMu    sync.RWMutex
	largeBodyRules []largeBodyRule
)

// RegisterLargeBody registers an exception to MaxBody's global ceiling for
// requests that match — without affecting any other route's ceiling. It is meant
// to be called exactly once, at process setup (in the init() of whoever registers
// the route that needs the larger body), never per request; the lock is there
// only as safety for tests that build the server more than once in the same
// process.
//
// It exists because http.MaxBytesReader does not "unlock": wrapping again with a
// larger limit does not cancel a smaller one already applied by an earlier wrapper
// on the same r.Body — the smallest always wins. No handler can ask for more bytes
// on its own; the route has to announce itself here BEFORE the request arrives.
func RegisterLargeBody(match func(*http.Request) bool, max int64) {
	largeBodyMu.Lock()
	defer largeBodyMu.Unlock()
	largeBodyRules = append(largeBodyRules, largeBodyRule{match: match, max: max})
}

func maxBodyBytesFor(r *http.Request) int64 {
	largeBodyMu.RLock()
	defer largeBodyMu.RUnlock()
	limit := int64(defaultMaxBodyBytes)
	for _, rule := range largeBodyRules {
		if rule.max > limit && rule.match(r) {
			limit = rule.max
		}
	}
	return limit
}

// gzipWriter encapsulates the pool and the "first write" window used to decide
// gzip on/off based on the detected Content-Type.
type gzipWriter struct {
	http.ResponseWriter
	gz          *gzip.Writer
	wroteHeader bool
	headerSent  bool
	bypass      bool // if true, forwards straight to the ResponseWriter
	usedGz      bool // true if at least one Write went through the gz writer
}

func (g *gzipWriter) WriteHeader(code int) {
	if g.headerSent {
		return
	}
	g.wroteHeader = true
	g.decide()
	g.headerSent = true
	g.ResponseWriter.WriteHeader(code)
}

func (g *gzipWriter) decide() {
	if g.bypass {
		return
	}
	ct := g.Header().Get("Content-Type")
	if ct == "" || !isCompressible(ct) {
		g.bypass = true
		return
	}
	// Do not compress if a Content-Encoding is already set (the handler customized it)
	if g.Header().Get("Content-Encoding") != "" {
		g.bypass = true
		return
	}
	g.Header().Set("Content-Encoding", "gzip")
	g.Header().Set("Vary", "Accept-Encoding")
	g.Header().Del("Content-Length") // invalid after gzip
}

func (g *gzipWriter) Write(b []byte) (int, error) {
	if !g.headerSent {
		if g.Header().Get("Content-Type") == "" {
			g.Header().Set("Content-Type", http.DetectContentType(b))
		}
		g.WriteHeader(http.StatusOK)
	}
	if g.bypass {
		return g.ResponseWriter.Write(b)
	}
	g.usedGz = true
	return g.gz.Write(b)
}

// Flush implements http.Flusher to preserve streaming when bypassing.
func (g *gzipWriter) Flush() {
	if g.bypass {
		if f, ok := g.ResponseWriter.(http.Flusher); ok {
			f.Flush()
		}
		return
	}
	// If Flush is called before any Write, there is no gz state to flush —
	// this avoids corrupting the trailer.
	if g.usedGz {
		_ = g.gz.Flush()
	}
	if f, ok := g.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack exposes the http.Hijacker interface for WS upgrades — without it the
// middleware would break gorilla/websocket Upgrade.
func (g *gzipWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := g.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}

func isCompressible(ct string) bool {
	ct = strings.ToLower(ct)
	if i := strings.Index(ct, ";"); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	switch {
	case strings.HasPrefix(ct, "text/"):
		// text/event-stream does NOT — streaming breaks under gzip buffering.
		if ct == "text/event-stream" {
			return false
		}
		return true
	case ct == "application/json", ct == "application/javascript",
		ct == "application/manifest+json", ct == "application/xml",
		ct == "application/xhtml+xml", ct == "application/wasm",
		ct == "image/svg+xml":
		return true
	}
	return false
}

var gzWriterPool = sync.Pool{
	New: func() any {
		w, _ := gzip.NewWriterLevel(io.Discard, gzip.DefaultCompression)
		return w
	},
}

// Compress wraps a handler with transparent gzip when the client accepts
// it. Skips: WS upgrades (Connection: Upgrade), clients without
// Accept-Encoding gzip.
//
// Historic bug (fixed): gz.Close() wrote the gzip trailer (8 bytes of CRC +
// size) into the underlying writer EVEN when bypassing — a non-compressible
// Content-Type such as application/manifest+json became JSON + binary
// garbage, breaking the browser's parse. Now gz.Close() only runs if at
// least one Write went through it (gw.usedGz).
//
// Requests carrying a Range header (partial/resumable download, see the
// file download in internal/mobilebff) skip compression too: the
// Content-Range http.ServeContent computes describes byte positions in the
// ORIGINAL file, but gzip produces a body of a different size — if this
// middleware compressed the response, the client would receive compressed
// bytes labeled with the uncompressed file's Content-Range, corrupting every
// resumed download. nginx/Apache make the same decision (they do not combine
// Content-Encoding with Range) for the same reason.
func Compress(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip WS upgrades — the hijacker needs the raw RW.
		if strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade") {
			next.ServeHTTP(w, r)
			return
		}
		if r.Header.Get("Range") != "" {
			next.ServeHTTP(w, r)
			return
		}
		if !acceptsGzip(r.Header.Get("Accept-Encoding")) {
			next.ServeHTTP(w, r)
			return
		}
		gz := gzWriterPool.Get().(*gzip.Writer)
		gz.Reset(w)
		gw := &gzipWriter{ResponseWriter: w, gz: gz}
		defer func() {
			// ONLY flush/close the gzip writer if it was actually used.
			// Otherwise we would write a gzip trailer into a body whose
			// content-type is not compressible.
			if gw.usedGz {
				_ = gz.Close()
			}
			gzWriterPool.Put(gz)
		}()
		next.ServeHTTP(gw, r)
	})
}

func acceptsGzip(h string) bool {
	for _, part := range strings.Split(h, ",") {
		part = strings.TrimSpace(part)
		if part == "gzip" || strings.HasPrefix(part, "gzip;") {
			return true
		}
	}
	return false
}

// MaxBody applies MaxBytesReader to every non-WS request.
// Routes that accept a larger upload can override it explicitly.
func MaxBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade") {
			next.ServeHTTP(w, r)
			return
		}
		// GET/HEAD have no body — skip so as not to interfere.
		if r.Body == nil || r.Method == http.MethodGet || r.Method == http.MethodHead {
			next.ServeHTTP(w, r)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytesFor(r))
		next.ServeHTTP(w, r)
	})
}
