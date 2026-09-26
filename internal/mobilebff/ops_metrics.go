package mobilebff

// ops_metrics.go — the "server resources" half of GET /ops/status
// (CPU, memory, swap, disk, uptime, clock and network).
//
// WHY HERE, INSIDE /ops/status, AND NOT ON A NEW ROUTE
// The home screen's dashboard needs, in a single paint, health + queue +
// alerts + resources. Creating /ops/metrics alongside it would mean: a second
// identical admin gate, a second WS channel for the live version (or a
// dashboard half of whose numbers freeze after the first paint), and two
// sources of "operational state" that drift apart over time — exactly the
// surface divergence this BFF exists to prevent (see the header of
// mobilebff.go). OpsStatus already is, by definition, "an aggregated,
// screen-shaped snapshot of operational state, admin-only"; machine resources
// are operational state. So it grows one field, and the shape stays ONE —
// served over HTTP and published on the "ops.health" channel by the same
// builder (events_bridge_ops.go).
//
// WHERE THE NUMBERS COME FROM (no new collector)
// Everything comes out of deps.SysStats, which internal/api wires to the SAME
// collectStatsCached that serves the web panel's GET /api/stats — which in
// turn calls internal/system.Collect (gopsutil), the same collection that
// feeds the alert engine's systemCollector (internal/api/metrics_collectors.go).
// Not a byte of /proc is read by this file.
//
// COST
// system.Collect is expensive: it blocks ~200ms sampling CPU and sweeps ALL of
// /proc. That is why the injected dependency is NOT system.Collect but the
// panel's cached wrapper: 3s TTL + singleflight under a mutex. Consequences:
//   - an app polling /ops/status every 2s pays for ONE collection every 3s;
//   - if the web panel is open (5s poll), the app pays nothing at all — the
//     two share the SAME cache, not two competing caches;
//   - the 12s tick of the "ops.health" channel only collects when somebody is
//     subscribed (a guard that already existed in StartOpsHealthPublisher).
// A second cache was deliberately NOT created here: two TTLs over the same
// collection would only produce different staleness windows for the same
// number, without saving a single read of /proc.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"server-control-panel/internal/system"
)

// SystemMetrics is the screen-shaped projection of internal/system.Stats —
// only what a phone dashboard draws in the first few seconds. Stats's heavy
// fields (per-core percentage, top processes, Go runtime) are left out on
// purpose: none of them fits in a phone card, and all of them would also
// fatten every tick of the "ops.health" channel.
//
// Value/text pair convention: every number a human reads comes in TWO fields —
// the raw one (for the client to draw a bar/chart and compare) and the
// already-formatted `*_text` one (so the client can paint without
// reimplementing byte formatting in Kotlin). The same convention as
// formatDockerBytes/formatSecurityBytes in internal/mobilebff/screens.
type SystemMetrics struct {
	Hostname string `json:"hostname,omitempty"`
	Platform string `json:"platform,omitempty"`
	// ServerTime/ServerTimeEpoch are the instant THIS response was assembled
	// (not the sample's, which may be up to 3s behind because of the cache
	// described in the header). It is the "server clock" the app shows and uses
	// to detect a skewed clock.
	ServerTime      string `json:"server_time"`
	ServerTimeEpoch int64  `json:"server_time_epoch"`
	// UptimeSeconds is the raw value; UptimeText is the same value as "18d 5h 3m".
	UptimeSeconds uint64        `json:"uptime_seconds"`
	UptimeText    string        `json:"uptime_text"`
	CPU           CPUMetrics    `json:"cpu"`
	Memory        MemoryMetrics `json:"memory"`
	// Swap always comes through; a host without swap returns total=0, which is
	// the honest answer ("it exists and is worth zero"), not the field's absence.
	Swap  MemoryMetrics `json:"swap"`
	Disks []DiskMetrics `json:"disks"`
	// Net is omitted when no uplink interface was identified — see pickUplink.
	// Absent is honest; zeroed would be a lie.
	Net *NetMetrics `json:"net,omitempty"`
}

