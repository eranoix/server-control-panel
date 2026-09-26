// Package procs is the host process manager backend.
//
// It exposes List/Tree/Signal over gopsutil/v4/process with a hard denylist
// on the system-critical PIDs (init, sshd, this very vps-manager) so a UI
// kill button can never lock the operator out of the box.
//
// All exported functions are safe for concurrent use; gopsutil snapshots
// each /proc read so there's no shared mutable state here.
package procs

import (
	"context"
	"errors"
	"os"
	"sort"
	"strings"
	"syscall"
	"unicode/utf8"

	"github.com/shirou/gopsutil/v4/process"
)

// Info is the snapshot returned by List and Tree. CPU is a percentage of one
// core (so 230% on a 4-core box means ~58% total) and Memory is the resident
// set as a percentage of total RAM.
type Info struct {
	PID     int32   `json:"pid"`
	PPID    int32   `json:"ppid"`
	Name    string  `json:"name"`
	User    string  `json:"user"`
	CPU     float64 `json:"cpu"`
	Memory  float64 `json:"memory"`
	RSS     uint64  `json:"rss"`
	Cmdline string  `json:"cmdline"`
	Status  string  `json:"status"`
	Started int64   `json:"started"` // unix sec
}

// Filter narrows List output. Empty values match anything; numeric fields
// of zero are ignored.
type Filter struct {
	NameContains string
	User         string
	CmdContains  string
	MinCPU       float64
	MinMEM       float64
}

// SortBy is the column List sorts by, descending.
type SortBy string

const (
	SortCPU   SortBy = "cpu"
	SortMem   SortBy = "mem"
	SortPID   SortBy = "pid"
	SortName  SortBy = "name"
	SortStart SortBy = "start"
)

// Errors returned by Signal. ErrDenied means the PID is on the static
// denylist; ErrForbidden means the caller is not allowed to signal this
// PID (RBAC check at the handler layer should turn this into 403).
var (
	ErrDenied    = errors.New("pid denied by safety list")
	ErrForbidden = errors.New("caller not allowed to signal this pid")
	ErrBadSignal = errors.New("unknown signal name")
)

// sanitize strips ASCII control bytes and clamps length. Mirrors the
// helper in internal/system; copied to keep procs free of cross-package
// imports beyond gopsutil.
func sanitize(s string) string {
	const max = 512
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r == utf8.RuneError {
			continue
		}
		if r < 0x20 && r != '\t' {
			b.WriteByte(' ')
			continue
		}
		b.WriteRune(r)
		if b.Len() >= max {
			break
		}
	}
	return strings.TrimSpace(b.String())
}

// List returns up to `limit` processes after applying filter and sort.
// Offset is honored for pagination. limit<=0 means "all".
func List(ctx context.Context, f Filter, by SortBy, limit, offset int) ([]Info, int, error) {
	procs, err := process.ProcessesWithContext(ctx)
	if err != nil {
		return nil, 0, err
	}
	out := make([]Info, 0, len(procs))
	for _, p := range procs {
		if err := ctx.Err(); err != nil {
			return nil, 0, err
		}
		info, ok := snapshot(ctx, p)
		if !ok {
			continue
		}
		if !match(info, f) {
			continue
		}
		out = append(out, info)
	}
	total := len(out)
	sortBy(out, by)
	if offset < 0 {
		offset = 0
	}
	if offset >= len(out) {
		return []Info{}, total, nil
	}
	out = out[offset:]
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, total, nil
}

// Tree returns every process indexed by PID and a separate slice of root PIDs
// (parent missing or self-parented), letting the UI walk the tree without a
// second pass over gopsutil.
type TreeNode struct {
	Info
	Children []int32 `json:"children"`
}

