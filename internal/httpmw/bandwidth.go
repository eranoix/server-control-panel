package httpmw

import (
	"bufio"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"server-control-panel/internal/auth"
)

// Per-session byte tracker (key: JTI). Accumulated in memory; resets when the
// binary restarts. It captures the request body (BytesIn) and the response body
// (BytesOut), WebSocket traffic after hijack included, by wrapping net.Conn.
//
// Cost: two atomic.AddInt64 per I/O chunk. Imperceptible.

type sessionBW struct {
	BytesIn  int64
	BytesOut int64
	Since    int64 // unix timestamp of the session's first request
}

var (
	bwMu  sync.RWMutex
	bwMap = make(map[string]*sessionBW)
)

// bwTTL: JTIs idle for longer than this are purged. The 7-day default covers
// standard session tokens (24h) with room to spare, plus invite tokens.
const bwTTL = 7 * 24 * time.Hour

// bwGCOnce guards the garbage collector's single start. Called lazily from the
// first bwGetOrCreate — main does not have to orchestrate it.
var (
	bwGCOnce sync.Once
	bwGCStop = make(chan struct{})
)

// BWShutdown signals the GC to stop (called by the signal handler in main).
func BWShutdown() {
	select {
	case <-bwGCStop:
	default:
		close(bwGCStop)
	}
}

func bwStartGC() {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				// A GC panic must not take the app down.
			}
		}()
		// Gentle tick (1h) — the purge is cheap but pointless to run often.
		t := time.NewTicker(1 * time.Hour)
		defer t.Stop()
		for {
			select {
			case <-bwGCStop:
				return
			case <-t.C:
				cutoff := time.Now().Add(-bwTTL).Unix()
				bwMu.Lock()
				for k, v := range bwMap {
					if v.Since < cutoff {
						delete(bwMap, k)
					}
				}
				bwMu.Unlock()
			}
		}
	}()
}

func bwGetOrCreate(jti string) *sessionBW {
	bwGCOnce.Do(bwStartGC)
	bwMu.RLock()
	s, ok := bwMap[jti]
	bwMu.RUnlock()
	if ok {
		return s
	}
	bwMu.Lock()
	defer bwMu.Unlock()
	if s, ok := bwMap[jti]; ok {
		return s
	}
	s = &sessionBW{Since: time.Now().Unix()}
	bwMap[jti] = s
	return s
}

// BWReset clears the JTI's counters. Used by /api/session/bandwidth/reset.
func BWReset(jti string) {
	bwMu.Lock()
	delete(bwMap, jti)
	bwMu.Unlock()
}

// Snapshot returns the current state. Used by the /api/session/bandwidth handler.
func BWSnapshot(jti string) sessionBW {
	bwMu.RLock()
	defer bwMu.RUnlock()
	s, ok := bwMap[jti]
	if !ok {
		return sessionBW{Since: time.Now().Unix()}
	}
	return sessionBW{
		BytesIn:  atomic.LoadInt64(&s.BytesIn),
		BytesOut: atomic.LoadInt64(&s.BytesOut),
		Since:    s.Since,
	}
}

// bwResponseWriter wraps an http.ResponseWriter counting the bytes written to the
// body. It implements http.Hijacker to support WebSocket — after the hijack it
// returns a wrapped net.Conn that keeps counting Read/Write.
type bwResponseWriter struct {
	http.ResponseWriter
	in  *int64
	out *int64
}

func (w *bwResponseWriter) Write(b []byte) (int, error) {
	n, err := w.ResponseWriter.Write(b)
	atomic.AddInt64(w.out, int64(n))
	return n, err
}

func (w *bwResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hj, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	c, rw, err := hj.Hijack()
	if err != nil {
		return c, rw, err
	}
	return &bwConn{Conn: c, in: w.in, out: w.out}, rw, nil
}

func (w *bwResponseWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// bwConn wraps net.Conn to count bytes read/written. Crucial for WebSocket —
// after the hijack, ResponseWriter.Write is never called again; all of the
// framebuffer/streaming traffic goes through this Conn.
type bwConn struct {
	net.Conn
	in  *int64
	out *int64
}

func (c *bwConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	atomic.AddInt64(c.in, int64(n))
	return n, err
}

func (c *bwConn) Write(b []byte) (int, error) {
	n, err := c.Conn.Write(b)
	atomic.AddInt64(c.out, int64(n))
	return n, err
}

// bwReadCloser counts bytes from request.Body. It matters for file uploads and
// large POSTs, but is marginal for typical use.
type bwReadCloser struct {
	rc interface {
		Read([]byte) (int, error)
		Close() error
	}
	counter *int64
}

func (r *bwReadCloser) Read(b []byte) (int, error) {
	n, err := r.rc.Read(b)
	atomic.AddInt64(r.counter, int64(n))
	return n, err
}
func (r *bwReadCloser) Close() error { return r.rc.Close() }

// Bandwidth wraps EVERY route to count IN/OUT bytes per session. It works both on
// authenticated routes (reading the JTI from the context, set by auth.Middleware)
// and on anonymous/static ones (reading the JTI straight from the cookie via
// authSvc.JTIFromAny — without validating revocation, only to associate).
//
// For requests with NO token (an anonymous visitor, /api/health before login) it
// does not count — to avoid a mess of unknown keys.
func Bandwidth(authSvc *auth.Service, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var jti string
		if authSvc != nil {
			jti = authSvc.JTIFromAny(r)
		} else {
			jti = auth.JTIFrom(r)
		}
		if jti == "" {
			next.ServeHTTP(w, r)
			return
		}
		s := bwGetOrCreate(jti)
		if r.Body != nil {
			r.Body = &bwReadCloser{rc: r.Body, counter: &s.BytesIn}
		}
		bw := &bwResponseWriter{ResponseWriter: w, in: &s.BytesIn, out: &s.BytesOut}
		next.ServeHTTP(bw, r)
	})
}
