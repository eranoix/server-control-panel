package inventory

import (
	"context"
	"reflect"
	"testing"
	"time"

	"server-control-panel/internal/pve"
)

func testStatus() pve.NodeStatus {
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

func TestHypervisorAgeIndependentOfNodes(t *testing.T) {
	f := &fakePVE{resources: testResources(), status: testStatus()}
	p, st, rel := newTestPoller(t, f, Sources{}, PollerConfig{})
	ttl := 90 * time.Second

	if err := p.tick(context.Background()); err != nil {
		t.Fatalf("tick 1: %v", err)
	}
	inv, _ := st.Snapshot()
	if v := ViewHypervisor(inv.Hypervisor, ttl, rel.now()); v.AgeSeconds != 0 {
		t.Fatalf("right after the tick the hypervisor age is %d, want 0", v.AgeSeconds)
	}

	rel.advance(300 * time.Second)
	f.mu.Lock()
	f.statusErr = context.DeadlineExceeded
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

	now := time.Unix(1800000000, 0)
	mixed := Inventory{
		Hypervisor: Hypervisor{Node: "pve", Uptime: Observe(int64(1), now.Unix())},
		Nodes: []Node{{
			ID: "lxc/207", Name: "apps", Kind: NodeKindGuest, Transport: TransportPVEAPI,
			Status: Observe("running", now.Unix()-500),
		}},
	}
	if v := ViewHypervisor(mixed.Hypervisor, ttl, now); v.AgeSeconds != 0 {
		t.Errorf("hypervisor age = %d, want 0", v.AgeSeconds)
	}
	if v := View(mixed, ttl, now); v[0].AgeSeconds != 500 {
		t.Errorf("node age = %d, want 500 — the hypervisor timestamp rejuvenated the node", v[0].AgeSeconds)
	}
}

func TestStatusFailureKeepsHypervisor(t *testing.T) {
	f := &fakePVE{resources: testResources(), status: testStatus()}
	p, st, rel := newTestPoller(t, f, Sources{}, PollerConfig{})

	if err := p.tick(context.Background()); err != nil {
		t.Fatalf("tick 1: %v", err)
	}
	rel.advance(60 * time.Second)
	f.mu.Lock()
	f.statusErr = context.DeadlineExceeded
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
	if nodes := nodesByID(t, st); nodes["lxc/207"].Status.ObservedAt != rel.now().Unix() {
		t.Errorf("the node did not advance (%d) — the hypervisor failure froze discovery", nodes["lxc/207"].Status.ObservedAt)
	}
}

func TestHypervisorNameComesFromDiscovery(t *testing.T) {
	resources := []pve.Resource{
		{ID: "lxc/207", VMID: 207, Name: "apps", Node: "renamed-hypervisor", Type: "lxc", Status: "running"},
	}
	f := &fakePVE{resources: resources, status: testStatus()}
	p, st, _ := newTestPoller(t, f, Sources{}, PollerConfig{})

	if err := p.tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	inv, _ := st.Snapshot()
	if inv.Hypervisor.Node != "renamed-hypervisor" {
		t.Errorf("Hypervisor.Node = %q, want 'renamed-hypervisor' (name hard-coded in the code)", inv.Hypervisor.Node)
	}
	f.mu.Lock()
	seen := f.nodeStatusName
	f.mu.Unlock()
	if seen != "renamed-hypervisor" {
		t.Errorf("NodeStatus was called with %q — the node queried is not the one discovery pointed at", seen)
	}
}

func TestHypervisorNameIsDeterministic(t *testing.T) {
	resources := []pve.Resource{
		{ID: "lxc/207", VMID: 207, Name: "apps", Node: "zeta", Type: "lxc", Status: "running"},
		{ID: "lxc/208", VMID: 208, Name: "dev", Node: "alpha", Type: "lxc", Status: "running"},
	}
	for i := 0; i < 5; i++ {
		if got := hypervisorName(resources); got != "alpha" {
			t.Fatalf("hypervisorName = %q, want 'alpha' on EVERY run", got)
		}
	}
}

func TestEveryHypervisorFieldIsStamped(t *testing.T) {
	tp := reflect.TypeOf(Hypervisor{})
	for i := 0; i < tp.NumField(); i++ {
		f := tp.Field(i)
		if f.Name == "Node" {
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

func TestHypervisorNeverObservedSaysSo(t *testing.T) {
	v := ViewHypervisor(Hypervisor{}, 90*time.Second, time.Unix(1800000000, 0))
	if v.AgeSeconds != -1 {
		t.Errorf("AgeSeconds = %d, want -1 (never observed)", v.AgeSeconds)
	}
	if !v.Stale {
		t.Error("never observed has to count as expired")
	}
}

func TestLoadParsedAsNumber(t *testing.T) {
	var inv Inventory
	applyHypervisor(&inv, "pve", testStatus(), 1800000000)
	want := [3]float64{1.14, 1.55, 1.70}
	if inv.Hypervisor.Load.Value != want {
		t.Errorf("Load = %v, want %v", inv.Hypervisor.Load.Value, want)
	}
	st := testStatus()
	st.LoadAvg = []string{"not-a-number"}
	var inv2 Inventory
	applyHypervisor(&inv2, "pve", st, 1800000000)
	if inv2.Hypervisor.MemUsed.Value != 40100000000 {
		t.Error("an unreadable loadavg took down the rest of the hypervisor health")
	}
}
