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

// hypervisor_watcher.go — the sentinel that speaks WHEN THE HOUSE GOES DOWN.
//
// ────────────────────────────────────────────────────────────────────────────
// 🔴 WHY IT HAS TO LIVE HERE, AND NOT ON THE HYPERVISOR
//
// The home host once became unreachable and NOBODY was told. The alarm path that
// existed lived INSIDE it (postfix → proxmox-mail-forward → ntfy); with the host
// down, it could not speak about itself. On the same day, a routine mail from
// the host produced a useless alert.
//
// In other words: the channel emitted noise and fell silent on the real event.
// This sentinel exists to close exactly that hole — it runs on the VPS, which is
// the only point in the system able to say "the house is gone" while the house
// is gone.
//
// 🔴 IT ALERTS ON THE EDGE, NEVER ON THE LEVEL
//
// An alarm that fires every cycle for as long as the problem lasts is not an
// alarm: it is an open tap, and it trains people to ignore it. Here only the
// TRANSITION produces an event — it went down, and it came back. While the state
// does not change, silence.
//
// 🔴 AND IT WAITS N CYCLES BEFORE BELIEVING IT
//
// One failed tick is routine: the network wobbles, the hypervisor gets busy.
// Alerting on the first is the same disease as permanent red, by another route.
// The default is 3 consecutive cycles — minutes, not seconds.
// ────────────────────────────────────────────────────────────────────────────

const (
	// TipoHipervisorInalcancavel and TipoHipervisorVoltou are types of their OWN,
	// not `metric.threshold`: whoever writes a rule on screen needs to tell "the
	// house went down" from "a metric crossed a threshold". Mixing them would
	// make the house's rule inherit the routing of any gauge.
	TipoHipervisorInalcancavel = "hypervisor.unreachable"
	TipoHipervisorVoltou       = "hypervisor.recovered"

	// dedupHipervisor keeps both ends under the SAME dedup identity: a "went
	// down" followed by a "came back" is a single story, and the router needs to
	// be able to treat it as such.
	dedupHipervisor = "hypervisor:alcance"
)

type hipervisorSentinela struct {
	mu        sync.Mutex
	falhas    int   // consecutive ticks with the hypervisor unreachable
	caido     bool  // have we already announced the outage?
	desdeUnix int64 // when the first failure of the run happened
}

// ciclosParaAcreditar is how many consecutive ticks have to fail before the
// sentinel believes it. Configurable because the test needs 1 and production
// needs 3 — and a `time.Sleep` in the test would mean waiting on the clock,
// which is exactly what is forbidden here.
func (r *Router) ciclosParaAcreditar() int {
	if v := os.Getenv("VPSM_SENTINELA_CICLOS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 3
}

func (r *Router) intervaloDaSentinela() time.Duration {
	if v := os.Getenv("VPSM_SENTINELA_INTERVALO_S"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return 60 * time.Second
}

func (r *Router) startHypervisorWatcher(ctx context.Context) {
	if r.inventoryStore == nil {
		// With no inventory there is nothing to watch, and a sentinel watching the
		// void would alert "it went down" forever.
		return
	}
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("hypervisor sentinel: panic: %v", rec)
			}
		}()
		t := time.NewTicker(r.intervaloDaSentinela())
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				r.tickSentinela()
			}
		}
	}()
}

// tickSentinela reads the inventory and decides whether there is news. It does
// NOT talk to the hypervisor: the poller does that, and having two channels
// asking the same question would create two truths about whether the house is
// reachable.
func (r *Router) tickSentinela() {
	// 🔴 THE GUARD LIVES HERE, and not only at start-up.
	//
	// `startHypervisorWatcher` already refuses to start without an inventory, but
	// a guard that exists only at the starting point is a guard the next call
	// forgets — and the price here is a panic inside a background goroutine,
	// which takes the watch down exactly when it should be standing. The test
	// caught this.
	if r.inventoryStore == nil {
		return
	}
	inv, err := r.inventoryStore.Snapshot()
	if err != nil {
		// Being unable to read our own inventory is not news ABOUT THE HOUSE.
		// Alerting here would say "the house went down" about a disk fault on the VPS.
		log.Printf("hypervisor sentinel: inventory unreadable (%v) — no verdict", err)
		return
	}
	r.avaliaAlcance(inv, time.Now().Unix())
}

