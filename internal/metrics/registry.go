package metrics

import (
	"context"
	"sort"
	"sync"
	"time"
)

type MetricDescriptor struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Unit     string `json:"unit"`
	Category string `json:"category"`
	Kind     string `json:"kind"`
}

type Snapshot struct {
	T      int64              `json:"t"`
	Values map[string]float64 `json:"values"`
}

type Collector interface {
	Describe() []MetricDescriptor
	Collect(ctx context.Context) map[string]float64
	Interval() time.Duration
}

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

type Registry struct {
	collectors []Collector

	mu      sync.RWMutex
	cache   []map[string]float64
	lastRun []time.Time
	last    Snapshot
}

func NewRegistry() *Registry {
	return &Registry{last: Snapshot{Values: map[string]float64{}}}
}

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

func (r *Registry) Gather(ctx context.Context) Snapshot {
	now := time.Now()
	merged := make(map[string]float64)
	for i, c := range r.collectors {
		r.mu.RLock()
		due := r.lastRun[i].IsZero() || now.Sub(r.lastRun[i]) >= c.Interval()
		vals := r.cache[i]
		r.mu.RUnlock()
		if due {
			fresh := c.Collect(ctx)
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

func (r *Registry) Latest() Snapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	cp := make(map[string]float64, len(r.last.Values))
	for k, v := range r.last.Values {
		cp[k] = v
	}
	return Snapshot{T: r.last.T, Values: cp}
}

type HistPoint struct {
	T int64   `json:"t"`
	V float64 `json:"v"`
}

type MetricHistory struct {
	mu   sync.RWMutex
	cap  int
	keys map[string][]HistPoint
}

func NewMetricHistory(capacity int) *MetricHistory {
	if capacity < 1 {
		capacity = 1
	}
	return &MetricHistory{cap: capacity, keys: map[string][]HistPoint{}}
}

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

func (h *MetricHistory) Series(key string) []HistPoint {
	h.mu.RLock()
	defer h.mu.RUnlock()
	src := h.keys[key]
	out := make([]HistPoint, len(src))
	copy(out, src)
	return out
}
