package inventory

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"server-control-panel/internal/pve"
)

// fakePVE is the fake hypervisor. No mock framework: the repo does not use one,
// and a two-method interface does not justify introducing one.
type fakePVE struct {
	mu        sync.Mutex
	recursos  []pve.Resource
	erro      error
	enderecos map[int]string
	erroAddr  map[int]error
	atraso    time.Duration
	chamadas  int32

	// The health of the hypervisor. erroStatus injects the failure that invariant 2
	// requires handling without erasing anything; nodeStatusNome records WITH WHICH
	// NAME the poller asked — it is the pin for invariant 3.
	status         pve.NodeStatus
	erroStatus     error
	nodeStatusNome string
	chamadasStatus int32
}

func (f *fakePVE) NodeStatus(ctx context.Context, node string) (pve.NodeStatus, error) {
	atomic.AddInt32(&f.chamadasStatus, 1)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nodeStatusNome = node
	if f.erroStatus != nil {
		return pve.NodeStatus{}, f.erroStatus
	}
	return f.status, nil
}

// The three remaining routes of the interface. In this double they answer EMPTY
// and without error: the tests in this file are about discovery, health and
// credentials, and their behaviour has a double of its own in
// poller_storage_test.go — two doubles with separate responsibilities instead
// of one with ten fields.
func (f *fakePVE) StorageList(ctx context.Context, node string) ([]pve.Storage, error) {
	return nil, nil
}
func (f *fakePVE) ZFSList(ctx context.Context, node string) ([]pve.ZPool, error) { return nil, nil }
func (f *fakePVE) Permissions(ctx context.Context) (map[string]map[string]int, error) {
	return nil, nil
}

