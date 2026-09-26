package system

import (
	"context"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/process"
)

// sanitizeCmdline strips ASCII control bytes and caps length so a process
// emitting `<script>...` or terminal escape sequences in its argv cannot
// poison the JSON response or leak into x-text/x-html via Alpine.
func sanitizeCmdline(s string) string {
	if s == "" {
		return ""
	}
	const max = 512
	if len(s) > max {
		s = s[:max] + "…"
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\t' || r == ' ':
			b.WriteRune(' ')
		case r < 0x20 || r == 0x7f:
			// drop control bytes
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

type Stats struct {
	Host      HostInfo      `json:"host"`
	CPU       CPUInfo       `json:"cpu"`
	Memory    MemInfo       `json:"memory"`
	Swap      MemInfo       `json:"swap"`
	Load      LoadInfo      `json:"load"`
	Disks     []DiskInfo    `json:"disks"`
	Net       []NetInfo     `json:"net"`
	TopProcs  []ProcInfo    `json:"top_procs"`
	GoRuntime GoRuntimeInfo `json:"go_runtime"`
}

type HostInfo struct {
	Hostname     string `json:"hostname"`
	Platform     string `json:"platform"`
	PlatformVer  string `json:"platform_version"`
	Kernel       string `json:"kernel"`
	Uptime       uint64 `json:"uptime"`
	BootTime     uint64 `json:"boot_time"`
	Architecture string `json:"architecture"`
}

type CPUInfo struct {
	Model   string    `json:"model"`
	Cores   int       `json:"cores"`
	Percent []float64 `json:"percent"`
	Overall float64   `json:"overall"`
	// Steal = % of CPU time the HYPERVISOR stole from this VM (overcrowded
	// host / noisy neighbours). Invisible to per-process %CPU — which is why
	// the sum of the processes "does not add up" to the real slowness. >10–15% = a provider
	// problem. Iowait = % waiting on disk. Computed by delta between collections.
	Steal  float64 `json:"steal"`
	Iowait float64 `json:"iowait"`
}

type MemInfo struct {
	Total       uint64  `json:"total"`
	Used        uint64  `json:"used"`
	Free        uint64  `json:"free"`
	UsedPercent float64 `json:"used_percent"`
}

type LoadInfo struct {
	Load1  float64 `json:"load1"`
	Load5  float64 `json:"load5"`
	Load15 float64 `json:"load15"`
}

type DiskInfo struct {
	Mount       string  `json:"mount"`
	FSType      string  `json:"fstype"`
	Total       uint64  `json:"total"`
	Used        uint64  `json:"used"`
	Free        uint64  `json:"free"`
	UsedPercent float64 `json:"used_percent"`
}

type NetInfo struct {
	Name      string `json:"name"`
	BytesSent uint64 `json:"bytes_sent"`
	BytesRecv uint64 `json:"bytes_recv"`
}

type ProcInfo struct {
	PID     int32   `json:"pid"`
	Name    string  `json:"name"`
	User    string  `json:"user"`
	CPU     float64 `json:"cpu"`
	Memory  float32 `json:"memory"`
	Cmdline string  `json:"cmdline"`
}

type GoRuntimeInfo struct {
	Version    string `json:"version"`
	Goroutines int    `json:"goroutines"`
	NumCPU     int    `json:"num_cpu"`
}

// state for the steal/iowait computation by DELTA between collections (non-blocking:
// no extra sleep — it uses the difference of the cumulative /proc/stat counters).
var (
	lastCPUTimes cpu.TimesStat
	haveLastCPU  bool
	cpuTimesMu   sync.Mutex
)

// cpuStealIowait returns (steal%, iowait%) for the interval since the last collection.
// The first call returns 0/0 (no baseline yet).
func cpuStealIowait(ctx context.Context) (float64, float64) {
	ts, err := cpu.TimesWithContext(ctx, false) // aggregate (all cores)
	if err != nil || len(ts) == 0 {
		return 0, 0
	}
	cur := ts[0]
	total := func(t cpu.TimesStat) float64 {
		// sum of the states (Guest is already included in User in the kernel — do not add it)
		return t.User + t.System + t.Idle + t.Nice + t.Iowait + t.Irq + t.Softirq + t.Steal
	}
	cpuTimesMu.Lock()
	defer cpuTimesMu.Unlock()
	if !haveLastCPU {
		lastCPUTimes = cur
		haveLastCPU = true
		return 0, 0
	}
	prev := lastCPUTimes
	lastCPUTimes = cur
	dTotal := total(cur) - total(prev)
	if dTotal <= 0 {
		return 0, 0
	}
	steal := (cur.Steal - prev.Steal) / dTotal * 100
	iowait := (cur.Iowait - prev.Iowait) / dTotal * 100
	if steal < 0 {
		steal = 0
	}
	if iowait < 0 {
		iowait = 0
	}
	return steal, iowait
}

func Collect(ctx context.Context) (*Stats, error) {
	s := &Stats{}

	if h, err := host.InfoWithContext(ctx); err == nil {
		s.Host = HostInfo{
			Hostname:     h.Hostname,
			Platform:     h.Platform,
			PlatformVer:  h.PlatformVersion,
			Kernel:       h.KernelVersion,
			Uptime:       h.Uptime,
			BootTime:     h.BootTime,
			Architecture: h.KernelArch,
		}
	}

	cpuPerc, _ := cpu.PercentWithContext(ctx, 200*time.Millisecond, true)
	overall := 0.0
	if len(cpuPerc) > 0 {
		for _, p := range cpuPerc {
			overall += p
		}
		overall /= float64(len(cpuPerc))
	}
	model := ""
	if infos, err := cpu.InfoWithContext(ctx); err == nil && len(infos) > 0 {
		model = infos[0].ModelName
	}
	steal, iowait := cpuStealIowait(ctx)
	s.CPU = CPUInfo{Model: model, Cores: runtime.NumCPU(), Percent: cpuPerc, Overall: overall, Steal: steal, Iowait: iowait}

	if m, err := mem.VirtualMemoryWithContext(ctx); err == nil {
		s.Memory = MemInfo{Total: m.Total, Used: m.Used, Free: m.Available, UsedPercent: m.UsedPercent}
	}
	if sw, err := mem.SwapMemoryWithContext(ctx); err == nil {
		s.Swap = MemInfo{Total: sw.Total, Used: sw.Used, Free: sw.Free, UsedPercent: sw.UsedPercent}
	}

	if l, err := load.AvgWithContext(ctx); err == nil {
		s.Load = LoadInfo{Load1: l.Load1, Load5: l.Load5, Load15: l.Load15}
	}

	if parts, err := disk.PartitionsWithContext(ctx, false); err == nil {
		// Deduplicate by DEVICE, not by mount point.
		//
		// A container bind-mounts /etc/hosts, /etc/hostname and
		// /etc/resolv.conf as three separate entries in /proc/mounts. Their
		// mount points differ, so a mount-point key lets all three through,
		// and each then reports the size of the filesystem behind it -- the
		// panel showed three identical "disks" of the same hundreds of GB.
		// The device is what actually identifies a volume.
		seenDevice := map[string]bool{}
		for _, p := range parts {
			if p.Device != "" && seenDevice[p.Device] {
				continue
			}
			// A bind-mounted FILE is never a disk. Reporting one as a disk is
			// wrong everywhere, not just in a container.
			if fi, statErr := os.Stat(p.Mountpoint); statErr != nil || !fi.IsDir() {
				continue
			}
			u, err := disk.UsageWithContext(ctx, p.Mountpoint)
			if err != nil {
				continue
			}
			if p.Device != "" {
				seenDevice[p.Device] = true
			}
			s.Disks = append(s.Disks, DiskInfo{
				Mount: p.Mountpoint, FSType: p.Fstype,
				Total: u.Total, Used: u.Used, Free: u.Free, UsedPercent: u.UsedPercent,
			})
		}
	}

	if ios, err := net.IOCountersWithContext(ctx, true); err == nil {
		for _, io := range ios {
			if io.BytesSent == 0 && io.BytesRecv == 0 {
				continue
			}
			s.Net = append(s.Net, NetInfo{Name: io.Name, BytesSent: io.BytesSent, BytesRecv: io.BytesRecv})
		}
	}

	if procs, err := process.ProcessesWithContext(ctx); err == nil {
		type pair struct {
			p   *process.Process
			cpu float64
		}
		var ps []pair
		for _, p := range procs {
			c, _ := p.CPUPercentWithContext(ctx)
			ps = append(ps, pair{p, c})
		}
		// quick partial sort: pick top 10 by cpu
		for i := 0; i < len(ps) && i < 10; i++ {
			maxIdx := i
			for j := i + 1; j < len(ps); j++ {
				if ps[j].cpu > ps[maxIdx].cpu {
					maxIdx = j
				}
			}
			ps[i], ps[maxIdx] = ps[maxIdx], ps[i]
		}
		limit := 10
		if len(ps) < limit {
			limit = len(ps)
		}
		for i := 0; i < limit; i++ {
			p := ps[i].p
			name, _ := p.NameWithContext(ctx)
			user, _ := p.UsernameWithContext(ctx)
			memp, _ := p.MemoryPercentWithContext(ctx)
			cmd, _ := p.CmdlineWithContext(ctx)
			s.TopProcs = append(s.TopProcs, ProcInfo{
				PID: p.Pid, Name: name, User: user,
				CPU: ps[i].cpu, Memory: memp, Cmdline: sanitizeCmdline(cmd),
			})
		}
	}

	s.GoRuntime = GoRuntimeInfo{Version: runtime.Version(), Goroutines: runtime.NumGoroutine(), NumCPU: runtime.NumCPU()}
	return s, nil
}
