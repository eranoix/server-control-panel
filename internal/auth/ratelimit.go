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

// Limiter is an IP-based sliding-window rate limiter.
type Limiter struct {
	mu          sync.Mutex
	buckets     map[string]*bucket
	maxAttempts int
	window      time.Duration
}

// NewLimiter creates a new Limiter allowing maxAttempts within the given window.
func NewLimiter(maxAttempts int, window time.Duration) *Limiter {
	return &Limiter{
		buckets:     make(map[string]*bucket),
		maxAttempts: maxAttempts,
		window:      window,
	}
}

// Allow returns false if the IP has exceeded maxAttempts within the window.
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

	// Prune old attempts.
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

// Reset clears any recorded attempts for the given IP.
func (l *Limiter) Reset(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.buckets, ip)
}

// ---------- Lockout (per-username brute-force protection) ----------

type lockoutEntry struct {
	failures    int
	lockedUntil time.Time
	lastFail    time.Time
}

// Lockout tracks failed authentication attempts per username and applies an
// escalating cooldown to defeat slow-trickle brute force attacks that bypass
// IP-based limits (e.g., attackers rotating through a botnet).
type Lockout struct {
	mu        sync.Mutex
	entries   map[string]*lockoutEntry
	threshold int           // failures before lock kicks in
	base      time.Duration // first lock duration
	cap       time.Duration // maximum lock duration
	window    time.Duration // failures older than this are forgotten
}

// NewLockout configures a per-username lockout.
//
//	threshold: number of failures before the first lock
//	base:      duration of the first lock
//	cap:       upper bound on lock duration
//	window:    period after which an idle entry is discarded
func NewLockout(threshold int, base, capDur, window time.Duration) *Lockout {
	return &Lockout{
		entries:   make(map[string]*lockoutEntry),
		threshold: threshold,
		base:      base,
		cap:       capDur,
		window:    window,
	}
}

// Allowed returns (true, _) if the username may attempt a login right now.
// When locked, returns (false, until) so the caller can tell the client
// exactly when to retry.
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
	// Forget stale entries so a long-idle user starts fresh.
	if now.Sub(e.lastFail) > l.window {
		delete(l.entries, username)
	}
	return true, time.Time{}
}

// RecordFailure increments the failure count and may extend the lock window.
// Cooldown doubles every `threshold` failures past the trigger, up to cap.
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
	// step: every `threshold` extra failures doubles the cooldown.
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

// RecordSuccess clears failures for the username.
func (l *Lockout) RecordSuccess(username string) {
	if username == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.entries, username)
}

// trustedProxyIP returns true when RemoteAddr is loopback or on a private
// network (RFC1918) — only then are XFF/X-Real-IP trustworthy. Without this,
// any client on the public network could forge the header and bypass the
// rate limit and the lockout.
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

// ClientIP extracts the client IP from common proxy headers or RemoteAddr.
// XFF/X-Real-IP are honoured only when RemoteAddr is a trusted proxy (loopback
// or a private network); otherwise an attacker forges the header and bypasses
// the lockout.
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
		// Fallback: crude split on last colon.
		if idx := strings.LastIndex(r.RemoteAddr, ":"); idx >= 0 {
			return r.RemoteAddr[:idx]
		}
		return r.RemoteAddr
	}
	return host
}