// CPUMetrics mirrors the names of internal/system.CPUInfo (used_percent is its
// `overall`, renamed to match memory's and disk's used_percent — within ONE
// response, "percentage in use" has to have a single name).
type CPUMetrics struct {
	Model       string  `json:"model,omitempty"`
	Cores       int     `json:"cores"`
	UsedPercent float64 `json:"used_percent"`
	Load1       float64 `json:"load1"`
	Load5       float64 `json:"load5"`
	Load15      float64 `json:"load15"`
	// Steal = % of CPU time stolen by the hypervisor; Iowait = % waiting on
	// disk. The same fields the web panel already exposes, the same names.
	Steal  float64 `json:"steal"`
	Iowait float64 `json:"iowait"`
}

// MemoryMetrics mirrors internal/system.MemInfo (bytes) and adds the formatted
// pairs. It serves both memory and swap.
type MemoryMetrics struct {
	Total       uint64  `json:"total"`
	Used        uint64  `json:"used"`
	Free        uint64  `json:"free"`
	UsedPercent float64 `json:"used_percent"`
	TotalText   string  `json:"total_text"`
	UsedText    string  `json:"used_text"`
}

// DiskMetrics mirrors internal/system.DiskInfo for the mounts that survive
// relevantDisks.
type DiskMetrics struct {
	Mount       string  `json:"mount"`
	FSType      string  `json:"fstype,omitempty"`
	Total       uint64  `json:"total"`
	Used        uint64  `json:"used"`
	Free        uint64  `json:"free"`
	UsedPercent float64 `json:"used_percent"`
	TotalText   string  `json:"total_text"`
	UsedText    string  `json:"used_text"`
}

// NetMetrics is the rate of ONE interface — the uplink one (see pickUplink) —
// never the sum of all of them.
//
// Why not the sum: `sys.net.sent_rate` in the metrics catalogue
// (internal/api/metrics_collectors.go) sums ALL interfaces, including `lo`, the
// `br-*` bridges and Docker's `veth*`. On this host that counts the same
// container packet three times and the whole of loopback — a number good enough
// for a relative alert rule, and terrible for a card telling a human
// "↓ 12 MiB/s". Here the interface is chosen, named in the payload itself
// (Interface), and the rate is derived from the cumulative counters the
// collection already brought back — without a single extra read of the system.
type NetMetrics struct {
	Interface string `json:"interface"`
	BytesSent uint64 `json:"bytes_sent"`
	BytesRecv uint64 `json:"bytes_recv"`
	// SentRate/RecvRate (bytes/s) stay ABSENT until a second sample exists to
	// derive the rate from — a zero on the first call would be
	// indistinguishable from "network idle". The client draws "—" while they
	// are null, and the second call (or the next ops.health tick) fills them in.
	SentRate     *float64 `json:"sent_rate,omitempty"`
	RecvRate     *float64 `json:"recv_rate,omitempty"`
	SentRateText string   `json:"sent_rate_text,omitempty"`
	RecvRateText string   `json:"recv_rate_text,omitempty"`
}

// buildSystemMetrics shapes an already-collected *system.Stats. It returns nil
// when the dependency is not wired or the collection failed — the caller then
// omits the whole `system` field instead of publishing a block of zeros the app
// would paint as "CPU 0%, disk 0/0" (a lie indistinguishable from an idle
// server).
func buildSystemMetrics(ctx context.Context, deps Deps) *SystemMetrics {
	if deps.SysStats == nil {
		return nil
	}
	s, err := deps.SysStats(ctx)
	if err != nil || s == nil {
		return nil
	}
	now := time.Now().UTC()
	m := &SystemMetrics{
		Hostname:        s.Host.Hostname,
		Platform:        platformLabel(s.Host),
		ServerTime:      now.Format(time.RFC3339),
		ServerTimeEpoch: now.Unix(),
		UptimeSeconds:   s.Host.Uptime,
		UptimeText:      formatUptime(s.Host.Uptime),
		CPU: CPUMetrics{
			Model:       s.CPU.Model,
			Cores:       s.CPU.Cores,
			UsedPercent: s.CPU.Overall,
			Load1:       s.Load.Load1,
			Load5:       s.Load.Load5,
			Load15:      s.Load.Load15,
			Steal:       s.CPU.Steal,
			Iowait:      s.CPU.Iowait,
		},
		Memory: memoryMetrics(s.Memory),
		Swap:   memoryMetrics(s.Swap),
		Disks:  relevantDisks(s.Disks),
		Net:    netRates.sample(s.Net, now),
	}
	return m
}

