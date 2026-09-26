package metrics

// registry.go — the generic metric model.
//
// Instead of the alert engine knowing only 4 fixed fields (Point), the app now
// has a dynamic CATALOG of metrics (MetricDescriptor) and, on every tick, a
// SNAPSHOT keyed by metric (map[string]float64) gathered from pluggable
// COLLECTORS — one per subsystem (system, jobs, notify, claude, docker, ...).
// Any metric in the catalog becomes a trigger target, with no hardcoding.
// Expensive collectors (claude/docker) declare a cadence of their own; the
// Registry caches between gathers.

import (
	"context"
	"sort"
	"sync"
	"time"
)

// MetricDescriptor is the catalog metadata of a metric.
type MetricDescriptor struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Unit     string `json:"unit"`     // %, bytes, USD, tokens, count, s, load
	Category string `json:"category"` // Sistema, Jobs, Notificações, Claude, Docker, ...
	Kind     string `json:"kind"`     // gauge | counter
}

// Snapshot is one tick's photograph: values keyed by metric.
type Snapshot struct {
	T      int64              `json:"t"`
	Values map[string]float64 `json:"values"`
}

// Collector contributes a set of metrics. Collect may be expensive; Interval
// caps how often the Registry re-invokes it (cached values fill the gap).
type Collector interface {
	Describe() []MetricDescriptor
	Collect(ctx context.Context) map[string]float64
	Interval() time.Duration
}

// NewFuncCollector builds a Collector out of functions — used by most of the
// collectors, whose keys are static.
func NewFuncCollector(interval time.Duration, desc []MetricDescriptor, collect func(context.Context) map[string]float64) Collector {
	return &funcCollector{interval: interval, desc: desc, collect: collect}
}

type funcCollector struct {
	interval time.Duration
	desc     []MetricDescriptor
	collect  func(context.Context) map[string]float64
}

func (f *funcCollector) Describe() []MetricDescriptor                   { return f.desc }
func (f *funcCollector) Interval() time.Duration                        { return f.interval }
func (f *funcCollector) Collect(ctx context.Context) map[string]float64 { return f.collect(ctx) }

// Registry merges collectors into snapshots, honouring each one's Interval
// (expensive collectors run on a slower cadence; cached values fill the
// intervening ticks).
type Registry struct {
	collectors []Collector // immutable after startup wiring (read without a lock in Gather)

	mu      sync.RWMutex
	cache   []map[string]float64 // latest values per collector (index-aligned)
	lastRun []time.Time          // last gather per collector
	last    Snapshot             // last merged snapshot
}

// NewRegistry builds an empty Registry.
func NewRegistry() *Registry {
	return &Registry{last: Snapshot{Values: map[string]float64{}}}
}

// Register adds a collector. Must be called at startup only (before the sampler).
func (r *Registry) Register(c Collector) {
	if c == nil {
		return
	}
	r.collectors = append(r.collectors, c)
	r.mu.Lock()
	r.cache = append(r.cache, map[string]float64{})
	r.lastRun = append(r.lastRun, time.Time{})
	r.mu.Unlock()
}

// Catalog returns every descriptor, ordered by category and then by key.
// Stateful collectors may expose dynamic descriptors (e.g. disk per mount).
func (r *Registry) Catalog() []MetricDescriptor {
	var out []MetricDescriptor
	seen := map[string]bool{}
	for _, c := range r.collectors {
		for _, d := range c.Describe() {
			if seen[d.Key] {
				continue
			}
			seen[d.Key] = true
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Category != out[j].Category {
			return out[i].Category < out[j].Category
		}
		return out[i].Key < out[j].Key
	})
	return out
}

// DescriptorFor returns the descriptor for a key, or ok=false.
func (r *Registry) DescriptorFor(key string) (MetricDescriptor, bool) {
	for _, c := range r.collectors {
		for _, d := range c.Describe() {
			if d.Key == key {
				return d, true
			}
		}
	}
	return MetricDescriptor{}, false
}

// Gather collects the collectors that are due (cadence) and returns the merged
// snapshot. Called ONLY by the sampler goroutine. It holds no lock during
// Collect (which can block for seconds) — only while updating cache/last.
func (r *Registry) Gather(ctx context.Context) Snapshot {
	now := time.Now()
	merged := make(map[string]float64)
	for i, c := range r.collectors {
		r.mu.RLock()
		due := r.lastRun[i].IsZero() || now.Sub(r.lastRun[i]) >= c.Interval()
		vals := r.cache[i]
		r.mu.RUnlock()
		if due {
			fresh := c.Collect(ctx) // NO lock
			if fresh != nil {
				vals = fresh
			}
			r.mu.Lock()
			r.cache[i] = vals
			r.lastRun[i] = now
			r.mu.Unlock()
		}
		for k, v := range vals {
			merged[k] = v
		}
	}
	snap := Snapshot{T: now.Unix(), Values: merged}
	r.mu.Lock()
	r.last = snap
	r.mu.Unlock()
	return snap
}

// Latest returns a copy of the last merged snapshot (for /snapshot without
// re-collecting). Safe for concurrent reads by HTTP handlers.
func (r *Registry) Latest() Snapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	cp := make(map[string]float64, len(r.last.Values))
	for k, v := range r.last.Values {
		cp[k] = v
	}
	return Snapshot{T: r.last.T, Values: cp}
}

// HistPoint is one point of a metric's time series.
type HistPoint struct {
	T int64   `json:"t"`
	V float64 `json:"v"`
}

// MetricHistory keeps a bounded time series per metric key, so the trend/overlay
// of any alerted metric can be drawn. The per-key cap prevents a memory leak
// even with dozens of keys.
type MetricHistory struct {
	mu   sync.RWMutex
	cap  int
	keys map[string][]HistPoint
}

// NewMetricHistory builds a history retaining the last `capacity` points per key.
func NewMetricHistory(capacity int) *MetricHistory {
	if capacity < 1 {
		capacity = 1
	}
	return &MetricHistory{cap: capacity, keys: map[string][]HistPoint{}}
}

// Push appends every value of the snapshot to its series (capped per key).
func (h *MetricHistory) Push(s Snapshot) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for k, v := range s.Values {
		arr := append(h.keys[k], HistPoint{T: s.T, V: v})
		if len(arr) > h.cap {
			arr = arr[len(arr)-h.cap:]
		}
		h.keys[k] = arr
	}
}

// Series returns a copy of a metric's series (empty when the key is unknown).
func (h *MetricHistory) Series(key string) []HistPoint {
	h.mu.RLock()
	defer h.mu.RUnlock()
	src := h.keys[key]
	out := make([]HistPoint, len(src))
	copy(out, src)
	return out
}
