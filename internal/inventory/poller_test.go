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

type fakePVE struct {
	mu        sync.Mutex
	resources []pve.Resource
	failure   error
	addrs     map[int]string
	addrErr   map[int]error
	delay     time.Duration
	calls     int32

	status         pve.NodeStatus
	statusErr      error
	nodeStatusName string
	statusCalls    int32
}

func (f *fakePVE) NodeStatus(ctx context.Context, node string) (pve.NodeStatus, error) {
	atomic.AddInt32(&f.statusCalls, 1)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nodeStatusName = node
	if f.statusErr != nil {
		return pve.NodeStatus{}, f.statusErr
	}
	return f.status, nil
}

func (f *fakePVE) StorageList(ctx context.Context, node string) ([]pve.Storage, error) {
	return nil, nil
}
func (f *fakePVE) ZFSList(ctx context.Context, node string) ([]pve.ZPool, error) { return nil, nil }
func (f *fakePVE) Permissions(ctx context.Context) (map[string]map[string]int, error) {
	return nil, nil
}

func (f *fakePVE) ClusterResources(ctx context.Context) ([]pve.Resource, error) {
	atomic.AddInt32(&f.calls, 1)
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failure != nil {
		return nil, f.failure
	}
	out := make([]pve.Resource, len(f.resources))
	copy(out, f.resources)
	return out, nil
}

func (f *fakePVE) GuestAddress(ctx context.Context, node string, vmid int, typ string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err, ok := f.addrErr[vmid]; ok {
		return "", err
	}
	return f.addrs[vmid], nil
}

type fixedClock struct {
	mu sync.Mutex
	t  time.Time
}

func (r *fixedClock) now() time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.t
}

func (r *fixedClock) advance(d time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.t = r.t.Add(d)
}

func testResources() []pve.Resource {
	return []pve.Resource{
		{ID: "lxc/207", VMID: 207, Name: "apps", Node: "pve", Type: "lxc", Status: "running", Uptime: 60826},
		{ID: "qemu/208", VMID: 208, Name: "dev", Node: "pve", Type: "qemu", Status: "running", Uptime: 660828},
	}
}

func newTestPoller(t *testing.T, f *fakePVE, deps Sources, cfg PollerConfig) (*Poller, *Store, *fixedClock) {
	t.Helper()
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	rel := &fixedClock{t: time.Unix(1800000000, 0)}
	if cfg.Now == nil {
		cfg.Now = rel.now
	}
	return NewPoller(st, f, deps, cfg), st, rel
}