func Tree(ctx context.Context) (map[int32]TreeNode, []int32, error) {
	procs, err := process.ProcessesWithContext(ctx)
	if err != nil {
		return nil, nil, err
	}
	nodes := make(map[int32]TreeNode, len(procs))
	for _, p := range procs {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		info, ok := snapshot(ctx, p)
		if !ok {
			continue
		}
		nodes[info.PID] = TreeNode{Info: info}
	}
	var roots []int32
	for pid, n := range nodes {
		if n.PPID == 0 || n.PPID == pid {
			roots = append(roots, pid)
			continue
		}
		parent, ok := nodes[n.PPID]
		if !ok {
			roots = append(roots, pid)
			continue
		}
		parent.Children = append(parent.Children, pid)
		nodes[n.PPID] = parent
	}
	sort.Slice(roots, func(i, j int) bool { return roots[i] < roots[j] })
	return nodes, roots, nil
}

// SignalByName resolves a signal name (TERM, KILL, HUP, STOP, CONT, INT, USR1, USR2)
// to a syscall.Signal. Empty defaults to TERM.
func SignalByName(name string) (syscall.Signal, error) {
	u := strings.ToUpper(name)
	u = strings.TrimPrefix(u, "SIG")
	switch u {
	case "", "TERM":
		return syscall.SIGTERM, nil
	case "KILL":
		return syscall.SIGKILL, nil
	case "HUP":
		return syscall.SIGHUP, nil
	case "STOP":
		return syscall.SIGSTOP, nil
	case "CONT":
		return syscall.SIGCONT, nil
	case "INT":
		return syscall.SIGINT, nil
	case "USR1":
		return syscall.SIGUSR1, nil
	case "USR2":
		return syscall.SIGUSR2, nil
	}
	return 0, ErrBadSignal
}

// IsDenied reports whether killing a PID would brick the box. The list is:
//   - PID 1 (init/systemd)
//   - this process (os.Getpid)
//   - sshd parent process (so the operator never loses remote access)
//
// gopsutil is consulted only for the sshd lookup; failures fall closed
// (deny). This is deliberate: when unsure, refuse.
func IsDenied(ctx context.Context, pid int32) bool {
	if pid <= 1 {
		return true
	}
	if int(pid) == os.Getpid() {
		return true
	}
	// Walk up from this process to see if pid is an ancestor — kill it
	// and the session dies.
	if pid == int32(os.Getppid()) {
		return true
	}
	p, err := process.NewProcessWithContext(ctx, pid)
	if err != nil {
		return true // unknown PID → safer to deny than race
	}
	name, _ := p.NameWithContext(ctx)
	switch strings.ToLower(name) {
	case "sshd", "systemd", "init", "kthreadd", "ksoftirqd":
		return true
	}
	return false
}

// Signal sends sig to pid after the denylist check. Caller must have done
// the RBAC check (primary, or PID owned by caller) before invoking — this
// function only enforces the static safety list.
func Signal(ctx context.Context, pid int32, sig syscall.Signal) error {
	if IsDenied(ctx, pid) {
		return ErrDenied
	}
	p, err := process.NewProcessWithContext(ctx, pid)
	if err != nil {
		return err
	}
	return p.SendSignalWithContext(ctx, sig)
}

// OwnerOf returns the unix username that owns pid, or "" on lookup failure.
// The handler uses this to decide whether a non-primary user is allowed to
// signal pid (rule: only your own processes).
//
// WARNING: OwnerOf alone is racy — by the time the caller signals, the
// PID may have been recycled to a different process owned by someone else.
// Use SignalAsOwner for the atomic check-then-signal.
func OwnerOf(ctx context.Context, pid int32) string {
	p, err := process.NewProcessWithContext(ctx, pid)
	if err != nil {
		return ""
	}
	u, _ := p.UsernameWithContext(ctx)
	return u
}

