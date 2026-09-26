package inventory

import (
	"context"
	"errors"
	"testing"
	"time"
)

// pollclock_test.go — the TWO clocks.
//
// # The defect these tests close
//
// The screen had ONE clock: the age of the data (`age_seconds`). With a single
// clock, two very different accidents produce exactly the same screen:
//
//	the NODE went quiet       → its fault; go and look at the guest
//	the POLLER stopped/failed → OUR fault; every node ages together and
//	                            not one of them has anything wrong
//
// The second case is the worse one, because the screen accuses eleven innocent
// nodes. What breaks the tie is the instant of the last ATTEMPT: if the poller
// tried 4 s ago and the node is at 40 min, the node is the mute one; if the
// attempt is at 40 min too, the one that is mute is the panel.

// 🔴 TestAttemptStampedEvenWhenDiscoveryFails — the test that defines
// the field. Stamping only on success would reproduce the defect: a poller that
// has been failing for half an hour would carry a timestamp from half an hour
// ago, indistinguishable from a poller that never ran again.
func TestAttemptStampedEvenWhenDiscoveryFails(t *testing.T) {
	f := &fakePVE{resources: testResources()}
	p, st, rel := newTestPoller(t, f, Sources{}, PollerConfig{})
	if err := p.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	inv, _ := st.Snapshot()
	if inv.LastPollAt != 1800000000 {
		t.Fatalf("a successful attempt was not timestamped: %d", inv.LastPollAt)
	}
	if inv.LastPollError != "" {
		t.Fatalf("an attempt that succeeded was left carrying error %q", inv.LastPollError)
	}

	rel.advance(5 * time.Minute)
	f.mu.Lock()
	f.failure = errors.New("hipervisor mudo")
	f.mu.Unlock()
	if err := p.tick(context.Background()); err == nil {
		t.Fatal("the tick should have failed")
	}

	inv, _ = st.Snapshot()
	if inv.LastPollAt != 1800000300 {
		t.Fatalf("LastPollAt = %d, want 1800000300 — the ATTEMPT happened, and that is what this field measures",
			inv.LastPollAt)
	}
	if inv.LastPollError == "" {
		t.Fatal("an attempt that failed did not record the reason — 'I tried and could not' is different from 'I tried'")
	}

	// And the nodes' data keeps the OLD timestamp: there are two clocks, and it is
	// the DIVERGENCE between them that says whose fault it is.
	nodes := nodesByID(t, st)
	if got := nodes["lxc/207"].Status.ObservedAt; got != 1800000000 {
		t.Fatalf("the node was rejuvenated by the attempt that failed: %d", got)
	}
}

// TestViewPollResolvesAgeOnServer — the age of the attempt is born on the
// server, for the same reason as all the others: the browser's clock is not a
// controlled variable. And "never tried" is -1, never 0.
func TestViewPollResolvesAgeOnServer(t *testing.T) {
	now := time.Unix(1800000300, 0)
	ttl := 90 * time.Second

	never := ViewPoll(Inventory{}, ttl, now)
	if never.AgeSeconds != -1 {
		t.Errorf("with no attempt at all: age = %d, want -1 ('never observed')", never.AgeSeconds)
	}
	if !never.Stale {
		t.Error("with no attempt at all the poller has to show up EXPIRED")
	}

	recent := ViewPoll(Inventory{LastPollAt: 1800000296}, ttl, now)
	if recent.AgeSeconds != 4 {
		t.Errorf("age = %d, want 4", recent.AgeSeconds)
	}
	if recent.Stale {
		t.Error("4 s is not expired with a 90 s TTL")
	}

	stale := ViewPoll(Inventory{LastPollAt: 1800000000, LastPollError: "hipervisor mudo"}, ttl, now)
	if stale.AgeSeconds != 300 || !stale.Stale {
		t.Errorf("age = %d stale = %v, want 300/true", stale.AgeSeconds, stale.Stale)
	}
	if stale.Error != "hipervisor mudo" {
		t.Errorf("the reason did not reach the view: %q", stale.Error)
	}
}

// 🔴 TestTwoClocksAreIndependent — the scenario that motivated the two
// fields, staged in full: the poller answers on every tick and ONE node has
// vanished from discovery. The attempt's clock stays new; the node's ages. If
// the two moved together, the vanished node would look eternally fresh.
func TestTwoClocksAreIndependent(t *testing.T) {
	f := &fakePVE{resources: testResources()}
	p, st, rel := newTestPoller(t, f, Sources{}, PollerConfig{})
	if err := p.tick(context.Background()); err != nil {
		t.Fatal(err)
	}

	// The guest vanishes from the hypervisor; the tick stays healthy.
	f.mu.Lock()
	f.resources = f.resources[:1]
	f.mu.Unlock()
	rel.advance(10 * time.Minute)
	if err := p.tick(context.Background()); err != nil {
		t.Fatal(err)
	}

	inv, _ := st.Snapshot()
	poll := ViewPoll(inv, 90*time.Second, time.Unix(1800000600, 0))
	if poll.AgeSeconds != 0 || poll.Stale {
		t.Fatalf("attempt clock = %ds stale=%v — the poller has just run", poll.AgeSeconds, poll.Stale)
	}

	seen := View(inv, 90*time.Second, time.Unix(1800000600, 0))
	var gone NodeView
	for _, v := range seen {
		if v.ID == "qemu/208" {
			gone = v
		}
	}
	if gone.ID == "" {
		t.Fatal("the vanished guest was DELETED from the inventory — amnesia presented as truth")
	}
	if gone.AgeSeconds != 600 || !gone.Stale {
		t.Fatalf("vanished node: age = %d stale = %v, want 600/true — the two clocks cannot move together",
			gone.AgeSeconds, gone.Stale)
	}
}
