// Package netusage measures the REAL per-device consumption of the tunnel, in real time,
// reading the kernel's conntrack (read-only, via the conntrack CLI) and summing bytes
// by destination port — each device has a Reality port of its own
// (device→port in .device-ports). No firewall rule, no touching the tunnel:
// it only observes. It accumulates a monotonic total per device (via connection id) and the
// instantaneous rate.
package netusage

import (
	"bufio"
	"context"
	"encoding/json"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"sync"
	"time"
)

const pollInterval = 2 * time.Second

// DeviceUsage is one device's consumption.
type DeviceUsage struct {
	Name        string  `json:"name"`
	Port        int     `json:"port"`
	TotalBytes  int64   `json:"total_bytes"`  // accumulated since the tracker booted
	RateBps     float64 `json:"rate_bps"`     // bytes/s in the last cycle
	ActiveConns int     `json:"active_conns"` // connections active right now
}

// Tracker follows consumption per port.
type Tracker struct {
	mu          sync.Mutex
	portMapPath string
	statePath   string // persists the running total across restarts
	pollN       int    // counts cycles so it saves periodically
	port2name   map[int]string
	cum         map[string]int64   // name → accumulated bytes
	rate        map[string]float64 // name → bytes/s
	conns       map[string]int     // name → active connections
	lastByID    map[string]int64   // connection id → last bytes seen
}

func New(portMapPath, statePath string) *Tracker {
	t := &Tracker{
		portMapPath: portMapPath,
		statePath:   statePath,
		cum:         map[string]int64{},
		rate:        map[string]float64{},
		conns:       map[string]int{},
		lastByID:    map[string]int64{},
	}
	t.loadState()
	return t
}

// loadState reloads the per-device running total from disk (tolerates absence).
func (t *Tracker) loadState() {
	if t.statePath == "" {
		return
	}
	raw, err := os.ReadFile(t.statePath)
	if err != nil {
		return
	}
	var m map[string]int64
	if json.Unmarshal(raw, &m) == nil {
		t.cum = m
	}
}

// saveState writes the running total (called with t.mu already held).
func (t *Tracker) saveState() {
	if t.statePath == "" {
		return
	}
	data, err := json.Marshal(t.cum)
	if err != nil {
		return
	}
	tmp := t.statePath + ".tmp"
	if os.WriteFile(tmp, data, 0o600) == nil {
		_ = os.Rename(tmp, t.statePath)
	}
}

// loadPortMap re-reads device→port (cheap, tolerates absence).
func (t *Tracker) loadPortMap() map[int]string {
	out := map[int]string{}
	raw, err := os.ReadFile(t.portMapPath)
	if err != nil {
		return out
	}
	var m map[string]int
	if json.Unmarshal(raw, &m) != nil {
		return out
	}
	for name, port := range m {
		out[port] = name
	}
	return out
}

// Start fires the polling loop. Bulletproof: recover per cycle — a meter
// may never take the process down.
func (t *Tracker) Start(ctx context.Context) {
	if _, err := exec.LookPath("conntrack"); err != nil {
		log.Printf("netusage: conntrack missing — per-device measurement turned off (%v)", err)
		return
	}
	go func() {
		tk := time.NewTicker(pollInterval)
		defer tk.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tk.C:
			}
			func() {
				defer func() {
					if r := recover(); r != nil {
						log.Printf("netusage: recovered from a panic in the poll: %v", r)
					}
				}()
				t.poll()
			}()
		}
	}()
	log.Printf("netusage: measuring per-device usage every %s (conntrack read-only)", pollInterval)
}

var (
	reDport = regexp.MustCompile(`dport=(\d+)`)
	reBytes = regexp.MustCompile(`bytes=(\d+)`)
	reID    = regexp.MustCompile(`\bid=(\d+)`)
)

func (t *Tracker) poll() {
	port2name := t.loadPortMap()
	if len(port2name) == 0 {
		return
	}
	cmd := exec.Command("conntrack", "-L", "-o", "id", "-p", "tcp")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return
	}
	if err := cmd.Start(); err != nil {
		return
	}
	// Accumulate deltas per id in this round; count active connections and bytes.
	deltaByName := map[string]int64{}
	connsByName := map[string]int{}
	seen := map[string]bool{}
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		mDport := reDport.FindStringSubmatch(line) // the first dport = the original tuple
		if mDport == nil {
			continue
		}
		port, _ := strconv.Atoi(mDport[1])
		name, ok := port2name[port]
		if !ok {
			continue
		}
		// sum the two bytes= (out + back) = the connection's total
		var total int64
		for _, b := range reBytes.FindAllStringSubmatch(line, -1) {
			v, _ := strconv.ParseInt(b[1], 10, 64)
			total += v
		}
		id := ""
		if m := reID.FindStringSubmatch(line); m != nil {
			id = m[1]
		}
		connsByName[name]++
		if id != "" {
			seen[id] = true
			prev := t.lastByID[id]
			if total >= prev {
				deltaByName[name] += total - prev
			} else {
				deltaByName[name] += total // id reused/reset
			}
			t.lastByID[id] = total
		}
	}
	cmd.Wait()

	t.mu.Lock()
	defer t.mu.Unlock()
	t.port2name = port2name
	// prune ids that vanished (their final delta was already counted while they existed)
	for id := range t.lastByID {
		if !seen[id] {
			delete(t.lastByID, id)
		}
	}
	// apply: running total += delta; rate = delta/interval
	names := map[string]bool{}
	for _, n := range port2name {
		names[n] = true
	}
	for n := range names {
		d := deltaByName[n]
		t.cum[n] += d
		t.rate[n] = float64(d) / pollInterval.Seconds()
		t.conns[n] = connsByName[n]
	}
	// persist the running total every ~30s (15 cycles of 2s)
	t.pollN++
	if t.pollN%15 == 0 {
		t.saveState()
	}
}

// Snapshot returns the current consumption of every mapped device.
func (t *Tracker) Snapshot() []DeviceUsage {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := []DeviceUsage{}
	for port, name := range t.port2name {
		out = append(out, DeviceUsage{
			Name:        name,
			Port:        port,
			TotalBytes:  t.cum[name],
			RateBps:     t.rate[name],
			ActiveConns: t.conns[name],
		})
	}
	return out
}
