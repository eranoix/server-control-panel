package notify

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// CONCURRENCY ARMOR — read before touching anything in this file.
//
// The Router has exactly THREE ownership domains. Keeping them separate is the
// whole point; mixing them is what `go test -race` exists to catch.
//
//  1. CONFIG (rules, channels): copy-on-write under cfgMu (RWMutex). Mutators
//     (CRUD) build a NEW slice/map and swap the pointer under Lock. The worker
//     grabs the current reference under a brief RLock and then iterates that
//     immutable snapshot lock-free. NEVER mutate a published slice/map in place.
//  2. WORKER-ONLY (throttle, breaker): touched solely inside handle(), which
//     runs only in the single run() goroutine. No locks, no other readers.
//  3. CROSS-GOROUTINE (history, dropped): history under histMu (Dispatch writes,
//     History/DryRun read); dropped via sync/atomic. The worker never touches
//     these — so Dispatch can record history without ever blocking on a send.
//
// Dispatch is non-blocking and takes NO caller lock: a job finishing under
// queue.mu calls it directly and returns in microseconds.
// ─────────────────────────────────────────────────────────────────────────────

// Tunables (overridable via Options for deterministic tests).
const (
	defaultBufSize          = 1024
	defaultThrottleWindow   = 300 // seconds: identical (DedupKey,rule) within this window sends once
	defaultBreakerThreshold = 5   // consecutive channel failures before opening
	defaultBreakerOpenSec   = 60  // seconds a tripped breaker stays open
	defaultSendTimeout      = 5 * time.Second
	historyCapacity         = 500
)

// breakerState is per-channel circuit-breaker bookkeeping (worker-only).
type breakerState struct {
	failures  int
	openUntil int64 // unix sec; while now < openUntil the channel is skipped
}

// Options configures a Router. Only DataDir and Channels matter in production;
// the rest exist so tests can shrink windows and inject a fake clock.
type Options struct {
	DataDir          string
	Channels         map[string]Channel // channel TYPE id -> impl (e.g. "whatsapp")
	BufSize          int
	ThrottleWindow   int64
	BreakerThreshold int
	BreakerOpenSec   int64
	SendTimeout      time.Duration
	Now              func() int64 // unix seconds; nil -> time.Now().Unix
}

// Router is the notification spine. Construct with New; it starts its worker
// immediately. Call Close on shutdown.
type Router struct {
	dir      string
	channels map[string]Channel // type -> impl (immutable after New)
	bufSize  int
	window   int64
	brkLimit int
	brkOpen  int64
	sendTO   time.Duration
	now      func() int64

	// CONFIG domain (cfgMu, copy-on-write).
	cfgMu     sync.RWMutex
	rules     []Rule
	channelsC map[string]ChannelDef // configured destinations, id -> def

	// WORKER-ONLY domain (no locks).
	throttle map[string]int64         // "DedupKey|ruleID" -> last-send unix
	breaker  map[string]*breakerState // channelDef ID -> breaker

	// CROSS-GOROUTINE domain.
	histMu  sync.Mutex
	history *ring
	inboxMu sync.Mutex
	inbox   *ring // in-app inbox (InAppChannel sink) read by GET /api/notify/inbox
	dropped int64 // atomic

	ch   chan Event
	quit chan struct{}
	done chan struct{}

	// test-only seam: invoked at the end of handle() with the processed event.
	// nil in production. Lets tests await async processing deterministically.
	onHandled func(Event)
}

// New builds a Router, loads persisted rules/channels (tolerating absence),
// and starts the worker goroutine.
func New(opts Options) (*Router, error) {
	r := &Router{
		dir:       filepath.Join(opts.DataDir, "notify"),
		channels:  opts.Channels,
		bufSize:   orInt(opts.BufSize, defaultBufSize),
		window:    orInt64(opts.ThrottleWindow, defaultThrottleWindow),
		brkLimit:  orInt(opts.BreakerThreshold, defaultBreakerThreshold),
		brkOpen:   orInt64(opts.BreakerOpenSec, defaultBreakerOpenSec),
		sendTO:    opts.SendTimeout,
		now:       opts.Now,
		channelsC: map[string]ChannelDef{},
		throttle:  map[string]int64{},
		breaker:   map[string]*breakerState{},
		history:   newRing(historyCapacity),
		inbox:     newRing(historyCapacity),
		quit:      make(chan struct{}),
		done:      make(chan struct{}),
	}
	if r.channels == nil {
		r.channels = map[string]Channel{}
	}
	if r.sendTO == 0 {
		r.sendTO = defaultSendTimeout
	}
	if r.now == nil {
		r.now = func() int64 { return time.Now().Unix() }
	}
	r.ch = make(chan Event, r.bufSize)

	if err := r.load(); err != nil {
		return nil, err
	}
	go r.run()
	return r, nil
}

