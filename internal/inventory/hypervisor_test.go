package inventory

import (
	"context"
	"reflect"
	"testing"
	"time"

	"server-control-panel/internal/pve"
)

// hypervisor_test.go — the pins for the hypervisor's SEPARATE document.
//
// What these tests defend is a decision, not a function: the hypervisor's
// health is NOT a field of the Node. observedAtDoNo (freshness.go:64-70) looks
// only at Status and Uptime; hanging RAM off the Node would make the age of
// the `pve` node FREEZE while the card showed a fresh number — two ages
// disagreeing on the same screen. Separate document, separate timestamp,
// separate view.

func statusDeTeste() pve.NodeStatus {
	return pve.NodeStatus{
		Uptime:     123456,
		PVEVersion: "pve-manager/9.2.2/abcdef",
		LoadAvg:    []string{"1.14", "1.55", "1.70"},
		Memory:     pve.MemInfo{Total: 67200000000, Used: 40100000000, Free: 27100000000},
		Swap:       pve.MemInfo{Total: 8000000000, Used: 1000000},
		RootFS:     pve.RootFSInfo{Total: 100000000000, Used: 20000000000, Avail: 80000000000},
		KSM:        pve.KSMInfo{Shared: 4096},
	}
}

// 🔴 TestIdadeDoHipervisorEIndependenteDaDosNos is the pin for the
// age-hitching trap, in BOTH directions: advancing only the nodes makes the
// hypervisor's age grow, and a freshly stamped hypervisor rejuvenates no node
// at all.
func TestIdadeDoHipervisorEIndependenteDaDosNos(t *testing.T) {
	f := &fakePVE{recursos: recursosDeTeste(), status: statusDeTeste()}
	p, st, rel := novoPollerDeTeste(t, f, Sources{}, PollerConfig{})
	ttl := 90 * time.Second

	if err := p.tick(context.Background()); err != nil {
		t.Fatalf("tick 1: %v", err)
	}
	inv, _ := st.Snapshot()
	if v := ViewHypervisor(inv.Hypervisor, ttl, rel.now()); v.AgeSeconds != 0 {
		t.Fatalf("right after the tick the hypervisor age is %d, want 0", v.AgeSeconds)
	}

	// Direction 1: /status stops answering, discovery carries on. The nodes move
	// forward; the hypervisor must NOT move forward with them.
	rel.avanca(300 * time.Second)
	f.mu.Lock()
	f.erroStatus = context.DeadlineExceeded
	f.mu.Unlock()
	if err := p.tick(context.Background()); err != nil {
		t.Fatalf("tick 2: %v", err)
	}
	inv, _ = st.Snapshot()
	vh := ViewHypervisor(inv.Hypervisor, ttl, rel.now())
	if vh.AgeSeconds != 300 {
		t.Errorf("hypervisor age = %d, want 300 — it is riding on the nodes' timestamp (A-5)", vh.AgeSeconds)
	}
	if !vh.Stale {
		t.Errorf("a hypervisor at 300 s with a 90 s TTL is not expired — the panel would say 'live' about 5-minute-old data")
	}
	for _, n := range View(inv, ttl, rel.now()) {
		if n.Kind == NodeKindGuest && n.AgeSeconds != 0 {
			t.Errorf("node %s with age %d — the /status failure contaminated discovery", n.ID, n.AgeSeconds)
		}
	}

	// Direction 2 (the inverse), measured straight on the documents: a brand-new
	// hypervisor and old nodes coexist, each with its OWN age.
	agora := time.Unix(1800000000, 0)
	misto := Inventory{
		Hypervisor: Hypervisor{Node: "pve", Uptime: Observe(int64(1), agora.Unix())},
		Nodes: []Node{{
			ID: "lxc/207", Name: "apps", Kind: NodeKindGuest, Transport: TransportPVEAPI,
			Status: Observe("running", agora.Unix()-500),
		}},
	}
	if v := ViewHypervisor(misto.Hypervisor, ttl, agora); v.AgeSeconds != 0 {
		t.Errorf("hypervisor age = %d, want 0", v.AgeSeconds)
	}
	if v := View(misto, ttl, agora); v[0].AgeSeconds != 500 {
		t.Errorf("node age = %d, want 500 — the hypervisor timestamp rejuvenated the node", v[0].AgeSeconds)
	}
}

// TestFalhaDoStatusNaoApagaOHipervisor is invariant 2 of the poller applied to
// the new document: a failure does NOT erase. Silent amnesia is worse than old
// data — the screen has to say "data from 5 min ago", not "I know nothing
// about the hypervisor".
func TestFalhaDoStatusNaoApagaOHipervisor(t *testing.T) {
	f := &fakePVE{recursos: recursosDeTeste(), status: statusDeTeste()}
	p, st, rel := novoPollerDeTeste(t, f, Sources{}, PollerConfig{})

	if err := p.tick(context.Background()); err != nil {
		t.Fatalf("tick 1: %v", err)
	}
	rel.avanca(60 * time.Second)
	f.mu.Lock()
	f.erroStatus = context.DeadlineExceeded
	f.mu.Unlock()
	if err := p.tick(context.Background()); err != nil {
		t.Fatalf("tick 2 returned an error (%v) — a /status failure is news for the log, not a tick failure", err)
	}

	inv, _ := st.Snapshot()
	h := inv.Hypervisor
	if h.MemUsed.Value != 40100000000 {
		t.Errorf("MemUsed = %d, want the last known value (40100000000)", h.MemUsed.Value)
	}
	if h.Version.Value != "pve-manager/9.2.2/abcdef" {
		t.Errorf("Version = %q, want the last known value", h.Version.Value)
	}
	if h.Node != "pve" {
		t.Errorf("Node = %q, want 'pve'", h.Node)
	}
	if h.MemUsed.ObservedAt != 1800000000 {
		t.Errorf("timestamp = %d, want the OLD one (1800000000) — a fresh timestamp over a stale value is exactly the lie criterion 4 forbids", h.MemUsed.ObservedAt)
	}
	if nos := nosPorID(t, st); nos["lxc/207"].Status.ObservedAt != rel.now().Unix() {
		t.Errorf("the node did not advance (%d) — the hypervisor failure froze discovery", nos["lxc/207"].Status.ObservedAt)
	}
}

