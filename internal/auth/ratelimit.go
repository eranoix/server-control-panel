package auth

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type bucket struct {
	attempts []time.Time
}

type Limiter struct {
	mu          sync.Mutex
	buckets     map[string]*bucket
	maxAttempts int
	window      time.Duration
}

func NewLimiter(maxAttempts int, window time.Duration) *Limiter {
	return &Limiter{
		buckets:     make(map[string]*bucket),
		maxAttempts: maxAttempts,
		window:      window,
	}
}

func (l *Limiter) Allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-l.window)

	b, ok := l.buckets[ip]
	if !ok {
		b = &bucket{}
		l.buckets[ip] = b
	}

	pruned := b.attempts[:0]
	for _, t := range b.attempts {
		if t.After(cutoff) {
			pruned = append(pruned, t)
		}
	}
	b.attempts = pruned

	if len(b.attempts) >= l.maxAttempts {
		return false
	}
	b.attempts = append(b.attempts, now)
	return true
}

func (l *Limiter) Reset(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.buckets, ip)
}

type lockoutEntry struct {
	failures    int
	lockedUntil time.Time
	lastFail    time.Time
}

type Lockout struct {
	mu        sync.Mutex
	entries   map[string]*lockoutEntry
	threshold int
	base      time.Duration
	cap       time.Duration
	window    time.Duration
}

func NewLockout(threshold int, base, capDur, window time.Duration) *Lockout {
	return &Lockout{
		entries:   make(map[string]*lockoutEntry),
		threshold: threshold,
		base:      base,
		cap:       capDur,
		window:    window,
	}
}

func (l *Lockout) Allowed(username string) (bool, time.Time) {
	if username == "" {
		return true, time.Time{}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.entries[username]
	now := time.Now()
	if e == nil {
		return true, time.Time{}
	}
	if e.lockedUntil.After(now) {
		return false, e.lockedUntil
	}
	if now.Sub(e.lastFail) > l.window {
		delete(l.entries, username)
	}
	return true, time.Time{}
}

func (l *Lockout) RecordFailure(username string) {
	if username == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.entries[username]
	if e == nil {
		e = &lockoutEntry{}
		l.entries[username] = e
	}
	e.failures++
	e.lastFail = time.Now()
	if e.failures < l.threshold {
		return
	}
	over := e.failures - l.threshold
	step := over / l.threshold
	dur := l.base
	for i := 0; i < step && dur < l.cap; i++ {
		dur *= 2
	}
	if dur > l.cap {
		dur = l.cap
	}
	e.lockedUntil = time.Now().Add(dur)
}

func (l *Lockout) RecordSuccess(username string) {
	if username == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.entries, username)
}

func trustedProxyIP(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(strings.TrimSpace(host))
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsPrivate() {
		return true
	}
	return false
}

func ClientIP(r *http.Request) string {
	if trustedProxyIP(r.RemoteAddr) {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			return strings.TrimSpace(parts[0])
		}
		if xr := r.Header.Get("X-Real-IP"); xr != "" {
			return strings.TrimSpace(xr)
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		if idx := strings.LastIndex(r.RemoteAddr, ":"); idx >= 0 {
			return r.RemoteAddr[:idx]
		}
		return r.RemoteAddr
	}
	return host
}
