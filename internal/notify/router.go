package notify

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultBufSize          = 1024
	defaultThrottleWindow   = 300
	defaultBreakerThreshold = 5
	defaultBreakerOpenSec   = 60
	defaultSendTimeout      = 5 * time.Second
	historyCapacity         = 500
)

type breakerState struct {
	failures  int
	openUntil int64
}

type Options struct {
	DataDir          string
	Channels         map[string]Channel
	BufSize          int
	ThrottleWindow   int64
	BreakerThreshold int
	BreakerOpenSec   int64
	SendTimeout      time.Duration
	Now              func() int64
}

type Router struct {
	dir      string
	channels map[string]Channel
	bufSize  int
	window   int64
	brkLimit int
	brkOpen  int64
	sendTO   time.Duration
	now      func() int64

	cfgMu     sync.RWMutex
	rules     []Rule
	channelsC map[string]ChannelDef

	throttle map[string]int64
	breaker  map[string]*breakerState

	histMu  sync.Mutex
	history *ring
	inboxMu sync.Mutex
	inbox   *ring
	dropped int64

	ch   chan Event
	quit chan struct{}
	done chan struct{}

	onHandled func(Event)
}

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

func (r *Router) Close() {
	close(r.quit)
	<-r.done
}

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
		atomic.AddInt64(&r.dropped, 1)
	}
}

func (r *Router) Dropped() int64 { return atomic.LoadInt64(&r.dropped) }

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

func (r *Router) handle(ev Event) {
	r.cfgMu.RLock()
	rules := r.rules
	chans := r.channelsC
	impls := r.channels
	r.cfgMu.RUnlock()

	now := r.now()
	r.evictThrottle(now)

	for _, rl := range rules {
		if !rl.matches(ev) {
			continue
		}
		if ev.DedupKey != "" {
			key := ev.DedupKey + "|" + rl.ID
			if last, ok := r.throttle[key]; ok && now-last < r.window {
				continue
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
			evForRule := ev
			evForRule.RuleID = rl.ID
			r.sendVia(def, impl, evForRule, now)
		}
	}

	if r.onHandled != nil {
		r.onHandled(ev)
	}
}

func (r *Router) sendVia(def ChannelDef, impl Channel, ev Event, now int64) {
	b := r.breaker[def.ID]
	if b == nil {
		b = &breakerState{}
		r.breaker[def.ID] = b
	}
	if now < b.openUntil {
		return
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

func (r *Router) evictThrottle(now int64) {
	for k, ts := range r.throttle {
		if now-ts > r.window {
			delete(r.throttle, k)
		}
	}
}

func (r *Router) throttleLen() int { return len(r.throttle) }