// 🔴 TestNomeDoHipervisorVemDaDescoberta is invariant 3 of the poller: no
// hand-written hostname. The name comes out of what /cluster/resources
// returned, which is what makes a renamed hypervisor (or a second one) show up
// on its own instead of waiting for somebody to edit code.
func TestNomeDoHipervisorVemDaDescoberta(t *testing.T) {
	recursos := []pve.Resource{
		{ID: "lxc/207", VMID: 207, Name: "apps", Node: "hipervisor-renomeado", Type: "lxc", Status: "running"},
	}
	f := &fakePVE{recursos: recursos, status: statusDeTeste()}
	p, st, _ := novoPollerDeTeste(t, f, Sources{}, PollerConfig{})

	if err := p.tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	inv, _ := st.Snapshot()
	if inv.Hypervisor.Node != "hipervisor-renomeado" {
		t.Errorf("Hypervisor.Node = %q, want 'hipervisor-renomeado' (name hard-coded in the code)", inv.Hypervisor.Node)
	}
	f.mu.Lock()
	visto := f.nodeStatusNome
	f.mu.Unlock()
	if visto != "hipervisor-renomeado" {
		t.Errorf("NodeStatus was called with %q — the node queried is not the one discovery pointed at", visto)
	}
}

// TestNomeDoHipervisorEDeterministico: with more than one host in the
// response, the tick ALWAYS picks the same one (the smallest in string order).
// Without that, two consecutive ticks would write different documents and the
// file's diff would turn into noise.
func TestNomeDoHipervisorEDeterministico(t *testing.T) {
	recursos := []pve.Resource{
		{ID: "lxc/207", VMID: 207, Name: "apps", Node: "zeta", Type: "lxc", Status: "running"},
		{ID: "lxc/208", VMID: 208, Name: "dev", Node: "alfa", Type: "lxc", Status: "running"},
	}
	for i := 0; i < 5; i++ {
		if got := nomeDoHipervisor(recursos); got != "alfa" {
			t.Fatalf("nomeDoHipervisor = %q, want 'alfa' on EVERY run", got)
		}
	}
}

// 🔴 TestTodoCampoDoHipervisorTemCarimbo: the timestamp being mandatory is
// structural, not a matter of discipline. A raw field added in the future (a
// `MemFree int64` "just for the screen") fails here — it is the only way for
// "an old number displayed as live" to stay impossible when nobody is looking.
func TestTodoCampoDoHipervisorTemCarimbo(t *testing.T) {
	tp := reflect.TypeOf(Hypervisor{})
	for i := 0; i < tp.NumField(); i++ {
		f := tp.Field(i)
		if f.Name == "Node" {
			// Identity, not measurement: the hypervisor's name does not age.
			if f.Type.Kind() != reflect.String {
				t.Errorf("Node stopped being a string (%s)", f.Type)
			}
			continue
		}
		obs, ok := f.Type.FieldByName("ObservedAt")
		if !ok || obs.Type.Kind() != reflect.Int64 {
			t.Errorf("field %s (%s) is not Observed[T] — a number with no timestamp is a number that lies", f.Name, f.Type)
			continue
		}
		if _, ok := f.Type.FieldByName("Value"); !ok {
			t.Errorf("field %s (%s) has no Value", f.Name, f.Type)
		}
	}
}

// TestHypervisorNuncaObservadoDizIsso: an age of -1 is the marker for "never
// observed", and it must NOT become 0. "0 s ago" reads as just-seen.
func TestHypervisorNuncaObservadoDizIsso(t *testing.T) {
	v := ViewHypervisor(Hypervisor{}, 90*time.Second, time.Unix(1800000000, 0))
	if v.AgeSeconds != -1 {
		t.Errorf("AgeSeconds = %d, want -1 (never observed)", v.AgeSeconds)
	}
	if !v.Stale {
		t.Error("never observed has to count as expired")
	}
}

// TestLoadEntraComoNumero: the hypervisor sends ["1.14","1.55","1.70"]
// (strings). Storing the string forces the screen to convert — and the screen
// is precisely where no data logic is allowed.
func TestLoadEntraComoNumero(t *testing.T) {
	var inv Inventory
	aplicaHipervisor(&inv, "pve", statusDeTeste(), 1800000000)
	quer := [3]float64{1.14, 1.55, 1.70}
	if inv.Hypervisor.Load.Value != quer {
		t.Errorf("Load = %v, want %v", inv.Hypervisor.Load.Value, quer)
	}
	// A short or unreadable loadavg must not bring down the rest of the health.
	st := statusDeTeste()
	st.LoadAvg = []string{"nao-e-numero"}
	var inv2 Inventory
	aplicaHipervisor(&inv2, "pve", st, 1800000000)
	if inv2.Hypervisor.MemUsed.Value != 40100000000 {
		t.Error("an unreadable loadavg took down the rest of the hypervisor health")
	}
}