func memoryMetrics(mi system.MemInfo) MemoryMetrics {
	return MemoryMetrics{
		Total:       mi.Total,
		Used:        mi.Used,
		Free:        mi.Free,
		UsedPercent: mi.UsedPercent,
		TotalText:   formatBytes(mi.Total),
		UsedText:    formatBytes(mi.Used),
	}
}

// ignoredDiskFSTypes are filesystems that ALWAYS show up 100% full by
// construction (read-only mounted images: each snap is a squashfs the exact size
// of its contents) or that do not represent persistent storage. Without this
// filter, this host would return 16 mounts of which 14 are snaps at 100% — a
// phone dashboard that opens shouting "disk full" fourteen times over.
var ignoredDiskFSTypes = map[string]bool{
	"squashfs":  true,
	"overlay":   true,
	"tmpfs":     true,
	"devtmpfs":  true,
	"ramfs":     true,
	"iso9660":   true,
	"autofs":    true,
	"fuse.snap": true,
}

// relevantDisks filters and orders the mounts a dashboard should show.
// Order: "/" first (it is the disk the VPS owner thinks of as "the disk"), then
// the rest from fullest to emptiest — whoever is about to fill up shows up
// before you have to scroll.
func relevantDisks(in []system.DiskInfo) []DiskMetrics {
	out := make([]DiskMetrics, 0, len(in))
	for _, d := range in {
		if d.Total == 0 || ignoredDiskFSTypes[d.FSType] || strings.HasPrefix(d.Mount, "/snap/") {
			continue
		}
		out = append(out, DiskMetrics{
			Mount:       d.Mount,
			FSType:      d.FSType,
			Total:       d.Total,
			Used:        d.Used,
			Free:        d.Free,
			UsedPercent: d.UsedPercent,
			TotalText:   formatBytes(d.Total),
			UsedText:    formatBytes(d.Used),
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if (out[i].Mount == "/") != (out[j].Mount == "/") {
			return out[i].Mount == "/"
		}
		return out[i].UsedPercent > out[j].UsedPercent
	})
	return out
}

// virtualIfacePrefixes are interfaces that do NOT represent the server's real
// bandwidth: loopback, Docker's bridges and veths (container traffic already
// goes through the physical one) and tunnels (tailscale/wireguard/tun/tap travel
// INSIDE the physical one — counting them would double the same byte).
var virtualIfacePrefixes = []string{
	"lo", "docker", "br-", "veth", "virbr", "tun", "tap", "wg", "tailscale",
	"cni", "flannel", "kube", "dummy", "gre", "sit", "bond-",
}

// pickUplink chooses the physical interface with the most cumulative traffic —
// on the VPS, the public NIC. It returns ok=false when there are only virtual
// interfaces (e.g. a CI container), and then the `net` field drops out entirely.
func pickUplink(ifaces []system.NetInfo) (system.NetInfo, bool) {
	var best system.NetInfo
	found := false
	for _, n := range ifaces {
		if isVirtualIface(n.Name) {
			continue
		}
		if !found || n.BytesSent+n.BytesRecv > best.BytesSent+best.BytesRecv {
			best, found = n, true
		}
	}
	return best, found
}

func isVirtualIface(name string) bool {
	for _, p := range virtualIfacePrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// netRateTracker derives bytes/s from the CUMULATIVE counters the collection
// already returned — it reads nothing itself. It keeps the previous sample
// because a rate requires two samples and the collection only hands over totals.
//
// Package state (like systemMetricsWindowByUser in screens/system.go): there is
// a single server per process and the series belongs to the host, not to the
// user. Tests that depend on a rate MUST use a tracker of their own (sample is a
// method precisely for that) rather than the singleton, or one test's sample
// leaks into another's assertion.
type netRateTracker struct {
	mu       sync.Mutex
	have     bool
	iface    string
	sent     uint64
	recv     uint64
	at       time.Time
	sentRate *float64
	recvRate *float64
}

var netRates = &netRateTracker{}

// sample records the current sample and returns the matching NetMetrics.
//
// Three guards, all because of the collection's 3s cache:
//  1. counters identical to the previous sample's ⇒ it is the SAME collection
//     being served again; recomputing would give a false rate of 0, so the
//     previous rates are repeated and the baseline does not move.
//  2. the interface changed or a counter went backwards (NIC/host restart) ⇒
//     rebaseline without publishing a rate.
//  3. first sample of the process ⇒ no rate (fields absent).
func (t *netRateTracker) sample(ifaces []system.NetInfo, now time.Time) *NetMetrics {
	up, ok := pickUplink(ifaces)
	if !ok {
		return nil
	}
	out := &NetMetrics{Interface: up.Name, BytesSent: up.BytesSent, BytesRecv: up.BytesRecv}

	t.mu.Lock()
	defer t.mu.Unlock()
	switch {
	case t.have && t.iface == up.Name && t.sent == up.BytesSent && t.recv == up.BytesRecv:
		// (1) the same collection served from the cache: repeat what we already
		// knew and do NOT touch the baseline — advancing `at` without advancing
		// the bytes would shrink the next real sample's dt and inflate the rate.
	case !t.have || t.iface != up.Name || up.BytesSent < t.sent || up.BytesRecv < t.recv:
		// (2)/(3) no usable baseline: record it and wait for the next one.
		t.sentRate, t.recvRate = nil, nil
		t.iface, t.sent, t.recv, t.at, t.have = up.Name, up.BytesSent, up.BytesRecv, now, true
	default:
		// Fresh pointers on every computation (never a write through the pointer
		// already handed out), so a response being serialized never sees the
		// value change underneath it.
		if dt := now.Sub(t.at).Seconds(); dt > 0 {
			sr := float64(up.BytesSent-t.sent) / dt
			rr := float64(up.BytesRecv-t.recv) / dt
			t.sentRate, t.recvRate = &sr, &rr
		}
		t.iface, t.sent, t.recv, t.at = up.Name, up.BytesSent, up.BytesRecv, now
	}
	out.SentRate, out.RecvRate = t.sentRate, t.recvRate
	if out.SentRate != nil {
		out.SentRateText = formatRate(*out.SentRate)
	}
	if out.RecvRate != nil {
		out.RecvRateText = formatRate(*out.RecvRate)
	}
	return out
}

// platformLabel joins platform and version into a header label
// ("ubuntu 24.04"), empty when the collection could not tell.
func platformLabel(h system.HostInfo) string {
	switch {
	case h.Platform == "":
		return ""
	case h.PlatformVer == "":
		return h.Platform
	default:
		return h.Platform + " " + h.PlatformVer
	}
}

// formatUptime renders seconds as "18d 5h 3m" — descending units, at most
// three, without seconds (an uptime of days does not need them) except when the
// server came up less than a minute ago.
func formatUptime(secs uint64) string {
	if secs < 60 {
		return fmt.Sprintf("%ds", secs)
	}
	d := secs / 86400
	h := (secs % 86400) / 3600
	m := (secs % 3600) / 60
	var parts []string
	if d > 0 {
		parts = append(parts, fmt.Sprintf("%dd", d))
	}
	if h > 0 {
		parts = append(parts, fmt.Sprintf("%dh", h))
	}
	if m > 0 {
		parts = append(parts, fmt.Sprintf("%dm", m))
	}
	return strings.Join(parts, " ")
}

// formatBytes renders bytes in binary units, the same output as
// formatDockerBytes/formatSecurityBytes (internal/mobilebff/screens) — the app
// must not see "1.2 GiB" on one screen and "1.29 GB" on another for the same
// number. The signature takes uint64 because the sources here (gopsutil's
// mem/disk/net) have no negative sentinel, unlike Docker's sizes.
func formatBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// formatRate renders bytes/s, the same output as formatSecurityRate.
func formatRate(bps float64) string {
	if bps < 0 {
		return ""
	}
	const unit = 1024.0
	if bps < unit {
		return fmt.Sprintf("%.0f B/s", bps)
	}
	div, exp := unit, 0
	for m := bps / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB/s", bps/div, "KMGTPE"[exp])
}
