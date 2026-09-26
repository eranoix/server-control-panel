package api

import (
	"strings"
	"testing"

	"server-control-panel/internal/config"
	"server-control-panel/internal/inventory"
	"server-control-panel/internal/notify"
	"server-control-panel/internal/pve"
)

// sentinelRouter builds a Router with an event collector in place of the
// notification router. NO test here sends a real message: what is asserted is
// WHICH event would go out, and when.
func sentinelRouter(t *testing.T) (*Router, *[]notify.Event) {
	t.Helper()
	var seen []notify.Event
	r := &Router{cfg: &config.Config{Primary: "sam"}}
	r.sentinelSink = func(ev notify.Event) { seen = append(seen, ev) }
	return r, &seen
}

func invOK() inventory.Inventory { return inventory.Inventory{LastPollError: ""} }
func badInv() inventory.Inventory {
	return inventory.Inventory{LastPollError: "pve /api2/json/cluster/resources?type=vm: inalcancavel: dial tcp 198.51.100.20:8006: i/o timeout"}
}

// 🔴 TestSentinelDoesNotTrustFirstTick — a tick that fails is routine: the
// network wobbles, the hypervisor gets busy. Alerting on the first one is the
// permanent-red disease by another road.
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

// TestSentinelAlertsOnEdge: while the problem lasts, silence. An alarm
// that fires every cycle is not an alarm: it is a running tap, and it trains
// people to ignore it.
func TestSentinelAlertsOnEdge(t *testing.T) {
	r, evs := sentinelRouter(t)
	for i := 0; i < 20; i++ {
		r.checkReachability(badInv(), int64(1000+i*60))
	}
	if len(*evs) != 1 {
		t.Fatalf("🔴 %d events for ONE outage — the alarm turned into a running tap", len(*evs))
	}
}

// And the recovery is news too, exactly once.
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

// A transient failure that clears BEFORE the threshold produces no event at all
// — neither a down nor a recovery. Alerting here would count a wobble as an incident.
func TestTransientFailureEmitsNothing(t *testing.T) {
	r, evs := sentinelRouter(t)
	r.checkReachability(badInv(), 1000)
	r.checkReachability(badInv(), 1060)
	r.checkReachability(invOK(), 1120)
	if len(*evs) != 0 {
		t.Fatalf("a wobble turned into an incident: %v", typesOf(*evs))
	}
}

// 🔴 TestMessageSaysWhatWhereAndWhy — the operator's request, turned into a pin:
// "if it is going to raise an alert it has to say what is happening, where and
// why". A vague message is what he already had, and it is what made the channel
// lose its credibility.
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
	// WHERE has to be CONCRETE, not "the server".
	if !strings.Contains(b, "hypervisor.local") {
		t.Errorf("the WHERE does not name the target:\n%s", b)
	}
	// And the poller's real error goes in — it is the clue the operator has.
	if !strings.Contains(b, "i/o timeout") {
		t.Errorf("the message does not carry the real error:\n%s", b)
	}
	if (*evs)[0].Severity != "critical" {
		t.Errorf("severity = %q, want critical", (*evs)[0].Severity)
	}
}

// 🔴 TestUnreadableInventoryIsNotHomeOutage — failing to read OUR OWN
// inventory is a defect of the VPS, not news about the house. Confusing the two
// would send the operator to look in the wrong place at three in the morning.
func TestUnreadableInventoryIsNotHomeOutage(t *testing.T) {
	r, evs := sentinelRouter(t)
	r.inventoryStore = nil
	r.tickSentinel() // with no store there is no verdict
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

// testPVECfg gives the pin a CONCRETE target to demand in the message: a
// "WHERE" that says "the server" is no where at all.
var testPVECfg = pve.Config{BaseURL: "https://hypervisor.local:8006"}
