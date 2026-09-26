package metrics

import (
	"context"
	"testing"
	"time"
)

func TestRegistryGatherMergesCollectors(t *testing.T) {
	reg := NewRegistry()
	reg.Register(NewFuncCollector(0,
		[]MetricDescriptor{{Key: "a", Category: "X"}},
		func(context.Context) map[string]float64 { return map[string]float64{"a": 1} }))
	reg.Register(NewFuncCollector(0,
		[]MetricDescriptor{{Key: "b", Category: "Y"}},
		func(context.Context) map[string]float64 { return map[string]float64{"b": 2} }))
	s := reg.Gather(context.Background())
	if s.Values["a"] != 1 || s.Values["b"] != 2 {
		t.Fatalf("merged values wrong: %+v", s.Values)
	}
}

// A collector with a long Interval is not re-collected on the next Gather; the
// cached value is reused (proves cadence + cache for expensive collectors).
func TestRegistryHonorsInterval(t *testing.T) {
	calls := 0
	reg := NewRegistry()
	reg.Register(NewFuncCollector(time.Hour,
		[]MetricDescriptor{{Key: "slow"}},
		func(context.Context) map[string]float64 { calls++; return map[string]float64{"slow": float64(calls)} }))
	first := reg.Gather(context.Background())
	second := reg.Gather(context.Background())
	if calls != 1 {
		t.Fatalf("slow collector ran %d times, want 1 (cadence not honored)", calls)
	}
	if first.Values["slow"] != 1 || second.Values["slow"] != 1 {
		t.Fatalf("cached value not reused: %v %v", first.Values["slow"], second.Values["slow"])
	}
}

// Latest returns the last merged snapshot without re-collecting.
func TestRegistryLatest(t *testing.T) {
	calls := 0
	reg := NewRegistry()
	reg.Register(NewFuncCollector(0, nil,
		func(context.Context) map[string]float64 { calls++; return map[string]float64{"k": float64(calls)} }))
	reg.Gather(context.Background())
	before := calls
	lat := reg.Latest()
	if calls != before {
		t.Fatal("Latest must not trigger a collection")
	}
	if lat.Values["k"] != float64(before) {
		t.Fatalf("Latest value mismatch: %v", lat.Values["k"])
	}
}

func TestCatalogSortedAndDeduped(t *testing.T) {
	reg := NewRegistry()
	reg.Register(NewFuncCollector(0, []MetricDescriptor{{Key: "z", Category: "B"}, {Key: "z", Category: "B"}}, nil))
	reg.Register(NewFuncCollector(0, []MetricDescriptor{{Key: "a", Category: "A"}}, nil))
	cat := reg.Catalog()
	if len(cat) != 2 {
		t.Fatalf("want 2 deduped descriptors, got %d", len(cat))
	}
	if cat[0].Category != "A" || cat[1].Category != "B" {
		t.Fatalf("catalog not sorted by category: %+v", cat)
	}
}

func TestMetricHistoryBounded(t *testing.T) {
	h := NewMetricHistory(3)
	for i := 1; i <= 5; i++ {
		h.Push(Snapshot{T: int64(i), Values: map[string]float64{"k": float64(i)}})
	}
	s := h.Series("k")
	if len(s) != 3 {
		t.Fatalf("want capped at 3, got %d", len(s))
	}
	if s[0].V != 3 || s[2].V != 5 {
		t.Fatalf("want last 3 points [3,4,5], got %+v", s)
	}
	if len(h.Series("missing")) != 0 {
		t.Fatal("unknown key should return empty series")
	}
}
