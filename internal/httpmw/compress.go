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
	defaultMaxBodyBytes = 25 << 20
	minGzipBytes        = 1024
)

type largeBodyRule struct {
	match func(*http.Request) bool
	max   int64
}

var (
	largeBodyMu    sync.RWMutex
	largeBodyRules []largeBodyRule
)

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

type gzipWriter struct {
	http.ResponseWriter
	gz          *gzip.Writer
	wroteHeader bool
	headerSent  bool
	bypass      bool
	usedGz      bool
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
	if g.Header().Get("Content-Encoding") != "" {
		g.bypass = true
		return
	}
	g.Header().Set("Content-Encoding", "gzip")
	g.Header().Set("Vary", "Accept-Encoding")
	g.Header().Del("Content-Length")
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

func (g *gzipWriter) Flush() {
	if g.bypass {
		if f, ok := g.ResponseWriter.(http.Flusher); ok {
			f.Flush()
		}
		return
	}
	if g.usedGz {
		_ = g.gz.Flush()
	}
	if f, ok := g.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

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

func Compress(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

func MaxBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade") {
			next.ServeHTTP(w, r)
			return
		}
		if r.Body == nil || r.Method == http.MethodGet || r.Method == http.MethodHead {
			next.ServeHTTP(w, r)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytesFor(r))
		next.ServeHTTP(w, r)
	})
}
