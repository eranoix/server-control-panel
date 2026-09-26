package inventory

// hypervisor.go — the health of the Proxmox HOST, as a document of its OWN.
//
// # Why it is not a field of Node (the whole decision of this file)
//
// observedAtDoNo (freshness.go) resolves the age of a node by looking at only
// two timestamps: Status and Uptime. That is deliberate — those are the two the
// discovery refreshes on every tick.
//
// Hanging RAM, load and version inside the `node/pve` Node would produce a
// screen that contradicts itself: the discovery would stamp Status every 30 s
// and the node's age would show "data from 4 s ago", even with the hypervisor's
// /status mute for half an hour. Two truths about freshness on the same card.
// The alternative — teaching observedAtDoNo to look at the new fields — would
// drag the age of EVERY node to the most recent timestamp among things that are
// not observed together.
//
// Hence: a separate document, a timestamp of its own, a view that is the sister
// of NodeView. And freshness.go is NOT touched — an empty `git diff` on it is an
// acceptance criterion of the plan that created this file.
//
// # The scope cut
//
// Identity and state, never a time series: whoever wants a RAM chart has the
// embedded Grafana. What fits here is what answers "how is the hypervisor NOW,
// and how long ago did I learn that".

import (
	"strconv"
	"time"

	"server-control-panel/internal/pve"
)

// Hypervisor is the health of the Proxmox host at the instant it was observed.
//
// 🔴 Every measurement field is Observed[T]. Node is the declared exception: it
// is IDENTITY (the name the discovery returned), not a measurement — a name does
// not age. TestTodoCampoDoHipervisorTemCarimbo fails any raw field added later.
type Hypervisor struct {
	Node string `json:"node"` // discovered name ("pve"); never written by hand

	Version   Observed[string]     `json:"version"`    // "pve-manager/9.2.2/…"
	Uptime    Observed[int64]      `json:"uptime"`     // segundos
	Load      Observed[[3]float64] `json:"load"`       // 1, 5 e 15 minutos
	MemTotal  Observed[int64]      `json:"mem_total"`  // bytes
	MemUsed   Observed[int64]      `json:"mem_used"`   // bytes
	SwapTotal Observed[int64]      `json:"swap_total"` // bytes
	SwapUsed  Observed[int64]      `json:"swap_used"`  // bytes
	RootTotal Observed[int64]      `json:"root_total"` // bytes — the NODE's rootfs, not the pool
	RootUsed  Observed[int64]      `json:"root_used"`  // bytes
	KSMShared Observed[int64]      `json:"ksm_shared"`

	// Parity with the Proxmox Summary panel. CPU and Wait are fractions 0..1, the
	// way the hypervisor returns them — converting to a percentage here would
	// create two conventions in the same struct.
	CPU      Observed[float64] `json:"cpu"`
	Wait     Observed[float64] `json:"wait"` // IO delay
	Kernel   Observed[string]  `json:"kernel"`
	CPUModel Observed[string]  `json:"cpu_model"`
	CPUCores Observed[int]     `json:"cpu_cores"` // bytes shared between guests

	// 🔴 The THREE below are the hypervisor's, but are NOT part of its health: each
	// one comes out of a call of its own (/storage, /disks/zfs,
	// /access/permissions) that fails on its own. That is why they have views of
	// their own (ViewStorage/ViewZPools) and stay out of observedAtDoHipervisor —
	// see storage.go, and the test TestCarimboDaSaudeClassificaTodoCampo, which
	// forces every new field of this document to be classified.
	Storage        Observed[[]StoragePool] `json:"storage"`
	ZPools         Observed[[]ZPool]       `json:"zpools"`
	DatastoreAudit Observed[bool]          `json:"datastore_audit"`
}

// HypervisorView is the Hypervisor as the API delivers it: with the age ALREADY
// resolved on the server. Sister of NodeView, and for the same reason — the
// browser's clock is not a controlled variable. Neither tag has `omitempty`: an
// age missing from the payload is the ageless screen back again.
type HypervisorView struct {
	Hypervisor
	AgeSeconds int64 `json:"age_seconds"`
	Stale      bool  `json:"stale"`
}