func nodesByID(t *testing.T, st *Store) map[string]Node {
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

func TestPollerStampsOnServer(t *testing.T) {
	f := &fakePVE{resources: testResources(), addrs: map[int]string{207: "192.168.100.47"}}
	p, st, rel := newTestPoller(t, f, Sources{}, PollerConfig{})

	if err := p.tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	nodes := requireSet(t, st, pveSet()...)
	want := rel.now().Unix()
	if nodes["node/pve"].Kind != NodeKindHost || nodes["node/pve"].Status.ObservedAt != want {
		t.Errorf("host = %+v, want kind=host timestamped at %d", nodes["node/pve"], want)
	}
	for _, id := range []string{"lxc/207", "qemu/208"} {
		n, ok := nodes[id]
		if !ok {
			t.Fatalf("node %s missing from the inventory (%v)", id, nodes)
		}
		if n.Status.ObservedAt != want {
			t.Errorf("%s: observed_at = %d, want %d (the SERVER's clock)", id, n.Status.ObservedAt, want)
		}
		if n.Status.Value == "" {
			t.Errorf("%s: empty status", id)
		}
	}
	if got := nodes["lxc/207"].Address; got != "192.168.100.47" {
		t.Errorf("address = %q, want 192.168.100.47", got)
	}
	if got := nodes["qemu/208"].Address; got != "" {
		t.Errorf("address of the guest with no ip = %q, want empty", got)
	}

	rel.advance(30 * time.Second)
	if err := p.tick(context.Background()); err != nil {
		t.Fatalf("tick 2: %v", err)
	}
	if got := nodesByID(t, st)["lxc/207"].Status.ObservedAt; got != rel.now().Unix() {
		t.Fatalf("observed_at did not advance on the 2nd tick: %d, want %d", got, rel.now().Unix())
	}
}

func TestPollerTickTimeout(t *testing.T) {
	f := &fakePVE{resources: testResources(), delay: 5 * time.Second}
	p, st, _ := newTestPoller(t, f, Sources{}, PollerConfig{Timeout: 50 * time.Millisecond})

	start := time.Now()
	err := p.tick(context.Background())
	took := time.Since(start)
	if err == nil {
		t.Fatal("a tick that blew the timeout returned success")
	}
	if took > 2*time.Second {
		t.Fatalf("the tick took %v — the 50ms timeout was not respected", took)
	}
	inv, _ := st.Snapshot()
	if len(inv.Nodes) != 0 {
		t.Fatalf("a failed tick created nodes: %+v", inv.Nodes)
	}

	f.mu.Lock()
	f.delay = 0
	f.mu.Unlock()
	if err := p.tick(context.Background()); err != nil {
		t.Fatalf("the next tick failed: %v", err)
	}
	requireSet(t, st, pveSet()...)
}

func TestPollerTotalFailurePreservesNodes(t *testing.T) {
	f := &fakePVE{resources: testResources()}
	p, st, rel := newTestPoller(t, f, Sources{}, PollerConfig{})
	if err := p.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	oldStamp := nodesByID(t, st)["lxc/207"].Status.ObservedAt

	f.mu.Lock()
	f.failure = errors.New("hypervisor unreachable")
	f.mu.Unlock()
	rel.advance(5 * time.Minute)
	if err := p.tick(context.Background()); err == nil {
		t.Fatal("a total PVE failure returned success")
	}

	nodes := requireSet(t, st, pveSet()...)
	if got := nodes["lxc/207"].Status.ObservedAt; got != oldStamp {
		t.Fatalf("observed_at = %d, want the OLD %d (a timestamp cannot advance without an observation)", got, oldStamp)
	}
}

func TestPollerPartialFailure(t *testing.T) {
	f := &fakePVE{
		resources: testResources(),
		addrs:     map[int]string{208: "192.168.100.48"},
		addrErr:   map[int]error{207: errors.New("unreadable config")},
	}
	p, st, rel := newTestPoller(t, f, Sources{}, PollerConfig{})
	if err := p.tick(context.Background()); err != nil {
		t.Fatalf("a partial failure took down the whole tick: %v", err)
	}
	nodes := requireSet(t, st, pveSet()...)
	if nodes["qemu/208"].Address != "192.168.100.48" {
		t.Errorf("a healthy guest was left with no address: %+v", nodes["qemu/208"])
	}
	if nodes["qemu/208"].Status.ObservedAt != rel.now().Unix() {
		t.Errorf("a healthy guest was not timestamped")
	}
	if nodes["lxc/207"].Status.ObservedAt != rel.now().Unix() {
		t.Errorf("a guest with an unreadable address lost its status timestamp")
	}
	if nodes["lxc/207"].Address != "" {
		t.Errorf("address = %q, want empty", nodes["lxc/207"].Address)
	}
}

func TestPollerRunRespectsCtx(t *testing.T) {
	f := &fakePVE{resources: testResources()}
	p, st, _ := newTestPoller(t, f, Sources{}, PollerConfig{Interval: 5 * time.Millisecond})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { p.Run(ctx); close(done) }()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && atomic.LoadInt32(&f.calls) == 0 {
		time.Sleep(time.Millisecond)
	}
	if atomic.LoadInt32(&f.calls) == 0 {
		t.Fatal("Run did not execute a single tick")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop with the ctx cancelled")
	}
	if len(nodesByID(t, st)) == 0 {
		t.Fatal("Run ran but wrote nothing")
	}
}

func TestPollerPanicDoesNotKillLoop(t *testing.T) {
	var n int32
	deps := Sources{Jobs: func() ([]JobRef, error) {
		if atomic.AddInt32(&n, 1) == 1 {
			panic("job source blew up")
		}
		return nil, nil
	}}
	f := &fakePVE{resources: testResources()}
	p, st, _ := newTestPoller(t, f, deps, PollerConfig{})

	if err := p.tick(context.Background()); err == nil {
		t.Fatal("a tick with a panic returned success — the error has to surface")
	}
	if err := p.tick(context.Background()); err != nil {
		t.Fatalf("the tick after the panic failed: %v", err)
	}
	requireSet(t, st, pveSet()...)
}

func sortedIDs(nodes map[string]Node) []string {
	var ids []string
	for id := range nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func fmtIDs(ids []string) string { return fmt.Sprint(ids) }

func requireSet(t *testing.T, st *Store, want ...string) map[string]Node {
	t.Helper()
	nodes := nodesByID(t, st)
	sort.Strings(want)
	if got := fmtIDs(sortedIDs(nodes)); got != fmtIDs(want) {
		t.Fatalf("set of nodes = %s, want %s", got, fmtIDs(want))
	}
	return nodes
}

func pveSet() []string { return []string{"node/pve", "lxc/207", "qemu/208"} }

func TestDiscoveryAddsUnknownGuest(t *testing.T) {
	f := &fakePVE{resources: testResources()}
	p, st, rel := newTestPoller(t, f, Sources{}, PollerConfig{})

	if err := p.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	requireSet(t, st, pveSet()...)

	f.mu.Lock()
	f.resources = append(f.resources, pve.Resource{
		ID: "lxc/299", VMID: 299, Name: "surprise", Node: "pve", Type: "lxc", Status: "running", Uptime: 42,
	})
	f.mu.Unlock()
	rel.advance(30 * time.Second)

	if err := p.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	nodes := requireSet(t, st, append(pveSet(), "lxc/299")...)

	fresh := nodes["lxc/299"]
	if fresh.Name != "surprise" || fresh.Kind != NodeKindGuest || fresh.VMID != 299 {
		t.Fatalf("malformed new guest: %+v", fresh)
	}
	if fresh.Status.ObservedAt != rel.now().Unix() {
		t.Fatalf("a new guest with no timestamp: %+v", fresh)
	}
	if fresh.Transport != TransportPVEAPI {
		t.Fatalf("transport = %q, want pve-api", fresh.Transport)
	}
}

func TestDiscoveryRemovalKeepsHistory(t *testing.T) {
	f := &fakePVE{resources: testResources()}
	p, st, rel := newTestPoller(t, f, Sources{}, PollerConfig{TTL: 90 * time.Second})
	if err := p.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	oldStamp := nodesByID(t, st)["lxc/207"].Status.ObservedAt

	f.mu.Lock()
	f.resources = f.resources[1:]
	f.mu.Unlock()
	rel.advance(5 * time.Minute)
	if err := p.tick(context.Background()); err != nil {
		t.Fatal(err)
	}

	nodes := requireSet(t, st, pveSet()...)
	gone := nodes["lxc/207"]
	if gone.Status.ObservedAt != oldStamp {
		t.Fatalf("timestamp of the vanished one = %d, want the OLD %d", gone.Status.ObservedAt, oldStamp)
	}
	seen := View(Inventory{Nodes: []Node{gone}}, 90*time.Second, rel.now())
	if !seen[0].Stale || seen[0].AgeSeconds != 300 {
		t.Fatalf("view of the vanished one = %+v, want stale with 300s", seen[0])
	}
	if nodes["qemu/208"].Status.ObservedAt != rel.now().Unix() {
		t.Fatal("the guest that is present did not advance its timestamp")
	}
}

func TestNodeTransport(t *testing.T) {
	seeds := []Node{{ID: "canary", Name: "canary", Transport: TransportAgent, Address: "127.0.0.1:9", Kind: NodeKindExternal}}
	f := &fakePVE{resources: testResources()}
	deps := Sources{
		Seeds:     func() ([]Node, error) { return seeds, nil },
		AgentPing: func(ctx context.Context, addr string) error { return errors.New("refused") },
	}
	p, st, _ := newTestPoller(t, f, deps, PollerConfig{})
	if err := p.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	nodes := requireSet(t, st, append(pveSet(), "canary")...)
	for _, id := range pveSet() {
		if nodes[id].Transport != TransportPVEAPI {
			t.Errorf("%s: transport = %q, want pve-api", id, nodes[id].Transport)
		}
	}
	if nodes["canary"].Transport != TransportAgent {
		t.Errorf("the seed lost the transport: %+v", nodes["canary"])
	}
	if err := (Node{ID: "x", Transport: "telnet", Kind: NodeKindExternal}).Validate(); err == nil {
		t.Error("a transport outside the enum was accepted")
	}
}

func TestAggregatesReferences(t *testing.T) {
	deps := Sources{
		Projects: func() ([]Project, []Deployment, error) {
			return []Project{{ID: "css-lee", Name: "Acme Booking"}},
				[]Deployment{{ID: "d1", ProjectID: "css-lee", NodeID: "lxc/207", Branch: "main"}}, nil
		},
		Jobs: func() ([]JobRef, error) {
			return []JobRef{{ID: "j_1", Kind: "queue", NodeID: "lxc/207", Source: "user"}}, nil
		},
		Services: func() ([]Service, error) {
			return []Service{{ID: "s1", NodeID: "lxc/207", Name: "panel", Unit: "server-control-panel.service"}}, nil
		},
	}
	f := &fakePVE{resources: testResources()}
	p, st, _ := newTestPoller(t, f, deps, PollerConfig{})
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
	if len(inv.Services) != 1 || inv.Services[0].Unit != "server-control-panel.service" {
		t.Errorf("services = %+v", inv.Services)
	}
	if inv.SchemaVersion != SchemaVersion {
		t.Errorf("schema_version = %d, want %d", inv.SchemaVersion, SchemaVersion)
	}
}

func TestRevokedCredentialOnlyForUnexpired(t *testing.T) {
	f := &fakePVE{resources: testResources()}
	p, st, rel := newTestPoller(t, f, Sources{}, PollerConfig{})
	if err := p.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	now := rel.now().Unix()
	if err := st.Replace(func(inv *Inventory) {
		for i := range inv.Nodes {
			switch inv.Nodes[i].ID {
			case "lxc/207":
				inv.Nodes[i].Credential = Credential{TokenID: "panel@pve!a", Expire: now - 86400}
			case "qemu/208":
				inv.Nodes[i].Credential = Credential{TokenID: "panel@pve!b", Expire: now + 30*86400}
			}
		}
	}); err != nil {
		t.Fatal(err)
	}

	f.mu.Lock()
	f.failure = &pve.Error{Kind: pve.KindNoCredential, Status: 401, Path: "/cluster/resources"}
	f.mu.Unlock()
	if err := p.tick(context.Background()); err == nil {
		t.Fatal("401 returned success")
	}

	nodes := nodesByID(t, st)
	if got := nodes["qemu/208"].Credential.State; got != CredRevoked {
		t.Errorf("a valid token that took a 401: state = %q, want %q", got, CredRevoked)
	}
	if got := nodes["lxc/207"].Credential.State; got == CredRevoked {
		t.Errorf("an ALREADY EXPIRED token was marked revoked — the clue that this is a calendar matter disappears")
	}
	seen := View(Inventory{Nodes: []Node{nodes["lxc/207"]}}, time.Minute, rel.now())
	if seen[0].Credential.State != CredExpired {
		t.Errorf("view of the expired one = %q, want %q", seen[0].Credential.State, CredExpired)
	}
}

func TestNoCredentialErrorMarksNothing(t *testing.T) {
	f := &fakePVE{resources: testResources()}
	p, st, _ := newTestPoller(t, f, Sources{}, PollerConfig{})
	if err := p.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.failure = &pve.Error{Kind: pve.KindUnreachable, Path: "/cluster/resources"}
	f.mu.Unlock()
	if err := p.tick(context.Background()); err == nil {
		t.Fatal("a transport error returned success")
	}
	for id, n := range nodesByID(t, st) {
		if n.Credential.State == CredRevoked {
			t.Errorf("%s marked revoked because of a TRANSPORT error", id)
		}
	}
}

func TestRedeclaredSeedUpdates(t *testing.T) {
	seeds := []Node{{ID: "vps", Name: "vps", Transport: TransportSSH, Address: "203.0.113.10:22", Kind: NodeKindExternal}}
	f := &fakePVE{resources: testResources()}
	deps := Sources{Seeds: func() ([]Node, error) { return seeds, nil }}
	p, st, _ := newTestPoller(t, f, deps, PollerConfig{})
	if err := p.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := nodesByID(t, st)["vps"].Transport; got != TransportSSH {
		t.Fatalf("initial transport = %q, want ssh", got)
	}

	seeds[0] = Node{ID: "vps", Name: "vps-new", Transport: TransportAgent, Address: "10.0.0.9:8099", Kind: NodeKindExternal}
	deps.AgentPing = func(ctx context.Context, addr string) error { return errors.New("no response") }
	p2, _, _ := newTestPoller(t, f, deps, PollerConfig{})
	p2.store = st
	if err := p2.tick(context.Background()); err != nil {
		t.Fatal(err)
	}

	n := nodesByID(t, st)["vps"]
	if n.Transport != TransportAgent {
		t.Errorf("transport = %q, want agent (the seed was rewritten)", n.Transport)
	}
	if n.Address != "10.0.0.9:8099" {
		t.Errorf("address = %q, want the new one", n.Address)
	}
	if n.Name != "vps-new" {
		t.Errorf("name = %q, want the new one", n.Name)
	}
}

func writeSeeds(t *testing.T, dataDir, content string) {
	t.Helper()
	dir := filepath.Join(dataDir, "inventory")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, SeedsFileName), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSeedsLoad(t *testing.T) {
	dir := t.TempDir()

	seeds, err := LoadSeeds(dir)
	if err != nil || len(seeds) != 0 {
		t.Fatalf("seeds missing: (%v, %v), want (empty, nil)", seeds, err)
	}

	writeSeeds(t, dir, `[{"id":"canary","name":"canary","transport":"agent","address":"127.0.0.1:9","kind":"external"}]`)
	seeds, err = LoadSeeds(dir)
	if err != nil {
		t.Fatalf("LoadSeeds: %v", err)
	}
	if len(seeds) != 1 || seeds[0].ID != "canary" || seeds[0].Transport != TransportAgent {
		t.Fatalf("seeds = %+v", seeds)
	}
}

func TestSeedsInvalid(t *testing.T) {
	cases := []struct {
		name   string
		json   string
		inText string
	}{
		{"transport outside the enum", `[{"id":"x","transport":"telnet","kind":"external"}]`, "telnet"},
		{"kind outside the enum", `[{"id":"x","transport":"ssh","kind":"container"}]`, "container"},
		{"missing id", `[{"transport":"ssh","kind":"external"}]`, "ID"},
		{"duplicate id", `[{"id":"x","transport":"ssh","kind":"external"},{"id":"x","transport":"ssh","kind":"external"}]`, "duplicate"},
		{"agent without address", `[{"id":"x","transport":"agent","kind":"external"}]`, "address"},
		{"malformed json", `{this is not json}`, "malformed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeSeeds(t, dir, tc.json)
			_, err := LoadSeeds(dir)
			if err == nil {
				t.Fatalf("an invalid seed was accepted")
			}
			if !strings.Contains(err.Error(), tc.inText) {
				t.Fatalf("error %q does not mention %q — a generic error is a defect nobody finds", err, tc.inText)
			}
		})
	}
}

func TestAgentPollDeadNode(t *testing.T) {
	f := &fakePVE{resources: testResources()}
	deps := Sources{Seeds: func() ([]Node, error) {
		return []Node{{ID: "canary", Name: "canary", Transport: TransportAgent, Address: "127.0.0.1:9", Kind: NodeKindExternal}}, nil
	}}
	p, st, rel := newTestPoller(t, f, deps, PollerConfig{})

	if err := p.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	rel.advance(2 * time.Minute)
	if err := p.tick(context.Background()); err != nil {
		t.Fatal(err)
	}

	nodes := requireSet(t, st, append(pveSet(), "canary")...)
	if got := nodes["canary"].Status.ObservedAt; got != 0 {
		t.Fatalf("the canary was timestamped (%d) — port 9 answered nothing", got)
	}
	if got := nodes["lxc/207"].Status.ObservedAt; got != rel.now().Unix() {
		t.Fatalf("the PVE node did not advance on the same tick (%d) — the canary failure contaminated it", got)
	}
	seen := View(Inventory{Nodes: []Node{nodes["canary"], nodes["lxc/207"]}}, 90*time.Second, rel.now())
	if !seen[0].Stale || seen[0].AgeSeconds != -1 {
		t.Errorf("canary = %+v, want stale with age -1 (never observed)", seen[0])
	}
	if seen[1].Stale {
		t.Errorf("a live guest showed up expired: %+v", seen[1])
	}
}

func TestAgentPollAliveStamps(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			t.Errorf("path = %q, want /healthz", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	f := &fakePVE{resources: testResources()}
	deps := Sources{Seeds: func() ([]Node, error) {
		return []Node{{ID: "alive", Name: "alive", Transport: TransportAgent,
			Address: strings.TrimPrefix(srv.URL, "http://"), Kind: NodeKindExternal}}, nil
	}}
	p, st, rel := newTestPoller(t, f, deps, PollerConfig{})
	if err := p.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := nodesByID(t, st)["alive"].Status.ObservedAt; got != rel.now().Unix() {
		t.Fatalf("a LIVE agent node was not timestamped (%d) — the canary guard would be vacuous", got)
	}
}

func TestSSHSeedHasNoActivePoll(t *testing.T) {
	var pinged bool
	f := &fakePVE{resources: testResources()}
	deps := Sources{
		Seeds: func() ([]Node, error) {
			return []Node{{ID: "vps", Name: "vps", Transport: TransportSSH, Address: "127.0.0.1:9", Kind: NodeKindExternal}}, nil
		},
		AgentPing: func(ctx context.Context, addr string) error { pinged = true; return nil },
	}
	p, st, _ := newTestPoller(t, f, deps, PollerConfig{})
	if err := p.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if pinged {
		t.Error("an ssh node got an agent poll — there is no ssh client in this phase")
	}
	if got := nodesByID(t, st)["vps"].Status.ObservedAt; got != 0 {
		t.Errorf("an ssh node was timestamped (%d) with nobody observing it", got)
	}
}

func TestPollerAppliesCredentials(t *testing.T) {
	f := &fakePVE{resources: testResources()}
	deps := Sources{Credentials: func(nodes []Node) (map[string]Credential, error) {
		out := map[string]Credential{}
		for _, n := range nodes {
			if n.Transport == TransportPVEAPI && n.VMID > 0 {
				out[n.ID] = Credential{TokenID: "panel@pve!node-" + n.Name, Expire: 1802645875}
			}
		}
		return out, nil
	}}
	p, st, rel := newTestPoller(t, f, deps, PollerConfig{})
	if err := p.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	nodes := nodesByID(t, st)
	if got := nodes["lxc/207"].Credential.TokenID; got != "panel@pve!node-apps" {
		t.Fatalf("token_id = %q — the node was left with no credential while the vault is full", got)
	}
	if got := nodes["lxc/207"].Credential.Expire; got != 1802645875 {
		t.Fatalf("expire = %d — without it the Trap 10 warning never fires", got)
	}
	if nodes["lxc/207"].Credential.State != "" {
		t.Errorf("state written to disk = %q, want empty (the verdict belongs to the view)", nodes["lxc/207"].Credential.State)
	}
	seen := View(Inventory{Nodes: []Node{nodes["lxc/207"]}}, time.Minute, rel.now())
	if seen[0].Credential.State != CredOK {
		t.Fatalf("view = %q, want ok", seen[0].Credential.State)
	}
}

func TestPollerRevokedBeatsMissing(t *testing.T) {
	f := &fakePVE{resources: testResources()}
	var withKey bool
	deps := Sources{Credentials: func(nodes []Node) (map[string]Credential, error) {
		out := map[string]Credential{}
		if withKey {
			for _, n := range nodes {
				if n.VMID > 0 {
					out[n.ID] = Credential{TokenID: "panel@pve!node-" + n.Name}
				}
			}
		}
		return out, nil
	}}
	withKey = true
	p, st, _ := newTestPoller(t, f, deps, PollerConfig{})
	if err := p.tick(context.Background()); err != nil {
		t.Fatal(err)
	}

	if err := st.Replace(func(iv *Inventory) {
		for i := range iv.Nodes {
			if iv.Nodes[i].ID == "lxc/207" {
				iv.Nodes[i].Credential = Credential{State: CredRevoked}
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	withKey = false

	if err := p.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	nodes := nodesByID(t, st)
	if got := nodes["lxc/207"].Credential.State; got != CredRevoked {
		t.Fatalf("state = %q, want revoked — the tick erased the record of the revocation", got)
	}
	if got := nodes["qemu/208"].Credential.State; got != "" {
		t.Errorf("neighbour = %q, want empty (the view will say missing)", got)
	}
}

func TestPollerBrokenCredentialSourceDeletesNothing(t *testing.T) {
	f := &fakePVE{resources: testResources()}
	var broken bool
	deps := Sources{Credentials: func(nodes []Node) (map[string]Credential, error) {
		if broken {
			return nil, errors.New("vault down")
		}
		out := map[string]Credential{}
		for _, n := range nodes {
			if n.VMID > 0 {
				out[n.ID] = Credential{TokenID: "panel@pve!node-" + n.Name, Expire: 99}
			}
		}
		return out, nil
	}}
	p, st, _ := newTestPoller(t, f, deps, PollerConfig{})
	if err := p.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	broken = true
	if err := p.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := nodesByID(t, st)["lxc/207"].Credential.TokenID; got != "panel@pve!node-apps" {
		t.Fatalf("token_id = %q — the broken source erased the credential", got)
	}
}

func TestGoneNodeMarkedMissingNotDeleted(t *testing.T) {
	inv := Inventory{Nodes: []Node{
		{ID: "lxc/207", Name: "apps", Kind: NodeKindGuest, Transport: TransportPVEAPI, VMID: 207},
		{ID: "lxc/101", Name: "test-clone", Kind: NodeKindGuest, Transport: TransportPVEAPI, VMID: 101},
		{ID: "node/pve", Name: "pve", Kind: NodeKindHost, Transport: TransportPVEAPI},
	}}
	added := markGone(&inv, testResources(), nil, 1800000000)

	if len(added) != 1 || added[0] != "lxc/101" {
		t.Fatalf("marked = %v, want only lxc/101", added)
	}
	if len(inv.Nodes) != 3 {
		t.Fatalf("🔴 someone was DELETED: %v — the rule forbids it", idsDe(inv))
	}
	byID := map[string]Node{}
	for _, n := range inv.Nodes {
		byID[n.ID] = n
	}
	if byID["lxc/101"].MissingSince != 1800000000 {
		t.Errorf("lxc/101 with no absence timestamp: %d", byID["lxc/101"].MissingSince)
	}
	if byID["lxc/207"].MissingSince != 0 || byID["node/pve"].MissingSince != 0 {
		t.Errorf("a node that is present was marked missing")
	}
}

func TestAbsenceStampNotRewritten(t *testing.T) {
	inv := Inventory{Nodes: []Node{{ID: "lxc/101", Kind: NodeKindGuest, Transport: TransportPVEAPI, VMID: 101}}}
	markGone(&inv, testResources(), nil, 1800000000)
	added := markGone(&inv, testResources(), nil, 1800009999)
	if added != nil {
		t.Errorf("the second pass announced it again: %v", added)
	}
	if inv.Nodes[0].MissingSince != 1800000000 {
		t.Errorf("timestamp = %d, want the FIRST one (1800000000)", inv.Nodes[0].MissingSince)
	}
}

func TestReturningNodeLosesMissingMark(t *testing.T) {
	inv := Inventory{Nodes: []Node{{ID: "lxc/207", Kind: NodeKindGuest, Transport: TransportPVEAPI, VMID: 207, MissingSince: 1799999999}}}
	markGone(&inv, testResources(), nil, 1800000000)
	if inv.Nodes[0].MissingSince != 0 {
		t.Errorf("the node came back and is still marked missing: %d", inv.Nodes[0].MissingSince)
	}
}

func TestEmptyResponseMarksNobody(t *testing.T) {
	inv := Inventory{Nodes: []Node{
		{ID: "lxc/207", Kind: NodeKindGuest, Transport: TransportPVEAPI, VMID: 207},
		{ID: "qemu/208", Kind: NodeKindGuest, Transport: TransportPVEAPI, VMID: 208},
	}}
	if added := markGone(&inv, nil, nil, 1800000000); added != nil {
		t.Errorf("an empty response marked %v", added)
	}
	if added := markGone(&inv, []pve.Resource{}, nil, 1800000000); added != nil {
		t.Errorf("an empty response marked %v", added)
	}
	for _, n := range inv.Nodes {
		if n.MissingSince != 0 {
			t.Fatalf("🔴 the lab was marked missing because of an empty response: %s", n.ID)
		}
	}
}

func TestOtherTransportNodeNotMarked(t *testing.T) {
	inv := Inventory{Nodes: []Node{
		{ID: "canary", Kind: "external", Transport: TransportAgent, Address: "127.0.0.1:9"},
		{ID: "ssh-machine", Transport: TransportSSH},
		{ID: "lxc/207", Kind: NodeKindGuest, Transport: TransportPVEAPI, VMID: 207},
	}}
	if added := markGone(&inv, testResources(), nil, 1800000000); added != nil {
		t.Errorf("marked a node from another transport: %v", added)
	}
	for _, n := range inv.Nodes {
		if n.MissingSince != 0 {
			t.Errorf("%s was marked missing without the hypervisor owning it", n.ID)
		}
	}
}

func TestDeclaredSeedNotMarkedMissing(t *testing.T) {
	inv := Inventory{Nodes: []Node{{ID: "lxc/999", Kind: NodeKindGuest, Transport: TransportPVEAPI, VMID: 999}}}
	seeds := []Node{{ID: "lxc/999", Transport: TransportPVEAPI}}
	if added := markGone(&inv, testResources(), seeds, 1800000000); added != nil {
		t.Errorf("marked a node DECLARED in seeds: %v", added)
	}
}

func TestDiscoveryFailureDoesNotMarkAbsence(t *testing.T) {
	f := &fakePVE{resources: testResources()}
	p, st, _ := newTestPoller(t, f, Sources{}, PollerConfig{})
	if err := p.tick(context.Background()); err != nil {
		t.Fatalf("first tick: %v", err)
	}
	invBefore, err := st.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	before := idsDe(invBefore)
	if len(before) < 2 {
		t.Fatalf("the first tick did not populate the inventory: %v", before)
	}

	f.failure = errors.New("hypervisor unreachable")
	if err := p.tick(context.Background()); err == nil {
		t.Fatal("the tick with failed discovery returned nil")
	}
	invAfter, err := st.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if after := idsDe(invAfter); len(after) != len(before) {
		t.Errorf("🔴 failed discovery deleted nodes: before %v, after %v", before, after)
	}
	for _, n := range invAfter.Nodes {
		if n.MissingSince != 0 {
			t.Errorf("🔴 FAILED discovery marked %s as missing — 'nobody told me' became 'it is not there'", n.ID)
		}
	}
}
