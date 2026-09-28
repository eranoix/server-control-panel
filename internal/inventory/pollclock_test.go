package inventory

import (
	"context"
	"errors"
	"testing"
	"time"
)

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
	f.failure = errors.New("hypervisor silent")
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

	nodes := nodesByID(t, st)
	if got := nodes["lxc/207"].Status.ObservedAt; got != 1800000000 {
		t.Fatalf("the node was rejuvenated by the attempt that failed: %d", got)
	}
}

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

	stale := ViewPoll(Inventory{LastPollAt: 1800000000, LastPollError: "hypervisor silent"}, ttl, now)
	if stale.AgeSeconds != 300 || !stale.Stale {
		t.Errorf("age = %d stale = %v, want 300/true", stale.AgeSeconds, stale.Stale)
	}
	if stale.Error != "hypervisor silent" {
		t.Errorf("the reason did not reach the view: %q", stale.Error)
	}
}

func TestTwoClocksAreIndependent(t *testing.T) {
	f := &fakePVE{resources: testResources()}
	p, st, rel := newTestPoller(t, f, Sources{}, PollerConfig{})
	if err := p.tick(context.Background()); err != nil {
		t.Fatal(err)
	}

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