// avaliaAlcance is the pure logic, separated from the clock and from the disk so
// it can be proven in both directions without waiting for anything.
func (r *Router) avaliaAlcance(inv inventory.Inventory, agora int64) {
	if r.sentinela == nil {
		r.sentinela = &hipervisorSentinela{}
	}
	s := r.sentinela
	s.mu.Lock()
	defer s.mu.Unlock()

	// The poller stamps the ATTEMPT even when it fails (the second clock), and
	// records the reason in LastPollError. Empty = the last cycle worked.
	falhou := inv.LastPollError != ""

	if !falhou {
		s.falhas = 0
		if s.caido {
			s.caido = false
			fora := time.Duration(agora-s.desdeUnix) * time.Second
			r.dispatchSentinela(notify.Event{
				Type:     TipoHipervisorVoltou,
				Severity: "info",
				Source:   "sentinela:hipervisor",
				Owner:    r.primaryUser(),
				Title:    "Home hypervisor is back",
				Body: fmt.Sprintf(
					"WHAT: the panel can reach the home hypervisor again.\n"+
						"WHERE: %s (seen from the VPS, over the tailnet).\n"+
						"HOW LONG IT WAS DOWN: %s.\n"+
						"WHAT TO CHECK NOW: whether any guest failed to come back up, and whether the "+
						"overnight backup ran.",
					r.enderecoDoHipervisor(), duracaoEmPortugues(fora)),
				TS:       agora,
				DedupKey: dedupHipervisor,
				Labels:   map[string]string{"alvo": "hipervisor", "estado": "voltou"},
			})
		}
		return
	}

	s.falhas++
	if s.falhas == 1 {
		s.desdeUnix = agora
	}
	if s.caido || s.falhas < r.ciclosParaAcreditar() {
		// Already announced, or not yet worth believing. Silence in both cases:
		// repeating while the problem lasts is what trains people to ignore it.
		return
	}
	s.caido = true

	r.dispatchSentinela(notify.Event{
		Type:     TipoHipervisorInalcancavel,
		Severity: "critical",
		Source:   "sentinela:hipervisor",
		Owner:    r.primaryUser(),
		Title:    "Home hypervisor unreachable",
		Body: fmt.Sprintf(
			"WHAT: the panel can no longer talk to the home hypervisor — %d cycles "+
				"in a row failed.\n"+
				"WHERE: %s, seen from the VPS over the tailnet.\n"+
				"SINCE: %s (%s ago).\n"+
				"WHY THIS IS SERIOUS: while it is down, the panel cannot power on, power off "+
				"or open a console on any guest — and the alarm that lives ON the hypervisor cannot "+
				"warn about it.\n"+
				"WHAT THE ERROR SAYS: %s\n"+
				"HOW TO CHECK: `tailscale ping hypervisor-01`; if the guests answer and it does not, "+
				"the machine is up and the problem is its network.",
			s.falhas, r.enderecoDoHipervisor(),
			time.Unix(s.desdeUnix, 0).UTC().Format("02/01 15:04 UTC"),
			duracaoEmPortugues(time.Duration(agora-s.desdeUnix)*time.Second),
			primeiraLinhaDoErro(inv.LastPollError)),
		TS:       agora,
		DedupKey: dedupHipervisor,
		Labels:   map[string]string{"alvo": "hipervisor", "estado": "inalcancavel"},
	})
}

// dispatchSentinela is the sentinel's ONLY exit point. `sentinelaSink` exists so
// a test can assert WHICH event would go out without building a whole
// notification router — and, above all, without sending a real message to
// anybody's phone while the suite runs.
func (r *Router) dispatchSentinela(ev notify.Event) {
	// ALWAYS record it, even with no router: an alarm that found no channel is
	// still news, and the log is the last place where it survives.
	log.Printf("hypervisor sentinel: %s — %s", ev.Type, ev.Title)
	if r.sentinelaSink != nil {
		r.sentinelaSink(ev)
		return
	}
	if r.notify == nil {
		return
	}
	r.notify.Dispatch(ev)
}

func (r *Router) enderecoDoHipervisor() string {
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

// primeiraLinhaDoErro cuts the poller's error down to its first line and to a
// readable length: the message goes to WhatsApp, and a whole network stack
// trace there is text nobody reads.
func primeiraLinhaDoErro(e string) string {
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

// duracaoEmPortugues avoids "1h0m0s" in a message somebody reads on a phone.
func duracaoEmPortugues(d time.Duration) string {
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
