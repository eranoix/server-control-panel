package inventory

// storage.go — storage capacity and zpool state, with a timestamp of their OWN.
//
// ─────────────────────────────────────────────────────────────────────────────
// 🔴 WHY THE TIMESTAMP IS THEIR OWN, AND NOT THE HEALTH'S
//
// hypervisor.go already explained why the host's health could not be a field of
// Node: observedAtDoNo merges timestamps of things that are not observed
// together, and the result is a card that contradicts itself. Here the argument
// repeats one layer up, with three calls instead of two:
//
//	/nodes/{n}/status      → health   (92 ms)
//	/nodes/{n}/storage     → capacity (168 ms)
//	/nodes/{n}/disks/zfs   → zpool   (96 ms)
//
// They come out of the SAME tick, but they fail SEPARATELY. If the storage
// timestamp went into observedAtDoHipervisor, a /status that answers would keep
// the "live" badge up while capacity ages in silence — and old capacity shown
// as live is exactly what makes a pool fill up with nobody seeing it.
//
// Hence: fields in the SAME document (they are the hypervisor's), SEPARATE
// views (ViewStorage/ViewZPools) and observedAtDoHipervisor left in peace.
// TestCarimboDaSaudeClassificaTodoCampo forces every new field to be classified.
//
// 🔴 AND WHY THE PRIVILEGE VERDICT LIVES IN HERE
//
// /nodes/{n}/storage returns 200 with [] when the token has no privilege —
// measured. "No storage" and "no permission to see storage" are the same answer
// on the wire and OPPOSITE readings on the screen. The tie-breaker is the
// verdict from /access/permissions, and it travels in the same document, with a
// timestamp of its own, because a screen assembled from two routes that age at
// different rates is a screen that shows half a truth without saying which half.
// ─────────────────────────────────────────────────────────────────────────────

import (
	"sort"
	"time"

	"server-control-panel/internal/pve"
)

// StoragePool is a storage of the hypervisor, ALREADY NORMALIZED for the
// screen: `content` became a list, the hypervisor's 0|1 became booleans and the
// fraction became a percentage. The screen formats; it does not interpret (the
// same rule as the loadavg in hypervisor.go).
type StoragePool struct {
	ID            string   `json:"id"`      // "local-zfs"
	Type          string   `json:"type"`    // "zfspool" | "dir" | "pbs"
	Content       []string `json:"content"` // ["images","rootdir"], ordenado
	Total         int64    `json:"total"`   // bytes
	Used          int64    `json:"used"`    // bytes
	Avail         int64    `json:"avail"`   // bytes
	UsedPct       float64  `json:"used_pct"`
	Ativo         bool     `json:"ativo"`
	Habilitado    bool     `json:"habilitado"`
	Compartilhado bool     `json:"compartilhado"`
}

// ZPool is a ZFS pool of the hypervisor. Saudavel is derived ONCE, here, so that
// no screen needs to know the list of ZFS states by heart.
type ZPool struct {
	Name     string  `json:"name"`
	Health   string  `json:"health"` // literal do ZFS: ONLINE | DEGRADED | FAULTED | …
	Saudavel bool    `json:"saudavel"`
	Size     int64   `json:"size"`
	Alloc    int64   `json:"alloc"`
	Free     int64   `json:"free"`
	FragPct  int     `json:"frag_pct"`
	Dedup    float64 `json:"dedup"`
}

// StorageView is the capacity block as the API delivers it: pools, privilege
// verdict and the age ALREADY resolved on the server — sister of
// HypervisorView, and for the same reason (the browser's clock is not a
// controlled variable).
type StorageView struct {
	Pools []StoragePool `json:"pools"`
	// DatastoreAudit travels with a timestamp, and not as a bare bool, because
	// "I never asked" and "I asked and the answer is no" are different states:
	// the first does not authorize the screen to accuse a missing permission.
	DatastoreAudit Observed[bool] `json:"datastore_audit"`
	AgeSeconds     int64          `json:"age_seconds"`
	Stale          bool           `json:"stale"`
}

