package api

import (
	"strings"
	"testing"

	"server-control-panel/internal/config"
	"server-control-panel/internal/inventory"
	"server-control-panel/internal/notify"
	"server-control-panel/internal/pve"
)

// routerSentinela builds a Router with an event collector in place of the
// notification router. NO test here sends a real message: what is asserted is
// WHICH event would go out, and when.
func routerSentinela(t *testing.T) (*Router, *[]notify.Event) {
	t.Helper()
	var vistos []notify.Event
	r := &Router{cfg: &config.Config{Primary: "sam"}}
	r.sentinelaSink = func(ev notify.Event) { vistos = append(vistos, ev) }
	return r, &vistos
}

func invOK() inventory.Inventory { return inventory.Inventory{LastPollError: ""} }
func invRuim() inventory.Inventory {
	return inventory.Inventory{LastPollError: "pve /api2/json/cluster/resources?type=vm: inalcancavel: dial tcp 198.51.100.20:8006: i/o timeout"}
}

// 🔴 TestSentinelaNaoAcreditaNoPrimeiroTick — a tick that fails is routine: the
// network wobbles, the hypervisor gets busy. Alerting on the first one is the
// permanent-red disease by another road.
func TestSentinelaNaoAcreditaNoPrimeiroTick(t *testing.T) {
	r, evs := routerSentinela(t)
	r.avaliaAlcance(invRuim(), 1000)
	r.avaliaAlcance(invRuim(), 1060)
	if len(*evs) != 0 {
		t.Fatalf("alerted before the 3 cycles: %v", *evs)
	}
	r.avaliaAlcance(invRuim(), 1120)
	if len(*evs) != 1 {
		t.Fatalf("on the third cycle it had to alert: %d events", len(*evs))
	}
	if (*evs)[0].Type != TipoHipervisorInalcancavel {
		t.Errorf("tipo = %q", (*evs)[0].Type)
	}
}

// 🔴 TestSentinelaAlertaNaBORDA — while the problem lasts, silence. An alarm
// that fires every cycle is not an alarm: it is a running tap, and it trains
// people to ignore it.
func TestSentinelaAlertaNaBorda(t *testing.T) {
	r, evs := routerSentinela(t)
	for i := 0; i < 20; i++ {
		r.avaliaAlcance(invRuim(), int64(1000+i*60))
	}
	if len(*evs) != 1 {
		t.Fatalf("🔴 %d events for ONE outage — the alarm turned into a running tap", len(*evs))
	}
}

// And the recovery is news too, exactly once.
func TestSentinelaAnunciaAVoltaUmaVezSo(t *testing.T) {
	r, evs := routerSentinela(t)
	for i := 0; i < 5; i++ {
		r.avaliaAlcance(invRuim(), int64(1000+i*60))
	}
	for i := 0; i < 5; i++ {
		r.avaliaAlcance(invOK(), int64(2000+i*60))
	}
	if len(*evs) != 2 {
		t.Fatalf("events = %d, want 2 (went down, came back): %v", len(*evs), tiposDe(*evs))
	}
	if (*evs)[1].Type != TipoHipervisorVoltou {
		t.Errorf("the second event = %q", (*evs)[1].Type)
	}
	if !strings.Contains((*evs)[1].Body, "WAS DOWN") {
		t.Errorf("the recovery does not say how long it was down: %s", (*evs)[1].Body)
	}
}

// A transient failure that clears BEFORE the threshold produces no event at all
// — neither a down nor a recovery. Alerting here would count a wobble as an incident.
func TestFalhaPassageiraNaoGeraNada(t *testing.T) {
	r, evs := routerSentinela(t)
	r.avaliaAlcance(invRuim(), 1000)
	r.avaliaAlcance(invRuim(), 1060)
	r.avaliaAlcance(invOK(), 1120)
	if len(*evs) != 0 {
		t.Fatalf("a wobble turned into an incident: %v", tiposDe(*evs))
	}
}

// 🔴 TestMensagemDizOQueOndeEPorQue — the operator's request, turned into a pin:
// "if it is going to raise an alert it has to say what is happening, where and
// why". A vague message is what he already had, and it is what made the channel
// lose its credibility.
func TestMensagemDizOQueOndeEPorQue(t *testing.T) {
	r, evs := routerSentinela(t)
	r.pveConfig = &pveCfgDeTeste
	for i := 0; i < 3; i++ {
		r.avaliaAlcance(invRuim(), int64(1000+i*60))
	}
	if len(*evs) != 1 {
		t.Fatalf("eventos = %d", len(*evs))
	}
	b := (*evs)[0].Body
	for _, exigido := range []string{"WHAT:", "WHERE:", "SINCE:", "WHY THIS IS SERIOUS:", "HOW TO CHECK:"} {
		if !strings.Contains(b, exigido) {
			t.Errorf("the message is missing %q:\n%s", exigido, b)
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

// 🔴 TestInventarioIlegivelNaoViraQuedaDaCasa — failing to read OUR OWN
// inventory is a defect of the VPS, not news about the house. Confusing the two
// would send the operator to look in the wrong place at three in the morning.
func TestInventarioIlegivelNaoViraQuedaDaCasa(t *testing.T) {
	r, evs := routerSentinela(t)
	r.inventoryStore = nil
	r.tickSentinela() // with no store there is no verdict
	if len(*evs) != 0 {
		t.Fatalf("missing inventory turned into an outage of the house: %v", tiposDe(*evs))
	}
}

func tiposDe(evs []notify.Event) []string {
	out := make([]string, 0, len(evs))
	for _, e := range evs {
		out = append(out, e.Type)
	}
	return out
}

// pveCfgDeTeste gives the pin a CONCRETE target to demand in the message: a
// "WHERE" that says "the server" is no where at all.
var pveCfgDeTeste = pve.Config{BaseURL: "https://hypervisor.local:8006"}