// SignalAsOwner verifies the PID belongs to `expectedOwner` (case-insensitive),
// captures the process start time, then signals — re-checking start time
// just before the kill syscall to detect a PID reuse race. Returns
// ErrForbidden if the owner doesn't match or the process was replaced.
//
// This is the function HTTP handlers should call for non-privileged
// users; primaries can bypass via plain Signal().
func SignalAsOwner(ctx context.Context, pid int32, sig syscall.Signal, expectedOwner string) error {
	if IsDenied(ctx, pid) {
		return ErrDenied
	}
	p, err := process.NewProcessWithContext(ctx, pid)
	if err != nil {
		return err
	}
	u, _ := p.UsernameWithContext(ctx)
	if u == "" || !strings.EqualFold(u, expectedOwner) {
		return ErrForbidden
	}
	startBefore, _ := p.CreateTimeWithContext(ctx)
	// Re-fetch right before the kill: if the PID has been recycled, the
	// new process will have a different CreateTime — we refuse rather
	// than signal a stranger.
	p2, err := process.NewProcessWithContext(ctx, pid)
	if err != nil {
		return err
	}
	startNow, _ := p2.CreateTimeWithContext(ctx)
	if startBefore == 0 || startNow == 0 || startBefore != startNow {
		return ErrForbidden
	}
	return p2.SendSignalWithContext(ctx, sig)
}

func snapshot(ctx context.Context, p *process.Process) (Info, bool) {
	pid := p.Pid
	name, err := p.NameWithContext(ctx)
	if err != nil || name == "" {
		return Info{}, false
	}
	ppid, _ := p.PpidWithContext(ctx)
	user, _ := p.UsernameWithContext(ctx)
	cpu, _ := p.CPUPercentWithContext(ctx)
	memp, _ := p.MemoryPercentWithContext(ctx)
	cmd, _ := p.CmdlineWithContext(ctx)
	status, _ := p.StatusWithContext(ctx)
	started, _ := p.CreateTimeWithContext(ctx)
	memInfo, _ := p.MemoryInfoWithContext(ctx)
	var rss uint64
	if memInfo != nil {
		rss = memInfo.RSS
	}
	return Info{
		PID:     pid,
		PPID:    ppid,
		Name:    name,
		User:    user,
		CPU:     cpu,
		Memory:  float64(memp),
		RSS:     rss,
		Cmdline: sanitize(cmd),
		Status:  strings.Join(status, ","),
		Started: started / 1000,
	}, true
}

func match(i Info, f Filter) bool {
	// The "name" filter now matches Name OR Cmdline: the user searches for "claude"
	// but the process shows up as "node" (Name = binary) with claude in the
	// cmdline — it used to filter 0 results, now it catches them.
	if f.NameContains != "" {
		needle := strings.ToLower(f.NameContains)
		if !strings.Contains(strings.ToLower(i.Name), needle) &&
			!strings.Contains(strings.ToLower(i.Cmdline), needle) {
			return false
		}
	}
	if f.User != "" && !strings.EqualFold(i.User, f.User) {
		return false
	}
	if f.CmdContains != "" && !strings.Contains(strings.ToLower(i.Cmdline), strings.ToLower(f.CmdContains)) {
		return false
	}
	if f.MinCPU > 0 && i.CPU < f.MinCPU {
		return false
	}
	if f.MinMEM > 0 && i.Memory < f.MinMEM {
		return false
	}
	return true
}

func sortBy(s []Info, by SortBy) {
	switch by {
	case SortMem:
		sort.Slice(s, func(i, j int) bool { return s[i].Memory > s[j].Memory })
	case SortPID:
		sort.Slice(s, func(i, j int) bool { return s[i].PID < s[j].PID })
	case SortName:
		sort.Slice(s, func(i, j int) bool { return s[i].Name < s[j].Name })
	case SortStart:
		sort.Slice(s, func(i, j int) bool { return s[i].Started > s[j].Started })
	default: // SortCPU
		sort.Slice(s, func(i, j int) bool { return s[i].CPU > s[j].CPU })
	}
}
