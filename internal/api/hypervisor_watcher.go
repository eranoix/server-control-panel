package api

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"sync"
	"time"

	"server-control-panel/internal/inventory"
	"server-control-panel/internal/notify"
)

const (
	TypeHypervisorUnreachable = "hypervisor.unreachable"
	TypeHypervisorRecovered   = "hypervisor.recovered"

	dedupHypervisor = "hypervisor:reach"
)

type hypervisorSentinel struct {
	mu        sync.Mutex
	failures  int
	down      bool
	sinceUnix int64
}

func (r *Router) ticksToBelieve() int {
	if v := os.Getenv("PANEL_SENTINEL_CYCLES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 3
}

func (r *Router) sentinelInterval() time.Duration {
	if v := os.Getenv("PANEL_SENTINEL_INTERVAL_S"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return 60 * time.Second
}

func (r *Router) startHypervisorWatcher(ctx context.Context) {
	if r.inventoryStore == nil {
		return
	}
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("hypervisor sentinel: panic: %v", rec)
			}
		}()
		t := time.NewTicker(r.sentinelInterval())
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				r.tickSentinel()
			}
		}
	}()
}

func (r *Router) tickSentinel() {
	if r.inventoryStore == nil {
		return
	}
	inv, err := r.inventoryStore.Snapshot()
	if err != nil {
		log.Printf("hypervisor sentinel: inventory unreadable (%v) — no verdict", err)
		return
	}
	r.checkReachability(inv, time.Now().Unix())
}

func (r *Router) checkReachability(inv inventory.Inventory, now int64) {
	if r.sentinel == nil {
		r.sentinel = &hypervisorSentinel{}
	}
	s := r.sentinel
	s.mu.Lock()
	defer s.mu.Unlock()

	failed := inv.LastPollError != ""

	if !failed {
		s.failures = 0
		if s.down {
			s.down = false
			outside := time.Duration(now-s.sinceUnix) * time.Second
			r.dispatchSentinel(notify.Event{
				Type:     TypeHypervisorRecovered,
				Severity: "info",
				Source:   "sentinel:hypervisor",
				Owner:    r.primaryUser(),
				Title:    "Home hypervisor is back",
				Body: fmt.Sprintf(
					"WHAT: the panel can reach the home hypervisor again.\n"+
						"WHERE: %s (as seen from the panel's server).\n"+
						"HOW LONG IT WAS DOWN: %s.\n"+
						"WHAT TO CHECK NOW: whether any guest failed to come back up, and whether the "+
						"overnight backup ran.",
					r.hypervisorAddr(), humanDuration(outside)),
				TS:       now,
				DedupKey: dedupHypervisor,
				Labels:   map[string]string{"target": "hypervisor", "state": "recovered"},
			})
		}
		return
	}

	s.failures++
	if s.failures == 1 {
		s.sinceUnix = now
	}
	if s.down || s.failures < r.ticksToBelieve() {
		return
	}
	s.down = true

	r.dispatchSentinel(notify.Event{
		Type:     TypeHypervisorUnreachable,
		Severity: "critical",
		Source:   "sentinel:hypervisor",
		Owner:    r.primaryUser(),
		Title:    "Home hypervisor unreachable",
		Body: fmt.Sprintf(
			"WHAT: the panel can no longer talk to the home hypervisor — %d cycles "+
				"in a row failed.\n"+
				"WHERE: %s, as seen from the panel's server.\n"+
				"SINCE: %s (%s ago).\n"+
				"WHY THIS IS SERIOUS: while it is down, the panel cannot power on, power off "+
				"or open a console on any guest — and the alarm that lives ON the hypervisor cannot "+
				"warn about it.\n"+
				"WHAT THE ERROR SAYS: %s\n"+
				"HOW TO CHECK: `tailscale ping hypervisor-01`; if the guests answer and it does not, "+
				"the machine is up and the problem is its network.",
			s.failures, r.hypervisorAddr(),
			time.Unix(s.sinceUnix, 0).UTC().Format("02/01 15:04 UTC"),
			humanDuration(time.Duration(now-s.sinceUnix)*time.Second),
			firstErrorLine(inv.LastPollError)),
		TS:       now,
		DedupKey: dedupHypervisor,
		Labels:   map[string]string{"target": "hypervisor", "state": "unreachable"},
	})
}

func (r *Router) dispatchSentinel(ev notify.Event) {
	log.Printf("hypervisor sentinel: %s — %s", ev.Type, ev.Title)
	if r.sentinelSink != nil {
		r.sentinelSink(ev)
		return
	}
	if r.notify == nil {
		return
	}
	r.notify.Dispatch(ev)
}

func (r *Router) hypervisorAddr() string {
	if r.pveConfig != nil && r.pveConfig.BaseURL != "" {
		return r.pveConfig.BaseURL
	}
	return "home hypervisor"
}

func (r *Router) primaryUser() string {
	if r.cfg != nil {
		return r.cfg.Primary
	}
	return ""
}

func firstErrorLine(e string) string {
	for i := 0; i < len(e); i++ {
		if e[i] == '\n' {
			e = e[:i]
			break
		}
	}
	r := []rune(e)
	if len(r) > 160 {
		return string(r[:160]) + "…"
	}
	if len(r) == 0 {
		return "(no detail)"
	}
	return e
}

func humanDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%d s", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%d min", int(d.Minutes()))
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if m == 0 {
		return fmt.Sprintf("%d h", h)
	}
	return fmt.Sprintf("%d h %d min", h, m)
}
