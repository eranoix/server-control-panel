package api

import (
	"strings"
	"testing"

	"server-control-panel/internal/config"
	"server-control-panel/internal/inventory"
	"server-control-panel/internal/notify"
	"server-control-panel/internal/pve"
)

func sentinelRouter(t *testing.T) (*Router, *[]notify.Event) {
	t.Helper()
	var seen []notify.Event
	r := &Router{cfg: &config.Config{Primary: "sam"}}
	r.sentinelSink = func(ev notify.Event) { seen = append(seen, ev) }
	return r, &seen
}

func invOK() inventory.Inventory { return inventory.Inventory{LastPollError: ""} }
func badInv() inventory.Inventory {
	return inventory.Inventory{LastPollError: "pve /api2/json/cluster/resources?type=vm: unreachable: dial tcp 198.51.100.20:8006: i/o timeout"}
}

func TestSentinelDoesNotTrustFirstTick(t *testing.T) {
	r, evs := sentinelRouter(t)
	r.checkReachability(badInv(), 1000)
	r.checkReachability(badInv(), 1060)
	if len(*evs) != 0 {
		t.Fatalf("alerted before the 3 cycles: %v", *evs)
	}
	r.checkReachability(badInv(), 1120)
	if len(*evs) != 1 {
		t.Fatalf("on the third cycle it had to alert: %d events", len(*evs))
	}
	if (*evs)[0].Type != TypeHypervisorUnreachable {
		t.Errorf("type = %q", (*evs)[0].Type)
	}
}

func TestSentinelAlertsOnEdge(t *testing.T) {
	r, evs := sentinelRouter(t)
	for i := 0; i < 20; i++ {
		r.checkReachability(badInv(), int64(1000+i*60))
	}
	if len(*evs) != 1 {
		t.Fatalf("🔴 %d events for ONE outage — the alarm turned into a running tap", len(*evs))
	}
}

func TestSentinelAnnouncesRecoveryOnce(t *testing.T) {
	r, evs := sentinelRouter(t)
	for i := 0; i < 5; i++ {
		r.checkReachability(badInv(), int64(1000+i*60))
	}
	for i := 0; i < 5; i++ {
		r.checkReachability(invOK(), int64(2000+i*60))
	}
	if len(*evs) != 2 {
		t.Fatalf("events = %d, want 2 (went down, came back): %v", len(*evs), typesOf(*evs))
	}
	if (*evs)[1].Type != TypeHypervisorRecovered {
		t.Errorf("the second event = %q", (*evs)[1].Type)
	}
	if !strings.Contains((*evs)[1].Body, "WAS DOWN") {
		t.Errorf("the recovery does not say how long it was down: %s", (*evs)[1].Body)
	}
}

func TestTransientFailureEmitsNothing(t *testing.T) {
	r, evs := sentinelRouter(t)
	r.checkReachability(badInv(), 1000)
	r.checkReachability(badInv(), 1060)
	r.checkReachability(invOK(), 1120)
	if len(*evs) != 0 {
		t.Fatalf("a wobble turned into an incident: %v", typesOf(*evs))
	}
}

func TestMessageSaysWhatWhereAndWhy(t *testing.T) {
	r, evs := sentinelRouter(t)
	r.pveConfig = &testPVECfg
	for i := 0; i < 3; i++ {
		r.checkReachability(badInv(), int64(1000+i*60))
	}
	if len(*evs) != 1 {
		t.Fatalf("events = %d", len(*evs))
	}
	b := (*evs)[0].Body
	for _, required := range []string{"WHAT:", "WHERE:", "SINCE:", "WHY THIS IS SERIOUS:", "HOW TO CHECK:"} {
		if !strings.Contains(b, required) {
			t.Errorf("the message is missing %q:\n%s", required, b)
		}
	}
	if !strings.Contains(b, "hypervisor.local") {
		t.Errorf("the WHERE does not name the target:\n%s", b)
	}
	if !strings.Contains(b, "i/o timeout") {
		t.Errorf("the message does not carry the real error:\n%s", b)
	}
	if (*evs)[0].Severity != "critical" {
		t.Errorf("severity = %q, want critical", (*evs)[0].Severity)
	}
}

func TestUnreadableInventoryIsNotHomeOutage(t *testing.T) {
	r, evs := sentinelRouter(t)
	r.inventoryStore = nil
	r.tickSentinel()
	if len(*evs) != 0 {
		t.Fatalf("missing inventory turned into an outage of the house: %v", typesOf(*evs))
	}
}

func typesOf(evs []notify.Event) []string {
	out := make([]string, 0, len(evs))
	for _, e := range evs {
		out = append(out, e.Type)
	}
	return out
}

var testPVECfg = pve.Config{BaseURL: "https://hypervisor.local:8006"}
