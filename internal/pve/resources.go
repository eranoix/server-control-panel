package pve

import (
	"context"
	"fmt"
	"net/http"
)

// Resource is one row of /cluster/resources — the source of truth for the
// inventory.
//
// # The scope changed, and the reason is measured
//
// Until the third wave this type carried only identity and state, with the rationale
// that "counters are telemetry, and whoever wants a time series has Prometheus".
// The sentence is still true about a SERIES. It was wrong about STATE:
// how much RAM a guest uses RIGHT NOW is state, and it was that state the panel's
// screen did not have. Measured against the home hypervisor: the guest
// `dev` (qemu/208) was at 7.19 GB of 8.59 GB of RAM — 83.7% — and no
// panel screen showed it. The data came in every response and was discarded on
// every tick.
//
// The limit stays where it was: what enters here is the CURRENT VALUE of each counter,
// never a history. A chart over time still belongs to Grafana.
//
// 🔴 What does NOT come here: IP address, tags, hostname, mac, onboot, description.
// The address comes from GuestAddress (guestconfig.go), in a second call.
type Resource struct {
	ID     string `json:"id"`     // "lxc/207" — stable key, it is what the hypervisor uses
	VMID   int    `json:"vmid"`   // 207
	Name   string `json:"name"`   // "apps"
	Node   string `json:"node"`   // "pve"
	Type   string `json:"type"`   // "lxc" | "qemu"
	Status string `json:"status"` // "running" | "stopped"
	Uptime int64  `json:"uptime"` // seconds; 0 on a stopped guest

	// CPU is a FRACTION from 0 to 1, already normalised by the guest's core count.
	// That was measured: `games` with maxcpu=8 returned 0.0764, which is the 7.6%
	// the Proxmox UI shows, not 61%. Multiplying by MaxCPU here would produce a
	// number that matches no other screen in the world.
	CPU    float64 `json:"cpu"`
	MaxCPU int     `json:"maxcpu"` // cores the guest sees

	Mem    int64 `json:"mem"`    // bytes in use INSIDE the guest
	MaxMem int64 `json:"maxmem"` // bytes configurados
	// MemHost is the RAM the HOST spends on this guest. Measured: it is only
	// non-zero on QEMU (`dev`, 7.82 GB of host against 7.19 GB of guest); on LXC
	// it comes back 0 because the concept does not apply — and 0 there is absence
	// of measurement, not zero consumption. Whoever publishes to the screen
	// settles that distinction.
	MemHost int64 `json:"memhost"`

	// 🔴 Disk comes back 0 on QEMU without guest-agent — measured on BOTH QEMU
	// guests of this house. Zero here is "I don't know", never "empty disk", and
	// whoever publishes to the screen is obliged to tell the two apart.
	Disk    int64 `json:"disk"`
	MaxDisk int64 `json:"maxdisk"`

	// Counters accumulated since the guest booted. They only become a rate with
	// TWO points and the interval between them — and the division must not cross a
	// hole in observation. See taxaEntreObservacoes, in the inventory.
	NetIn     int64 `json:"netin"`
	NetOut    int64 `json:"netout"`
	DiskRead  int64 `json:"diskread"`
	DiskWrite int64 `json:"diskwrite"`

	// Template is 1 when the entry is a template, not a guest that can be powered
	// on.
	Template int `json:"template"`
}

// IsGuest says whether the entry is a VM or a container. /cluster/resources
// mixes guests with node, storage and network in the SAME array — ignoring the
// other types is the parser's duty, not a courtesy.
func (r Resource) IsGuest() bool { return r.Type == "qemu" || r.Type == "lxc" }

// ClusterResources returns the SET of guests this token can see.
//
// 🔴 The list is filtered by permission SILENTLY by the hypervisor
// (PVE::RPCEnvironment applies VM.Audit per guest before answering): a guest
// with no ACL simply does not appear, with a 200 and no warning at all. That is
// why the discovery token needs BROAD audit rights (/vms with propagate=1),
// which it was granted. A guest missing from the response is an ACL problem,
// never a bug in this parser — and it is also the reason the assertion is on
// the set: counting rows would turn a new guest into a failure and a vanished
// guest into a pass.
//
// The order is NOT promised (the hypervisor does not guarantee it); whoever
// needs order sorts.
func (c *Client) ClusterResources(ctx context.Context) ([]Resource, error) {
	// type=vm saves bandwidth by asking the hypervisor to filter up front. It is
	// not the defence: the root's view brings storage and network anyway, and the
	// filter below is what guarantees the contract.
	var todos []Resource
	if err := c.do(ctx, http.MethodGet, "/api2/json/cluster/resources?type=vm", &todos); err != nil {
		return nil, err
	}
	guests := make([]Resource, 0, len(todos))
	for _, r := range todos {
		if r.IsGuest() {
			guests = append(guests, r)
		}
	}
	return guests, nil
}

// guestPath builds a guest's path prefix, validating the type. "lxc" and "qemu"
// are the two the hypervisor knows; any other value would build a path that
// does not exist and spend a call to take a 501 — failing here is more honest.
func guestPath(node string, vmid int, typ string) (string, error) {
	if typ != "lxc" && typ != "qemu" {
		return "", fmt.Errorf("pve: guest type %q (only lxc or qemu)", typ)
	}
	if node == "" {
		return "", fmt.Errorf("pve: empty node for guest %d", vmid)
	}
	if vmid <= 0 {
		return "", fmt.Errorf("pve: invalid vmid (%d)", vmid)
	}
	return fmt.Sprintf("/api2/json/nodes/%s/%s/%d", node, typ, vmid), nil
}