// ZPoolsView is the zpool block, with an age of its own.
type ZPoolsView struct {
	Pools      []ZPool `json:"pools"`
	AgeSeconds int64   `json:"age_seconds"`
	Stale      bool    `json:"stale"`
}

// ViewStorage resolves the age and expiry of the capacity block.
//
// The age comes from the STORAGE timestamp, never from the health's — see the
// file header.
func ViewStorage(h Hypervisor, ttl time.Duration, now time.Time) StorageView {
	carimbo := h.Storage.ObservedAt
	pools := h.Storage.Value
	if pools == nil {
		// [] and not nil: `null` in the JSON would force the screen to check the type
		// before iterating, and the screen is the place where that check goes missing.
		pools = []StoragePool{}
	}
	return StorageView{
		Pools:          pools,
		DatastoreAudit: h.DatastoreAudit,
		AgeSeconds:     AgeSeconds(carimbo, now),
		Stale:          Stale(carimbo, ttl, now),
	}
}

// ViewZPools resolves the age and expiry of the zpool block.
func ViewZPools(h Hypervisor, ttl time.Duration, now time.Time) ZPoolsView {
	carimbo := h.ZPools.ObservedAt
	pools := h.ZPools.Value
	if pools == nil {
		pools = []ZPool{}
	}
	return ZPoolsView{
		Pools:      pools,
		AgeSeconds: AgeSeconds(carimbo, now),
		Stale:      Stale(carimbo, ttl, now),
	}
}

// aplicaStorage is the upsert of the capacity. It is only called when the route
// ANSWERED: a failure keeps the old value and the old timestamp (invariant 2 of
// the poller).
func aplicaStorage(inv *Inventory, ss []pve.Storage, agora int64) {
	out := make([]StoragePool, 0, len(ss))
	for _, s := range ss {
		out = append(out, StoragePool{
			ID:            s.Storage,
			Type:          s.Type,
			Content:       s.Conteudos(),
			Total:         s.Total,
			Used:          s.Used,
			Avail:         s.Avail,
			UsedPct:       pctDeUso(s.UsedFraction, s.Used, s.Total),
			Ativo:         s.Ativo(),
			Habilitado:    s.Habilitado(),
			Compartilhado: s.Compartilhado(),
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	inv.Hypervisor.Storage = Observe(out, agora)
}

// aplicaZPools is the upsert of the state of the ZFS pools.
func aplicaZPools(inv *Inventory, ps []pve.ZPool, agora int64) {
	out := make([]ZPool, 0, len(ps))
	for _, p := range ps {
		out = append(out, ZPool{
			Name:     p.Name,
			Health:   p.Health,
			Saudavel: p.Saudavel(),
			Size:     p.Size,
			Alloc:    p.Alloc,
			Free:     p.Free,
			FragPct:  p.Frag,
			Dedup:    p.Dedup,
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	inv.Hypervisor.ZPools = Observe(out, agora)
}

// aplicaDatastoreAudit records the privilege verdict WITH a timestamp.
func aplicaDatastoreAudit(inv *Inventory, pode bool, agora int64) {
	inv.Hypervisor.DatastoreAudit = Observe(pode, agora)
}

// pctDeUso returns the usage percentage of a storage.
//
// 🔴 The fraction comes READY from the hypervisor and it is the one that rules:
// for a storage of type `pbs`, `used/total` and `used_fraction` are not the same
// sum, and disagreeing with the source would be inventing a number.
//
// The degradation exists for the day an upgrade of the hypervisor stops sending
// `used_fraction`: without it, a FULL disk would show up at 0% — an empty bar,
// green, and the operator with no reason to look. Zero is never the safe output.
func pctDeUso(fracao float64, usado, total int64) float64 {
	if fracao > 0 {
		return fracao * 100
	}
	if total <= 0 || usado <= 0 {
		return 0
	}
	return float64(usado) / float64(total) * 100
}
