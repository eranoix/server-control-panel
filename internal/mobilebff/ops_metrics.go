package mobilebff

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"server-control-panel/internal/system"
)

type SystemMetrics struct {
	Hostname        string        `json:"hostname,omitempty"`
	Platform        string        `json:"platform,omitempty"`
	ServerTime      string        `json:"server_time"`
	ServerTimeEpoch int64         `json:"server_time_epoch"`
	UptimeSeconds   uint64        `json:"uptime_seconds"`
	UptimeText      string        `json:"uptime_text"`
	CPU             CPUMetrics    `json:"cpu"`
	Memory          MemoryMetrics `json:"memory"`
	Swap            MemoryMetrics `json:"swap"`
	Disks           []DiskMetrics `json:"disks"`
	Net             *NetMetrics   `json:"net,omitempty"`
}

type CPUMetrics struct {
	Model       string  `json:"model,omitempty"`
	Cores       int     `json:"cores"`
	UsedPercent float64 `json:"used_percent"`
	Load1       float64 `json:"load1"`
	Load5       float64 `json:"load5"`
	Load15      float64 `json:"load15"`
	Steal       float64 `json:"steal"`
	Iowait      float64 `json:"iowait"`
}

type MemoryMetrics struct {
	Total       uint64  `json:"total"`
	Used        uint64  `json:"used"`
	Free        uint64  `json:"free"`
	UsedPercent float64 `json:"used_percent"`
	TotalText   string  `json:"total_text"`
	UsedText    string  `json:"used_text"`
}

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

type NetMetrics struct {
	Interface    string   `json:"interface"`
	BytesSent    uint64   `json:"bytes_sent"`
	BytesRecv    uint64   `json:"bytes_recv"`
	SentRate     *float64 `json:"sent_rate,omitempty"`
	RecvRate     *float64 `json:"recv_rate,omitempty"`
	SentRateText string   `json:"sent_rate_text,omitempty"`
	RecvRateText string   `json:"recv_rate_text,omitempty"`
}

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

var virtualIfacePrefixes = []string{
	"lo", "docker", "br-", "veth", "virbr", "tun", "tap", "wg", "tailscale",
	"cni", "flannel", "kube", "dummy", "gre", "sit", "bond-",
}

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
	case !t.have || t.iface != up.Name || up.BytesSent < t.sent || up.BytesRecv < t.recv:
		t.sentRate, t.recvRate = nil, nil
		t.iface, t.sent, t.recv, t.at, t.have = up.Name, up.BytesSent, up.BytesRecv, now, true
	default:
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