func (f *fakePVE) ClusterResources(ctx context.Context) ([]pve.Resource, error) {
	atomic.AddInt32(&f.chamadas, 1)
	if f.atraso > 0 {
		select {
		case <-time.After(f.atraso):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.erro != nil {
		return nil, f.erro
	}
	out := make([]pve.Resource, len(f.recursos))
	copy(out, f.recursos)
	return out, nil
}

func (f *fakePVE) GuestAddress(ctx context.Context, node string, vmid int, typ string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err, ok := f.erroAddr[vmid]; ok {
		return "", err
	}
	return f.enderecos[vmid], nil
}

// relogioFixo is the injected clock: no criterion may be satisfied by waiting
// for it, so time advances by function call, not by sleep.
type relogioFixo struct {
	mu sync.Mutex
	t  time.Time
}

func (r *relogioFixo) now() time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.t
}

func (r *relogioFixo) avanca(d time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.t = r.t.Add(d)
}

func recursosDeTeste() []pve.Resource {
	return []pve.Resource{
		{ID: "lxc/207", VMID: 207, Name: "apps", Node: "pve", Type: "lxc", Status: "running", Uptime: 60826},
		{ID: "qemu/208", VMID: 208, Name: "dev", Node: "pve", Type: "qemu", Status: "running", Uptime: 660828},
	}
}

func novoPollerDeTeste(t *testing.T, f *fakePVE, deps Sources, cfg PollerConfig) (*Poller, *Store, *relogioFixo) {
	t.Helper()
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	rel := &relogioFixo{t: time.Unix(1800000000, 0)}
	if cfg.Now == nil {
		cfg.Now = rel.now
	}
	return NewPoller(st, f, deps, cfg), st, rel
}

func nosPorID(t *testing.T, st *Store) map[string]Node {
	t.Helper()
	inv, err := st.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	m := map[string]Node{}
	for _, n := range inv.Nodes {
		m[n.ID] = n
	}
	return m
}

// 🔴 TestPollerStampsOnServer is the pin for the server-side timestamp: it comes
// from the SERVER clock, injected, at the instant of the tick. A zero timestamp
// would be "never observed" and the screen would show age -1 for a node that
// has just answered.
func TestPollerStampsOnServer(t *testing.T) {
	f := &fakePVE{recursos: recursosDeTeste(), enderecos: map[int]string{207: "192.168.100.47"}}
	p, st, rel := novoPollerDeTeste(t, f, Sources{}, PollerConfig{})

	if err := p.tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	nos := exigeConjunto(t, st, conjuntoPVE()...)
	quer := rel.now().Unix()
	// The host comes in through the SAME door: it answered the API at this instant.
	if nos["node/pve"].Kind != NodeKindHost || nos["node/pve"].Status.ObservedAt != quer {
		t.Errorf("host = %+v, want kind=host timestamped at %d", nos["node/pve"], quer)
	}
	for _, id := range []string{"lxc/207", "qemu/208"} {
		n, ok := nos[id]
		if !ok {
			t.Fatalf("node %s missing from the inventory (%v)", id, nos)
		}
		if n.Status.ObservedAt != quer {
			t.Errorf("%s: observed_at = %d, want %d (the SERVER's clock)", id, n.Status.ObservedAt, quer)
		}
		if n.Status.Value == "" {
			t.Errorf("%s: empty status", id)
		}
	}
	if got := nos["lxc/207"].Address; got != "192.168.100.47" {
		t.Errorf("address = %q, want 192.168.100.47", got)
	}
	// DHCP: no declared address is NOT an error, nor a broken node.
	if got := nos["qemu/208"].Address; got != "" {
		t.Errorf("address of the guest with no ip = %q, want empty", got)
	}

	// Second tick with the clock moved forward: the timestamp ADVANCES.
	rel.avanca(30 * time.Second)
	if err := p.tick(context.Background()); err != nil {
		t.Fatalf("tick 2: %v", err)
	}
	if got := nosPorID(t, st)["lxc/207"].Status.ObservedAt; got != rel.now().Unix() {
		t.Fatalf("observed_at did not advance on the 2nd tick: %d, want %d", got, rel.now().Unix())
	}
}

// TestPollerTickTimeout: a hypervisor that does not answer must not stall the
// poller. The hypervisor delays EVERY 401 response by 3 s on
// purpose; with no per-tick timeout, a handful of invalid nodes makes the cycle
// never close.
func TestPollerTickTimeout(t *testing.T) {
	f := &fakePVE{recursos: recursosDeTeste(), atraso: 5 * time.Second}
	p, st, _ := novoPollerDeTeste(t, f, Sources{}, PollerConfig{Timeout: 50 * time.Millisecond})

	inicio := time.Now()
	err := p.tick(context.Background())
	levou := time.Since(inicio)
	if err == nil {
		t.Fatal("a tick that blew the timeout returned success")
	}
	if levou > 2*time.Second {
		t.Fatalf("the tick took %v — the 50ms timeout was not respected", levou)
	}
	// TOTAL discovery failure does NOT erase the inventory nor create an empty node.
	inv, _ := st.Snapshot()
	if len(inv.Nodes) != 0 {
		t.Fatalf("a failed tick created nodes: %+v", inv.Nodes)
	}

	// And the NEXT tick runs: the poller did not get stuck.
	f.mu.Lock()
	f.atraso = 0
	f.mu.Unlock()
	if err := p.tick(context.Background()); err != nil {
		t.Fatalf("the next tick failed: %v", err)
	}
	exigeConjunto(t, st, conjuntoPVE()...)
}

// 🔴 TestPollerFalhaTotalPreservaNos: PVE being down must NOT zero the
// inventory. The node stays there with the OLD timestamp — that is how the screen says
// "data from X min ago" instead of lying "there is no node at all".
func TestPollerFalhaTotalPreservaNos(t *testing.T) {
	f := &fakePVE{recursos: recursosDeTeste()}
	p, st, rel := novoPollerDeTeste(t, f, Sources{}, PollerConfig{})
	if err := p.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	carimboAntigo := nosPorID(t, st)["lxc/207"].Status.ObservedAt

	f.mu.Lock()
	f.erro = errors.New("hipervisor inalcançável")
	f.mu.Unlock()
	rel.avanca(5 * time.Minute)
	if err := p.tick(context.Background()); err == nil {
		t.Fatal("a total PVE failure returned success")
	}

	nos := exigeConjunto(t, st, conjuntoPVE()...)
	if got := nos["lxc/207"].Status.ObservedAt; got != carimboAntigo {
		t.Fatalf("observed_at = %d, want the OLD %d (a timestamp cannot advance without an observation)", got, carimboAntigo)
	}
}

// TestPollerPartialFailure: a guest whose address fails must not contaminate the
// others. The fan-out isolates per node — a partial failure never turns into a
// frozen inventory.
func TestPollerPartialFailure(t *testing.T) {
	f := &fakePVE{
		recursos:  recursosDeTeste(),
		enderecos: map[int]string{208: "192.168.100.48"},
		erroAddr:  map[int]error{207: errors.New("config ilegível")},
	}
	p, st, rel := novoPollerDeTeste(t, f, Sources{}, PollerConfig{})
	if err := p.tick(context.Background()); err != nil {
		t.Fatalf("a partial failure took down the whole tick: %v", err)
	}
	nos := exigeConjunto(t, st, conjuntoPVE()...)
	// The healthy guest has an address and the timestamp of the tick.
	if nos["qemu/208"].Address != "192.168.100.48" {
		t.Errorf("a healthy guest was left with no address: %+v", nos["qemu/208"])
	}
	if nos["qemu/208"].Status.ObservedAt != rel.now().Unix() {
		t.Errorf("a healthy guest was not timestamped")
	}
	// The guest with an unreadable address is STILL observed — what failed was the
	// address, not its existence.
	if nos["lxc/207"].Status.ObservedAt != rel.now().Unix() {
		t.Errorf("a guest with an unreadable address lost its status timestamp")
	}
	if nos["lxc/207"].Address != "" {
		t.Errorf("address = %q, want empty", nos["lxc/207"].Address)
	}
}

// TestPollerRunRespeitaCtx: Run() is a loop; it has to finish when the ctx
// dies, and it must not panic and take the panel process down with it.
func TestPollerRunRespeitaCtx(t *testing.T) {
	f := &fakePVE{recursos: recursosDeTeste()}
	p, st, _ := novoPollerDeTeste(t, f, Sources{}, PollerConfig{Interval: 5 * time.Millisecond})

	ctx, cancel := context.WithCancel(context.Background())
	fim := make(chan struct{})
	go func() { p.Run(ctx); close(fim) }()

	// Wait for the first tick to happen, without sleeping a fixed amount.
	prazo := time.Now().Add(3 * time.Second)
	for time.Now().Before(prazo) && atomic.LoadInt32(&f.chamadas) == 0 {
		time.Sleep(time.Millisecond)
	}
	if atomic.LoadInt32(&f.chamadas) == 0 {
		t.Fatal("Run did not execute a single tick")
	}
	cancel()
	select {
	case <-fim:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop with the ctx cancelled")
	}
	if len(nosPorID(t, st)) == 0 {
		t.Fatal("Run ran but wrote nothing")
	}
}

// TestPollerPanicoNaoDerrubaOLaco: a source that panics must not kill the
// poller goroutine — the whole panel would be left with no inventory until the
// next restart, in silence.
func TestPollerPanicoNaoDerrubaOLaco(t *testing.T) {
	var n int32
	deps := Sources{Jobs: func() ([]JobRef, error) {
		if atomic.AddInt32(&n, 1) == 1 {
			panic("fonte de jobs explodiu")
		}
		return nil, nil
	}}
	f := &fakePVE{recursos: recursosDeTeste()}
	p, st, _ := novoPollerDeTeste(t, f, deps, PollerConfig{})

	if err := p.tick(context.Background()); err == nil {
		t.Fatal("a tick with a panic returned success — the error has to surface")
	}
	if err := p.tick(context.Background()); err != nil {
		t.Fatalf("the tick after the panic failed: %v", err)
	}
	exigeConjunto(t, st, conjuntoPVE()...)
}

// ordenados is the helper for SET assertions — never counting.
func ordenados(nos map[string]Node) []string {
	var ids []string
	for id := range nos {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func fmtIDs(ids []string) string { return fmt.Sprint(ids) }

// exigeConjunto is the default assertion here: SET, never count.
// `len(nos) == 3` would turn a new guest into a failure and a vanished guest
// into a wrong pass; the set says exactly WHO came in or went out.
func exigeConjunto(t *testing.T, st *Store, quer ...string) map[string]Node {
	t.Helper()
	nos := nosPorID(t, st)
	sort.Strings(quer)
	if got := fmtIDs(ordenados(nos)); got != fmtIDs(quer) {
		t.Fatalf("set of nodes = %s, want %s", got, fmtIDs(quer))
	}
	return nos
}

// conjuntoPVE is what a successful tick over recursosDeTeste() produces: the two
// guests PLUS the hypervisor itself, derived from the `node` field of each row
// of /cluster/resources (no hostname hand-written in the poller).
func conjuntoPVE() []string { return []string{"node/pve", "lxc/207", "qemu/208"} }

// 🔴 TestDiscoveryAddsUnknownGuest is the headline requirement turned into a
// test: "a new guest shows up in the inventory WITHOUT anyone editing JSON by
// hand".
//
// The second tick uses the SAME configuration, the SAME store and the SAME
// poller — nothing is edited between them. The only new fact is that the
// hypervisor started returning `lxc/299 surpresa`. If this test passed with the
// guest missing, auto-discovery would be decorative and the inventory would go
// back to being a hand-written JSON — which is exactly what auto-discovery
// exists to end.
func TestDiscoveryAddsUnknownGuest(t *testing.T) {
	f := &fakePVE{recursos: recursosDeTeste()}
	p, st, rel := novoPollerDeTeste(t, f, Sources{}, PollerConfig{})

	if err := p.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	exigeConjunto(t, st, conjuntoPVE()...)

	// The hypervisor gained a guest. NOTHING else changed.
	f.mu.Lock()
	f.recursos = append(f.recursos, pve.Resource{
		ID: "lxc/299", VMID: 299, Name: "surpresa", Node: "pve", Type: "lxc", Status: "running", Uptime: 42,
	})
	f.mu.Unlock()
	rel.avanca(30 * time.Second)

	if err := p.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	nos := exigeConjunto(t, st, append(conjuntoPVE(), "lxc/299")...)

	novo := nos["lxc/299"]
	if novo.Name != "surpresa" || novo.Kind != NodeKindGuest || novo.VMID != 299 {
		t.Fatalf("malformed new guest: %+v", novo)
	}
	if novo.Status.ObservedAt != rel.now().Unix() {
		t.Fatalf("a new guest with no timestamp: %+v", novo)
	}
	if novo.Transport != TransportPVEAPI {
		t.Fatalf("transport = %q, want pve-api", novo.Transport)
	}
}

// 🔴 TestDiscoveryRemovalKeepsHistory: a guest that vanishes from the hypervisor
// is NOT deleted. It keeps the old timestamp and freshness marks it stale —
// "not seen for 5 min" is information; disappearing from the list is amnesia presented as
// truth.
func TestDiscoveryRemovalKeepsHistory(t *testing.T) {
	f := &fakePVE{recursos: recursosDeTeste()}
	p, st, rel := novoPollerDeTeste(t, f, Sources{}, PollerConfig{TTL: 90 * time.Second})
	if err := p.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	carimboAntigo := nosPorID(t, st)["lxc/207"].Status.ObservedAt

	// The guest vanished from the hypervisor (powered off, migrated or ACL removed).
	f.mu.Lock()
	f.recursos = f.recursos[1:]
	f.mu.Unlock()
	rel.avanca(5 * time.Minute)
	if err := p.tick(context.Background()); err != nil {
		t.Fatal(err)
	}

	nos := exigeConjunto(t, st, conjuntoPVE()...)
	sumido := nos["lxc/207"]
	if sumido.Status.ObservedAt != carimboAntigo {
		t.Fatalf("timestamp of the vanished one = %d, want the OLD %d", sumido.Status.ObservedAt, carimboAntigo)
	}
	// And freshness has to see it as stale, without anyone waiting 5 min.
	vistas := View(Inventory{Nodes: []Node{sumido}}, 90*time.Second, rel.now())
	if !vistas[0].Stale || vistas[0].AgeSeconds != 300 {
		t.Fatalf("view of the vanished one = %+v, want stale with 300s", vistas[0])
	}
	// What still shows up advanced normally.
	if nos["qemu/208"].Status.ObservedAt != rel.now().Unix() {
		t.Fatal("the guest that is present did not advance its timestamp")
	}
}

// TestNodeTransport pins the transport enum at both ends: whatever comes from
// the hypervisor is "pve-api"; a seed declares its own; a value outside the set
// is refused by the model.
func TestNodeTransport(t *testing.T) {
	seeds := []Node{{ID: "canario", Name: "canario", Transport: TransportAgente, Address: "127.0.0.1:9", Kind: NodeKindExterno}}
	f := &fakePVE{recursos: recursosDeTeste()}
	deps := Sources{
		Seeds:     func() ([]Node, error) { return seeds, nil },
		AgentPing: func(ctx context.Context, addr string) error { return errors.New("recusada") },
	}
	p, st, _ := novoPollerDeTeste(t, f, deps, PollerConfig{})
	if err := p.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	nos := exigeConjunto(t, st, append(conjuntoPVE(), "canario")...)
	for _, id := range conjuntoPVE() {
		if nos[id].Transport != TransportPVEAPI {
			t.Errorf("%s: transport = %q, want pve-api", id, nos[id].Transport)
		}
	}
	if nos["canario"].Transport != TransportAgente {
		t.Errorf("the seed lost the transport: %+v", nos["canario"])
	}
	// The enum is closed: anyone inventing a fourth value is refused at validation.
	if err := (Node{ID: "x", Transport: "telnet", Kind: NodeKindExterno}).Validate(); err == nil {
		t.Error("a transport outside the enum was accepted")
	}
}

// TestAgregaReferencias: Projects/Deployments/Jobs/Services come in on the SAME
// tick, from injected sources. The inventory references; it does not execute —
// JobRef points at queue.Job (queue.go:55) and scheduler.Job (scheduler.go:30),
// and duplicating an executor here would be the worst regression possible.
func TestAgregaReferencias(t *testing.T) {
	deps := Sources{
		Projects: func() ([]Project, []Deployment, error) {
			return []Project{{ID: "css-lee", Name: "Acme Booking"}},
				[]Deployment{{ID: "d1", ProjectID: "css-lee", NodeID: "lxc/207", Branch: "main"}}, nil
		},
		Jobs: func() ([]JobRef, error) {
			return []JobRef{{ID: "j_1", Kind: "queue", NodeID: "lxc/207", Source: "user"}}, nil
		},
		Services: func() ([]Service, error) {
			return []Service{{ID: "s1", NodeID: "lxc/207", Name: "painel", Unit: "vps-manager.service"}}, nil
		},
	}
	f := &fakePVE{recursos: recursosDeTeste()}
	p, st, _ := novoPollerDeTeste(t, f, deps, PollerConfig{})
	if err := p.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	inv, _ := st.Snapshot()
	if len(inv.Projects) != 1 || inv.Projects[0].ID != "css-lee" {
		t.Errorf("projects = %+v", inv.Projects)
	}
	if len(inv.Deployments) != 1 || inv.Deployments[0].NodeID != "lxc/207" {
		t.Errorf("deployments = %+v", inv.Deployments)
	}
	if len(inv.Jobs) != 1 || inv.Jobs[0].Kind != "queue" || inv.Jobs[0].Source != "user" {
		t.Errorf("jobs = %+v", inv.Jobs)
	}
	if len(inv.Services) != 1 || inv.Services[0].Unit != "vps-manager.service" {
		t.Errorf("services = %+v", inv.Services)
	}
	if inv.SchemaVersion != SchemaVersion {
		t.Errorf("schema_version = %d, want %d", inv.SchemaVersion, SchemaVersion)
	}
}

// TestCredencialRevogadaSoParaQuemNaoVenceu is the revoked-versus-expired
// trap on the loop side: the hypervisor's 401 is INDISTINGUISHABLE between a revoked token and an expired
// one. Marking everything "revoked" would erase the only clue that the problem
// is the calendar.
func TestCredencialRevogadaSoParaQuemNaoVenceu(t *testing.T) {
	f := &fakePVE{recursos: recursosDeTeste()}
	p, st, rel := novoPollerDeTeste(t, f, Sources{}, PollerConfig{})
	if err := p.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	agora := rel.now().Unix()
	// One token that expired yesterday, another valid for another month.
	if err := st.Replace(func(inv *Inventory) {
		for i := range inv.Nodes {
			switch inv.Nodes[i].ID {
			case "lxc/207":
				inv.Nodes[i].Credential = Credential{TokenID: "lab@pve!a", Expire: agora - 86400}
			case "qemu/208":
				inv.Nodes[i].Credential = Credential{TokenID: "lab@pve!b", Expire: agora + 30*86400}
			}
		}
	}); err != nil {
		t.Fatal(err)
	}

	f.mu.Lock()
	f.erro = &pve.Error{Kind: pve.KindNoCredential, Status: 401, Path: "/cluster/resources"}
	f.mu.Unlock()
	if err := p.tick(context.Background()); err == nil {
		t.Fatal("401 returned success")
	}

	nos := nosPorID(t, st)
	if got := nos["qemu/208"].Credential.State; got != CredRevogada {
		t.Errorf("a valid token that took a 401: state = %q, want %q", got, CredRevogada)
	}
	if got := nos["lxc/207"].Credential.State; got == CredRevogada {
		t.Errorf("an ALREADY EXPIRED token was marked revogada — the clue that this is a calendar matter disappears")
	}
	// And the final read for the screen, which is what resolves the state:
	vistas := View(Inventory{Nodes: []Node{nos["lxc/207"]}}, time.Minute, rel.now())
	if vistas[0].Credential.State != CredExpirada {
		t.Errorf("view of the expired one = %q, want %q", vistas[0].Credential.State, CredExpirada)
	}
}

// TestSemErroDeCredencialNaoMarcaNada: a transport error (unreachable node) is
// mute about the credential. Merging "I could not talk to it" with "the
// credential died" would make the screen ask for a revocation because of a
// loose cable.
func TestSemErroDeCredencialNaoMarcaNada(t *testing.T) {
	f := &fakePVE{recursos: recursosDeTeste()}
	p, st, _ := novoPollerDeTeste(t, f, Sources{}, PollerConfig{})
	if err := p.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.erro = &pve.Error{Kind: pve.KindUnreachable, Path: "/cluster/resources"}
	f.mu.Unlock()
	if err := p.tick(context.Background()); err == nil {
		t.Fatal("a transport error returned success")
	}
	for id, n := range nosPorID(t, st) {
		if n.Credential.State == CredRevogada {
			t.Errorf("%s marked revogada because of a TRANSPORT error", id)
		}
	}
}

// 🔴 TestSeedRedeclaradoAtualiza closes a blind spot found by mutation testing:
// the transport test only exercised a NEW seed, so removing the update of the
// declarative fields of an already existing seed went unnoticed.
//
// The real case is banal and frequent: the operator fixes the address (or the
// transport) of a node in seeds.json. Without this assertion the panel would go
// on dialling the old address forever — and the versioned file would say one
// thing while the inventory did another.
func TestSeedRedeclaradoAtualiza(t *testing.T) {
	seeds := []Node{{ID: "vps", Name: "vps", Transport: TransportSSH, Address: "203.0.113.10:22", Kind: NodeKindExterno}}
	f := &fakePVE{recursos: recursosDeTeste()}
	deps := Sources{Seeds: func() ([]Node, error) { return seeds, nil }}
	p, st, _ := novoPollerDeTeste(t, f, deps, PollerConfig{})
	if err := p.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := nosPorID(t, st)["vps"].Transport; got != TransportSSH {
		t.Fatalf("initial transport = %q, want ssh", got)
	}

	// The operator rewrites the seed: another address, another transport, another name.
	seeds[0] = Node{ID: "vps", Name: "vps-novo", Transport: TransportAgente, Address: "10.0.0.9:8099", Kind: NodeKindExterno}
	deps.AgentPing = func(ctx context.Context, addr string) error { return errors.New("sem resposta") }
	p2, _, _ := novoPollerDeTeste(t, f, deps, PollerConfig{})
	p2.store = st
	if err := p2.tick(context.Background()); err != nil {
		t.Fatal(err)
	}

	n := nosPorID(t, st)["vps"]
	if n.Transport != TransportAgente {
		t.Errorf("transport = %q, want agente (the seed was rewritten)", n.Transport)
	}
	if n.Address != "10.0.0.9:8099" {
		t.Errorf("address = %q, want the new one", n.Address)
	}
	if n.Name != "vps-novo" {
		t.Errorf("name = %q, want the new one", n.Name)
	}
}

func escreveSeeds(t *testing.T, dataDir, conteudo string) {
	t.Helper()
	dir := filepath.Join(dataDir, "inventory")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, SeedsFileName), []byte(conteudo), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestSeedsLoad: a missing file is zero seeds WITHOUT an error (a fresh install
// must not refuse to come up because of an optional file); a file that is there
// goes in through the model's validation.
func TestSeedsLoad(t *testing.T) {
	dir := t.TempDir()

	seeds, err := LoadSeeds(dir)
	if err != nil || len(seeds) != 0 {
		t.Fatalf("seeds missing: (%v, %v), want (empty, nil)", seeds, err)
	}

	escreveSeeds(t, dir, `[{"id":"canario","name":"canario","transport":"agente","address":"127.0.0.1:9","kind":"externo"}]`)
	seeds, err = LoadSeeds(dir)
	if err != nil {
		t.Fatalf("LoadSeeds: %v", err)
	}
	if len(seeds) != 1 || seeds[0].ID != "canario" || seeds[0].Transport != TransportAgente {
		t.Fatalf("seeds = %+v", seeds)
	}
}

// TestSeedsInvalido: everything that is refused is refused AT LOAD, quoting the
// value. Accepting first and validating later would mean the bad node was
// already in the inventory by the time anyone noticed.
func TestSeedsInvalido(t *testing.T) {
	casos := []struct {
		nome    string
		json    string
		noTexto string
	}{
		{"transporte fora do enum", `[{"id":"x","transport":"telnet","kind":"externo"}]`, "telnet"},
		{"kind fora do enum", `[{"id":"x","transport":"ssh","kind":"container"}]`, "container"},
		{"sem id", `[{"transport":"ssh","kind":"externo"}]`, "ID"},
		{"id repetido", `[{"id":"x","transport":"ssh","kind":"externo"},{"id":"x","transport":"ssh","kind":"externo"}]`, "repetido"},
		{"agente sem address", `[{"id":"x","transport":"agente","kind":"externo"}]`, "address"},
		{"json malformado", `{isto nao e json}`, "malformado"},
	}
	for _, tc := range casos {
		t.Run(tc.nome, func(t *testing.T) {
			dir := t.TempDir()
			escreveSeeds(t, dir, tc.json)
			_, err := LoadSeeds(dir)
			if err == nil {
				t.Fatalf("an invalid seed was accepted")
			}
			if !strings.Contains(err.Error(), tc.noTexto) {
				t.Fatalf("error %q does not mention %q — a generic error is a defect nobody finds", err, tc.noTexto)
			}
		})
	}
}

// 🔴 TestAgentPollDeadNode is the antidote on the loop side: the dead node (the
// discard port 127.0.0.1:9, connection ALWAYS refused) does NOT get a new
// timestamp, while the hypervisor's nodes on the SAME tick advance. It is that
// asymmetry — not a global counter — that the live verifier proves.
//
// The real ping is used on purpose: a fake returning an error would only prove
// the code reads the return value. Port 9 is refused on any Linux machine with
// no service on it, which makes the case deterministic without external network.
func TestAgentPollDeadNode(t *testing.T) {
	f := &fakePVE{recursos: recursosDeTeste()}
	deps := Sources{Seeds: func() ([]Node, error) {
		return []Node{{ID: "canario", Name: "canario", Transport: TransportAgente, Address: "127.0.0.1:9", Kind: NodeKindExterno}}, nil
	}}
	p, st, rel := novoPollerDeTeste(t, f, deps, PollerConfig{})

	if err := p.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	rel.avanca(2 * time.Minute)
	if err := p.tick(context.Background()); err != nil {
		t.Fatal(err)
	}

	nos := exigeConjunto(t, st, append(conjuntoPVE(), "canario")...)
	if got := nos["canario"].Status.ObservedAt; got != 0 {
		t.Fatalf("the canary was timestamped (%d) — port 9 answered nothing", got)
	}
	if got := nos["lxc/207"].Status.ObservedAt; got != rel.now().Unix() {
		t.Fatalf("the PVE node did not advance on the same tick (%d) — the canary failure contaminated it", got)
	}
	// And the read for the screen: the canary is stale, the guest is not.
	vistas := View(Inventory{Nodes: []Node{nos["canario"], nos["lxc/207"]}}, 90*time.Second, rel.now())
	if !vistas[0].Stale || vistas[0].AgeSeconds != -1 {
		t.Errorf("canary = %+v, want stale with age -1 (never observed)", vistas[0])
	}
	if vistas[1].Stale {
		t.Errorf("a live guest showed up expired: %+v", vistas[1])
	}
}

// TestAgentPollVivoCarimba is the contrapositive: without it, "never stamps
// anything" would pass as if it were dead-node detection.
func TestAgentPollVivoCarimba(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			t.Errorf("path = %q, want /healthz", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	f := &fakePVE{recursos: recursosDeTeste()}
	deps := Sources{Seeds: func() ([]Node, error) {
		return []Node{{ID: "vivo", Name: "vivo", Transport: TransportAgente,
			Address: strings.TrimPrefix(srv.URL, "http://"), Kind: NodeKindExterno}}, nil
	}}
	p, st, rel := novoPollerDeTeste(t, f, deps, PollerConfig{})
	if err := p.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := nosPorID(t, st)["vivo"].Status.ObservedAt; got != rel.now().Unix() {
		t.Fatalf("a LIVE agent node was not timestamped (%d) — the canary guard would be vacuous", got)
	}
}

// TestSeedSSHNaoTemPollAtivo: "ssh" already exists in the enum but has NO
// implementation yet — agent-based reach comes later. Stamping
// liveness nobody observed would be a lie; the node stays without a timestamp and the
// screen shows "never observed", which is the truth.
func TestSeedSSHNaoTemPollAtivo(t *testing.T) {
	var pingou bool
	f := &fakePVE{recursos: recursosDeTeste()}
	deps := Sources{
		Seeds: func() ([]Node, error) {
			return []Node{{ID: "vps", Name: "vps", Transport: TransportSSH, Address: "127.0.0.1:9", Kind: NodeKindExterno}}, nil
		},
		AgentPing: func(ctx context.Context, addr string) error { pingou = true; return nil },
	}
	p, st, _ := novoPollerDeTeste(t, f, deps, PollerConfig{})
	if err := p.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if pingou {
		t.Error("an ssh node got an agent poll — there is no ssh client in this phase")
	}
	if got := nosPorID(t, st)["vps"].Status.ObservedAt; got != 0 {
		t.Errorf("an ssh node was timestamped (%d) with nobody observing it", got)
	}
}

// 🔴 TestPollerAplicaCredenciais is the poller side of the production defect:
// without the source, Credential stays zeroed and credentialState() returns
// "ausente" for every node — 11 nodes saying "no credential" with a full vault.
func TestPollerAplicaCredenciais(t *testing.T) {
	f := &fakePVE{recursos: recursosDeTeste()}
	deps := Sources{Credentials: func(nos []Node) (map[string]Credential, error) {
		out := map[string]Credential{}
		for _, n := range nos {
			if n.Transport == TransportPVEAPI && n.VMID > 0 {
				out[n.ID] = Credential{TokenID: "lab@pve!node-" + n.Name, Expire: 1802645875}
			}
		}
		return out, nil
	}}
	p, st, rel := novoPollerDeTeste(t, f, deps, PollerConfig{})
	if err := p.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	nos := nosPorID(t, st)
	if got := nos["lxc/207"].Credential.TokenID; got != "lab@pve!node-apps" {
		t.Fatalf("token_id = %q — the node was left with no credential while the vault is full", got)
	}
	if got := nos["lxc/207"].Credential.Expire; got != 1802645875 {
		t.Fatalf("expire = %d — without it the Trap 10 warning never fires", got)
	}
	// The state is left EMPTY on disk on purpose: the view is what resolves it,
	// with the clock of whoever serializes. And the view has to say "ok".
	if nos["lxc/207"].Credential.State != "" {
		t.Errorf("state written to disk = %q, want empty (the verdict belongs to the view)", nos["lxc/207"].Credential.State)
	}
	vistas := View(Inventory{Nodes: []Node{nos["lxc/207"]}}, time.Minute, rel.now())
	if vistas[0].Credential.State != CredOK {
		t.Fatalf("view = %q, want ok", vistas[0].Credential.State)
	}
}

// 🔴 TestPollerRevogadaVenceAusente: after a revocation the key VANISHES from
// the vault, so the source returns no entry. Overwriting at that point would
// erase the record of the revocation itself and the screen would say "never had
// a credential" for a token that was just revoked.
func TestPollerRevogadaVenceAusente(t *testing.T) {
	f := &fakePVE{recursos: recursosDeTeste()}
	var comChave bool
	deps := Sources{Credentials: func(nos []Node) (map[string]Credential, error) {
		out := map[string]Credential{}
		if comChave {
			for _, n := range nos {
				if n.VMID > 0 {
					out[n.ID] = Credential{TokenID: "lab@pve!node-" + n.Name}
				}
			}
		}
		return out, nil
	}}
	comChave = true
	p, st, _ := novoPollerDeTeste(t, f, deps, PollerConfig{})
	if err := p.tick(context.Background()); err != nil {
		t.Fatal(err)
	}

	// The handler flagged the revocation and the key is gone from the vault.
	if err := st.Replace(func(iv *Inventory) {
		for i := range iv.Nodes {
			if iv.Nodes[i].ID == "lxc/207" {
				iv.Nodes[i].Credential = Credential{State: CredRevogada}
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	comChave = false

	if err := p.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	nos := nosPorID(t, st)
	if got := nos["lxc/207"].Credential.State; got != CredRevogada {
		t.Fatalf("state = %q, want revogada — the tick erased the record of the revocation", got)
	}
	// And the neighbour, which also lost its entry but was NEVER revoked, goes back
	// to "ausente" — which is the truth for it.
	if got := nos["qemu/208"].Credential.State; got != "" {
		t.Errorf("neighbour = %q, want empty (the view will say ausente)", got)
	}
}

// TestPollerFonteDeCredencialQuebradaNaoApaga: a source that errors (vault down)
// must NOT zero the credentials — the panel keeps the last known state instead
// of announcing that the whole lab lost its credentials.
func TestPollerFonteDeCredencialQuebradaNaoApaga(t *testing.T) {
	f := &fakePVE{recursos: recursosDeTeste()}
	var quebrada bool
	deps := Sources{Credentials: func(nos []Node) (map[string]Credential, error) {
		if quebrada {
			return nil, errors.New("cofre fora do ar")
		}
		out := map[string]Credential{}
		for _, n := range nos {
			if n.VMID > 0 {
				out[n.ID] = Credential{TokenID: "lab@pve!node-" + n.Name, Expire: 99}
			}
		}
		return out, nil
	}}
	p, st, _ := novoPollerDeTeste(t, f, deps, PollerConfig{})
	if err := p.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	quebrada = true
	if err := p.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := nosPorID(t, st)["lxc/207"].Credential.TokenID; got != "lab@pve!node-apps" {
		t.Fatalf("token_id = %q — the broken source erased the credential", got)
	}
}

// ══ a node that VANISHED from the hypervisor is FLAGGED, never erased ══════
//
// The operator watched a destroyed guest stay listed as "stale", inflating the
// total and the stale count on the health strip.
//
// The first attempt was to REMOVE it, and two existing pins knocked that down with
// good reason: a guest drops off the list because it is powered down, migrated or has had its
// ACL withdrawn, and erasing it is amnesia presented as truth.
//
// The fix is to tell the two silences apart: "stale" = the panel could not
// look; "absent" = the panel looked and did not find it.
//
// The negative tests here are worth more than the positive one: flagging by absence is still
// an assertion about the hypervisor, and each guard covers a distinct way of
// confusing "nobody told me" with "it is not there".

func TestNoQueSumiuEMarcadoAusenteSemSerApagado(t *testing.T) {
	inv := Inventory{Nodes: []Node{
		{ID: "lxc/207", Name: "apps", Kind: NodeKindGuest, Transport: TransportPVEAPI, VMID: 207},
		{ID: "lxc/101", Name: "prova-clone", Kind: NodeKindGuest, Transport: TransportPVEAPI, VMID: 101},
		{ID: "node/pve", Name: "pve", Kind: NodeKindHost, Transport: TransportPVEAPI},
	}}
	novos := marcaSumidos(&inv, recursosDeTeste(), nil, 1800000000)

	if len(novos) != 1 || novos[0] != "lxc/101" {
		t.Fatalf("marked = %v, want only lxc/101", novos)
	}
	if len(inv.Nodes) != 3 {
		t.Fatalf("🔴 someone was DELETED: %v — the rule forbids it", idsDe(inv))
	}
	porID := map[string]Node{}
	for _, n := range inv.Nodes {
		porID[n.ID] = n
	}
	if porID["lxc/101"].AusenteDesde != 1800000000 {
		t.Errorf("lxc/101 with no absence timestamp: %d", porID["lxc/101"].AusenteDesde)
	}
	if porID["lxc/207"].AusenteDesde != 0 || porID["node/pve"].AusenteDesde != 0 {
		t.Errorf("a node that is present was marked ausente")
	}
}

// 🔴 THE TIMESTAMP IS NOT REWRITTEN on every tick: it is what says how long the
// node has been gone. Rewriting it would make the absence always look freshly
// discovered.
func TestCarimboDeAusenciaNaoEReescrito(t *testing.T) {
	inv := Inventory{Nodes: []Node{{ID: "lxc/101", Kind: NodeKindGuest, Transport: TransportPVEAPI, VMID: 101}}}
	marcaSumidos(&inv, recursosDeTeste(), nil, 1800000000)
	novos := marcaSumidos(&inv, recursosDeTeste(), nil, 1800009999)
	if novos != nil {
		t.Errorf("the second pass announced it again: %v", novos)
	}
	if inv.Nodes[0].AusenteDesde != 1800000000 {
		t.Errorf("timestamp = %d, want the FIRST one (1800000000)", inv.Nodes[0].AusenteDesde)
	}
}

// And showing up again CLEARS the mark — a node that reappeared is not an
// absent node.
func TestNoQueVoltaPerdeAMarcaDeAusencia(t *testing.T) {
	inv := Inventory{Nodes: []Node{{ID: "lxc/207", Kind: NodeKindGuest, Transport: TransportPVEAPI, VMID: 207, AusenteDesde: 1799999999}}}
	marcaSumidos(&inv, recursosDeTeste(), nil, 1800000000)
	if inv.Nodes[0].AusenteDesde != 0 {
		t.Errorf("the node came back and is still marked ausente: %d", inv.Nodes[0].AusenteDesde)
	}
}

// 🔴 GUARD 2: a response with NO guest at all marks nothing.
//
// A /cluster/resources that comes back empty because of a passing hiccup on the
// hypervisor would mark the whole lab absent in a single tick. An empty list is
// precisely the answer that can be trusted the least.
func TestRespostaVaziaNaoMarcaNinguem(t *testing.T) {
	inv := Inventory{Nodes: []Node{
		{ID: "lxc/207", Kind: NodeKindGuest, Transport: TransportPVEAPI, VMID: 207},
		{ID: "qemu/208", Kind: NodeKindGuest, Transport: TransportPVEAPI, VMID: 208},
	}}
	if novos := marcaSumidos(&inv, nil, nil, 1800000000); novos != nil {
		t.Errorf("an empty response marked %v", novos)
	}
	if novos := marcaSumidos(&inv, []pve.Resource{}, nil, 1800000000); novos != nil {
		t.Errorf("an empty response marked %v", novos)
	}
	for _, n := range inv.Nodes {
		if n.AusenteDesde != 0 {
			t.Fatalf("🔴 the lab was marked ausente because of an empty response: %s", n.ID)
		}
	}
}

// 🔴 GUARD 3: a node that is NOT discovered by the hypervisor is not judged by
// it. `canario` has transport agente and comes from seeds; its absence from
// /cluster/resources means nothing.
func TestNoDeOutroTransporteNaoEMarcado(t *testing.T) {
	inv := Inventory{Nodes: []Node{
		{ID: "canario", Kind: "externo", Transport: TransportAgente, Address: "127.0.0.1:9"},
		{ID: "maquina-ssh", Transport: TransportSSH},
		{ID: "lxc/207", Kind: NodeKindGuest, Transport: TransportPVEAPI, VMID: 207},
	}}
	if novos := marcaSumidos(&inv, recursosDeTeste(), nil, 1800000000); novos != nil {
		t.Errorf("marked a node from another transport: %v", novos)
	}
	for _, n := range inv.Nodes {
		if n.AusenteDesde != 0 {
			t.Errorf("%s was marked ausente without the hypervisor owning it", n.ID)
		}
	}
}

// A seed that declares transport pve-api is spared too: it is DECLARED, not
// discovered.
func TestSeedDeclaradoNaoEMarcadoAusente(t *testing.T) {
	inv := Inventory{Nodes: []Node{{ID: "lxc/999", Kind: NodeKindGuest, Transport: TransportPVEAPI, VMID: 999}}}
	seeds := []Node{{ID: "lxc/999", Transport: TransportPVEAPI}}
	if novos := marcaSumidos(&inv, recursosDeTeste(), seeds, 1800000000); novos != nil {
		t.Errorf("marked a node DECLARED in seeds: %v", novos)
	}
}

// 🔴 GUARD 1, at tick level: discovery that FAILS neither marks nor erases
// anyone. It is invariant 2 of the poller, and the pin exists because the
// marking is exactly the kind of code someone moves to the wrong place.
func TestFalhaDeDescobertaNaoMarcaAusencia(t *testing.T) {
	f := &fakePVE{recursos: recursosDeTeste()}
	p, st, _ := novoPollerDeTeste(t, f, Sources{}, PollerConfig{})
	if err := p.tick(context.Background()); err != nil {
		t.Fatalf("first tick: %v", err)
	}
	invAntes, err := st.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	antes := idsDe(invAntes)
	if len(antes) < 2 {
		t.Fatalf("the first tick did not populate the inventory: %v", antes)
	}

	f.erro = errors.New("hipervisor inalcançável")
	if err := p.tick(context.Background()); err == nil {
		t.Fatal("the tick with failed discovery returned nil")
	}
	invDepois, err := st.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if depois := idsDe(invDepois); len(depois) != len(antes) {
		t.Errorf("🔴 failed discovery deleted nodes: before %v, after %v", antes, depois)
	}
	for _, n := range invDepois.Nodes {
		if n.AusenteDesde != 0 {
			t.Errorf("🔴 FAILED discovery marked %s as ausente — 'nobody told me' became 'it is not there'", n.ID)
		}
	}
}
