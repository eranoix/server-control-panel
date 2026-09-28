package inventory

import (
	"strconv"
	"time"

	"server-control-panel/internal/pve"
)

type Hypervisor struct {
	Node string `json:"node"`

	Version   Observed[string]     `json:"version"`
	Uptime    Observed[int64]      `json:"uptime"`
	Load      Observed[[3]float64] `json:"load"`
	MemTotal  Observed[int64]      `json:"mem_total"`
	MemUsed   Observed[int64]      `json:"mem_used"`
	SwapTotal Observed[int64]      `json:"swap_total"`
	SwapUsed  Observed[int64]      `json:"swap_used"`
	RootTotal Observed[int64]      `json:"root_total"`
	RootUsed  Observed[int64]      `json:"root_used"`
	KSMShared Observed[int64]      `json:"ksm_shared"`

	CPU      Observed[float64] `json:"cpu"`
	Wait     Observed[float64] `json:"wait"`
	Kernel   Observed[string]  `json:"kernel"`
	CPUModel Observed[string]  `json:"cpu_model"`
	CPUCores Observed[int]     `json:"cpu_cores"`

	Storage        Observed[[]StoragePool] `json:"storage"`
	ZPools         Observed[[]ZPool]       `json:"zpools"`
	DatastoreAudit Observed[bool]          `json:"datastore_audit"`
}

type HypervisorView struct {
	Hypervisor
	AgeSeconds int64 `json:"age_seconds"`
	Stale      bool  `json:"stale"`
}

func hypervisorObservedAt(h Hypervisor) int64 {
	newest := int64(0)
	for _, c := range []int64{
		h.Version.ObservedAt, h.Uptime.ObservedAt, h.Load.ObservedAt,
		h.MemTotal.ObservedAt, h.MemUsed.ObservedAt,
		h.SwapTotal.ObservedAt, h.SwapUsed.ObservedAt,
		h.RootTotal.ObservedAt, h.RootUsed.ObservedAt, h.KSMShared.ObservedAt,
		h.CPU.ObservedAt, h.Wait.ObservedAt, h.Kernel.ObservedAt,
		h.CPUModel.ObservedAt, h.CPUCores.ObservedAt,
	} {
		if c > newest {
			newest = c
		}
	}
	return newest
}

func ViewHypervisor(h Hypervisor, ttl time.Duration, now time.Time) HypervisorView {
	stamp := hypervisorObservedAt(h)
	return HypervisorView{
		Hypervisor: h,
		AgeSeconds: AgeSeconds(stamp, now),
		Stale:      Stale(stamp, ttl, now),
	}
}

func applyHypervisor(inv *Inventory, name string, st pve.NodeStatus, now int64) {
	h := inv.Hypervisor
	if name != "" {
		h.Node = name
	}
	h.Version = Observe(st.PVEVersion, now)
	h.Uptime = Observe(st.Uptime, now)
	h.Load = Observe(loadFromStrings(st.LoadAvg), now)
	h.MemTotal = Observe(st.Memory.Total, now)
	h.MemUsed = Observe(st.Memory.Used, now)
	h.SwapTotal = Observe(st.Swap.Total, now)
	h.SwapUsed = Observe(st.Swap.Used, now)
	h.RootTotal = Observe(st.RootFS.Total, now)
	h.RootUsed = Observe(st.RootFS.Used, now)
	h.KSMShared = Observe(st.KSM.Shared, now)
	h.CPU = Observe(st.CPU, now)
	h.Wait = Observe(st.Wait, now)
	h.Kernel = Observe(st.KVersion, now)
	h.CPUModel = Observe(st.CPUInfo.Model, now)
	h.CPUCores = Observe(st.CPUInfo.Cpus, now)
	inv.Hypervisor = h
}

func loadFromStrings(in []string) [3]float64 {
	var out [3]float64
	for i := 0; i < 3 && i < len(in); i++ {
		if f, err := strconv.ParseFloat(in[i], 64); err == nil {
			out[i] = f
		}
	}
	return out
}

func hypervisorName(resources []pve.Resource) string {
	name := ""
	for _, r := range resources {
		if r.Node == "" {
			continue
		}
		if name == "" || r.Node < name {
			name = r.Node
		}
	}
	return name
}