// observedAtDoHipervisor is the most RECENT timestamp of the node's HEALTH: if
// any measurement from /nodes/{n}/status was refreshed, the panel heard the
// hypervisor at that instant. Zero = never heard. Sister of observedAtDoNo —
// and separated from it on purpose.
//
// 🔴 Storage, ZPools and DatastoreAudit stay OUT of this list, and that is the
// decision, not an oversight. They come out of three other calls, which fail on
// their own; including them would make a /status that answers rejuvenate a
// capacity block that has been mute for half an hour — the same trap again, one
// layer up.
func observedAtDoHipervisor(h Hypervisor) int64 {
	maisNovo := int64(0)
	for _, c := range []int64{
		h.Version.ObservedAt, h.Uptime.ObservedAt, h.Load.ObservedAt,
		h.MemTotal.ObservedAt, h.MemUsed.ObservedAt,
		h.SwapTotal.ObservedAt, h.SwapUsed.ObservedAt,
		h.RootTotal.ObservedAt, h.RootUsed.ObservedAt, h.KSMShared.ObservedAt,
		// The five from the parity with the Proxmox Summary. They all come from the
		// SAME /nodes/{n}/status call as the ones above, so leaving them out would make
		// the health timestamp ignore half of what it had itself just observed — it was
		// TestCarimboDaSaudeClassificaTodoCampo that caught this, and that is exactly
		// what it exists for.
		h.CPU.ObservedAt, h.Wait.ObservedAt, h.Kernel.ObservedAt,
		h.CPUModel.ObservedAt, h.CPUCores.ObservedAt,
	} {
		if c > maisNovo {
			maisNovo = c
		}
	}
	return maisNovo
}

// ViewHypervisor resolves age and expiry on the SERVER, at the instant of
// serialization. It reuses AgeSeconds and Stale: -1 still means "never
// observed", and never 0 — "0 s ago" reads as just-seen.
func ViewHypervisor(h Hypervisor, ttl time.Duration, now time.Time) HypervisorView {
	carimbo := observedAtDoHipervisor(h)
	return HypervisorView{
		Hypervisor: h,
		AgeSeconds: AgeSeconds(carimbo, now),
		Stale:      Stale(carimbo, ttl, now),
	}
}

// aplicaHipervisor is the upsert of the health. It is only called when /status
// ANSWERED: a failure keeps the whole document as it was, with the old
// timestamp, so the screen shows the age growing instead of amnesia.
func aplicaHipervisor(inv *Inventory, nome string, st pve.NodeStatus, agora int64) {
	h := inv.Hypervisor
	if nome != "" {
		h.Node = nome
	}
	h.Version = Observe(st.PVEVersion, agora)
	h.Uptime = Observe(st.Uptime, agora)
	h.Load = Observe(loadDeStrings(st.LoadAvg), agora)
	h.MemTotal = Observe(st.Memory.Total, agora)
	h.MemUsed = Observe(st.Memory.Used, agora)
	h.SwapTotal = Observe(st.Swap.Total, agora)
	h.SwapUsed = Observe(st.Swap.Used, agora)
	h.RootTotal = Observe(st.RootFS.Total, agora)
	h.RootUsed = Observe(st.RootFS.Used, agora)
	h.KSMShared = Observe(st.KSM.Shared, agora)
	// Parity with the Proxmox Summary panel.
	//
	// 🔴 Wait is the IO DELAY, and it is the metric that separates "the machine is
	// busy" from "the machine is waiting on disk". On a server whose pool is a
	// single disk, that distinction is the beginning of every diagnosis — and it
	// did not exist on the screen.
	h.CPU = Observe(st.CPU, agora)
	h.Wait = Observe(st.Wait, agora)
	h.Kernel = Observe(st.KVersion, agora)
	h.CPUModel = Observe(st.CPUInfo.Model, agora)
	h.CPUCores = Observe(st.CPUInfo.Cpus, agora)
	inv.Hypervisor = h
}

// loadDeStrings converts the three windows the hypervisor sends as text
// ("1.14"). An unreadable value becomes 0 and does NOT take the rest down: a
// strange loadavg cannot cost the whole screen its RAM, its version and its
// uptime.
func loadDeStrings(in []string) [3]float64 {
	var out [3]float64
	for i := 0; i < 3 && i < len(in); i++ {
		if f, err := strconv.ParseFloat(in[i], 64); err == nil {
			out[i] = f
		}
	}
	return out
}

// nomeDoHipervisor derives the host name from what the discovery returned — no
// hostname lives in this package (invariant 3 of the poller). If there is more
// than one, the smallest by string order wins: the tick has to write the same
// document twice in a row, otherwise the file diff turns into noise.
func nomeDoHipervisor(recursos []pve.Resource) string {
	nome := ""
	for _, r := range recursos {
		if r.Node == "" {
			continue
		}
		if nome == "" || r.Node < nome {
			nome = r.Node
		}
	}
	return nome
}
