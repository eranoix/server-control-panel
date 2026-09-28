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

type sessionBW struct {
	BytesIn  int64
	BytesOut int64
	Since    int64
}

var (
	bwMu  sync.RWMutex
	bwMap = make(map[string]*sessionBW)
)

const bwTTL = 7 * 24 * time.Hour

var (
	bwGCOnce sync.Once
	bwGCStop = make(chan struct{})
)

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
			}
		}()
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

func BWReset(jti string) {
	bwMu.Lock()
	delete(bwMap, jti)
	bwMu.Unlock()
}

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