func orInt(v, def int) int {
	if v > 0 {
		return v
	}
	return def
}
func orInt64(v, def int64) int64 {
	if v > 0 {
		return v
	}
	return def
}

// Close stops the worker. Safe to call once.
func (r *Router) Close() {
	close(r.quit)
	<-r.done
}

// ── Producer side ────────────────────────────────────────────────────────────

// Dispatch records ev in history and hands it to the worker. It is
// NON-BLOCKING: on buffer overflow it drops the event and bumps the dropped
// counter rather than ever blocking the caller. Takes no caller lock and never
// touches cfgMu — safe to call from under queue.mu.
func (r *Router) Dispatch(ev Event) {
	if ev.TS == 0 {
		ev.TS = r.now()
	}
	r.histMu.Lock()
	r.history.push(ev)
	r.histMu.Unlock()

	select {
	case r.ch <- ev:
	default:
		atomic.AddInt64(&r.dropped, 1) // overflow: drop+count, never block
	}
}

// Dropped returns the number of events dropped due to buffer overflow. Silence
// is never success — the UI/health surface this so an overwhelmed bus is seen.
func (r *Router) Dropped() int64 { return atomic.LoadInt64(&r.dropped) }

// ── Worker ───────────────────────────────────────────────────────────────────

func (r *Router) run() {
	defer close(r.done)
	for {
		select {
		case <-r.quit:
			return
		case ev := <-r.ch:
			r.handle(ev)
		}
	}
}

// handle matches ev against the current rule snapshot and fans out to channels,
// applying throttle/dedup and the per-channel breaker. WORKER-ONLY: the only
// reader/writer of throttle and breaker.
func (r *Router) handle(ev Event) {
	r.cfgMu.RLock()
	rules := r.rules
	chans := r.channelsC
	impls := r.channels // snapshot (copy-on-write via AddChannelImpl)
	r.cfgMu.RUnlock()

	now := r.now()
	r.evictThrottle(now)

	for _, rl := range rules {
		if !rl.matches(ev) {
			continue
		}
		// Throttle/dedup per (DedupKey, rule). Empty DedupKey can't be deduped,
		// so it always sends (callers always set one; this is defensive).
		if ev.DedupKey != "" {
			key := ev.DedupKey + "|" + rl.ID
			if last, ok := r.throttle[key]; ok && now-last < r.window {
				continue // within window: already notified for this identity+rule
			}
			r.throttle[key] = now
		}
		for _, chID := range rl.Channels {
			def, ok := chans[chID]
			if !ok || !def.Enabled {
				continue
			}
			impl := impls[def.Type]
			if impl == nil {
				continue
			}
			// Per-rule copy: ev is a value type, so this is cheap and
			// cannot leak RuleID across the other Rules/Channels this
			// same event may also fan out to in this loop.
			evForRule := ev
			evForRule.RuleID = rl.ID
			r.sendVia(def, impl, evForRule, now)
		}
	}

	if r.onHandled != nil {
		r.onHandled(ev)
	}
}

// sendVia delivers one event through one channel, honoring + updating the
// breaker. WORKER-ONLY.
func (r *Router) sendVia(def ChannelDef, impl Channel, ev Event, now int64) {
	b := r.breaker[def.ID]
	if b == nil {
		b = &breakerState{}
		r.breaker[def.ID] = b
	}
	if now < b.openUntil {
		return // breaker open: skip without calling Send
	}

	ctx, cancel := context.WithTimeout(context.Background(), r.sendTO)
	err := impl.Send(ctx, ev, def.Config)
	cancel()

	if err != nil {
		b.failures++
		if b.failures >= r.brkLimit {
			b.openUntil = now + r.brkOpen
		}
		return
	}
	b.failures = 0
	b.openUntil = 0
}

// evictThrottle drops throttle entries older than the window so the map can't
// grow unbounded across many distinct DedupKeys (one per job id, etc.).
// WORKER-ONLY; runs once per consumed event.
func (r *Router) evictThrottle(now int64) {
	for k, ts := range r.throttle {
		if now-ts > r.window {
			delete(r.throttle, k)
		}
	}
}

// throttleLen is a test seam: reports the current throttle map size. Safe to
// call only when the worker is quiescent (tests await onHandled first).
func (r *Router) throttleLen() int { return len(r.throttle) }
