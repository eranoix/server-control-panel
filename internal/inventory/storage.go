package inventory

import (
	"sort"
	"time"

	"server-control-panel/internal/pve"
)

type StoragePool struct {
	ID        string   `json:"id"`
	Type      string   `json:"type"`
	Content   []string `json:"content"`
	Total     int64    `json:"total"`
	Used      int64    `json:"used"`
	Avail     int64    `json:"avail"`
	UsedPct   float64  `json:"used_pct"`
	IsActive  bool     `json:"active"`
	IsEnabled bool     `json:"enabled"`
	IsShared  bool     `json:"shared"`
}

type ZPool struct {
	Name    string  `json:"name"`
	Health  string  `json:"health"`
	Healthy bool    `json:"healthy"`
	Size    int64   `json:"size"`
	Alloc   int64   `json:"alloc"`
	Free    int64   `json:"free"`
	FragPct int     `json:"frag_pct"`
	Dedup   float64 `json:"dedup"`
}

type StorageView struct {
	Pools          []StoragePool  `json:"pools"`
	DatastoreAudit Observed[bool] `json:"datastore_audit"`
	AgeSeconds     int64          `json:"age_seconds"`
	Stale          bool           `json:"stale"`
}

type ZPoolsView struct {
	Pools      []ZPool `json:"pools"`
	AgeSeconds int64   `json:"age_seconds"`
	Stale      bool    `json:"stale"`
}

func ViewStorage(h Hypervisor, ttl time.Duration, now time.Time) StorageView {
	stamp := h.Storage.ObservedAt
	pools := h.Storage.Value
	if pools == nil {
		pools = []StoragePool{}
	}
	return StorageView{
		Pools:          pools,
		DatastoreAudit: h.DatastoreAudit,
		AgeSeconds:     AgeSeconds(stamp, now),
		Stale:          Stale(stamp, ttl, now),
	}
}

func ViewZPools(h Hypervisor, ttl time.Duration, now time.Time) ZPoolsView {
	stamp := h.ZPools.ObservedAt
	pools := h.ZPools.Value
	if pools == nil {
		pools = []ZPool{}
	}
	return ZPoolsView{
		Pools:      pools,
		AgeSeconds: AgeSeconds(stamp, now),
		Stale:      Stale(stamp, ttl, now),
	}
}

func applyStorage(inv *Inventory, ss []pve.Storage, now int64) {
	out := make([]StoragePool, 0, len(ss))
	for _, s := range ss {
		out = append(out, StoragePool{
			ID:        s.Storage,
			Type:      s.Type,
			Content:   s.ContentList(),
			Total:     s.Total,
			Used:      s.Used,
			Avail:     s.Avail,
			UsedPct:   usagePct(s.UsedFraction, s.Used, s.Total),
			IsActive:  s.IsActive(),
			IsEnabled: s.IsEnabled(),
			IsShared:  s.IsShared(),
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	inv.Hypervisor.Storage = Observe(out, now)
}

func applyZPools(inv *Inventory, ps []pve.ZPool, now int64) {
	out := make([]ZPool, 0, len(ps))
	for _, p := range ps {
		out = append(out, ZPool{
			Name:    p.Name,
			Health:  p.Health,
			Healthy: p.Healthy(),
			Size:    p.Size,
			Alloc:   p.Alloc,
			Free:    p.Free,
			FragPct: p.Frag,
			Dedup:   p.Dedup,
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	inv.Hypervisor.ZPools = Observe(out, now)
}

func applyDatastoreAudit(inv *Inventory, can bool, now int64) {
	inv.Hypervisor.DatastoreAudit = Observe(can, now)
}

func usagePct(fraction float64, used, total int64) float64 {
	if fraction > 0 {
		return fraction * 100
	}
	if total <= 0 || used <= 0 {
		return 0
	}
	return float64(used) / float64(total) * 100
}
