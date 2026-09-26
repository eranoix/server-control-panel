package inventory

import (
	"context"
	"errors"
	"testing"
	"time"

	"server-control-panel/internal/pve"
)

// poller_storage_test.go — the pins of the tick that started collecting
// capacity, zpool and the privilege verdict.
//
// The three go into the SAME tick as the health, and so the same invariants
// from the header of poller.go hold for them:
//
//	invariant 2 — a failure does NOT erase: the document keeps the old value
//	              AND the old timestamp, and the screen shows the age growing;
//	invariant 3 — no hostname lives in the poller: the node comes from the
//	              discovery.

// fontePVEStorage is the fakePVE from poller_test.go extended with the three
// new calls. A double of its own (and not more fields on fakePVE) because these
// tests need to register an error PER CALL — that is what separates "the
// storage failed" from "the tick failed".
type fontePVEStorage struct {
	recursos []pve.Resource

	status     pve.NodeStatus
	erroStatus error

	pools     []pve.Storage
	erroPools error
	noPools   string

	zpools     []pve.ZPool
	erroZPools error
	noZPools   string

	perms     map[string]map[string]int
	erroPerms error

	chamadasPools int
}

func (f *fontePVEStorage) ClusterResources(ctx context.Context) ([]pve.Resource, error) {
	return f.recursos, nil
}
func (f *fontePVEStorage) GuestAddress(ctx context.Context, node string, vmid int, typ string) (string, error) {
	return "", nil
}
func (f *fontePVEStorage) NodeStatus(ctx context.Context, node string) (pve.NodeStatus, error) {
	return f.status, f.erroStatus
}
func (f *fontePVEStorage) StorageList(ctx context.Context, node string) ([]pve.Storage, error) {
	f.chamadasPools++
	f.noPools = node
	return f.pools, f.erroPools
}
func (f *fontePVEStorage) ZFSList(ctx context.Context, node string) ([]pve.ZPool, error) {
	f.noZPools = node
	return f.zpools, f.erroZPools
}
func (f *fontePVEStorage) Permissions(ctx context.Context) (map[string]map[string]int, error) {
	return f.perms, f.erroPerms
}

// recursosRenomeados returns the discovery with a node name that is NOT "pve".
// It is the instrument of invariant 3: if somebody nails the hostname into the
// poller, the tests in this file point at the wrong name.
func recursosRenomeados() []pve.Resource {
	return []pve.Resource{
		{ID: "lxc/204", Type: "lxc", VMID: 204, Name: "lab", Node: "hipervisor-renomeado", Status: "running"},
	}
}

func pollerDeStorage(t *testing.T, f *fontePVEStorage, agora int64) (*Poller, *Store) {
	t.Helper()
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := NewPoller(st, f, Sources{}, PollerConfig{
		Now: func() time.Time { return time.Unix(agora, 0) },
	})
	return p, st
}

// TestTickColetaCapacidadeEZpool: the happy path, with the node name coming
// from the DISCOVERY (invariant 3) — no "pve" nailed into the poller.
func TestTickColetaCapacidadeEZpool(t *testing.T) {
	f := &fontePVEStorage{
		recursos: recursosRenomeados(),
		status:   statusDeTeste(),
		pools:    poolsDeTeste(),
		zpools:   zpoolsDeTeste(),
		perms:    map[string]map[string]int{"/": {"Datastore.Audit": 1}},
	}
	p, st := pollerDeStorage(t, f, 1800000000)
	if err := p.tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if f.noPools != "hipervisor-renomeado" || f.noZPools != "hipervisor-renomeado" {
		t.Errorf("nodes asked = %q/%q — want the DISCOVERED name, not a hard-coded hostname",
			f.noPools, f.noZPools)
	}
	inv, err := st.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if n := len(inv.Hypervisor.Storage.Value); n != 2 {
		t.Errorf("storages = %d, want 2", n)
	}
	if n := len(inv.Hypervisor.ZPools.Value); n != 2 {
		t.Errorf("zpools = %d, want 2", n)
	}
	if inv.Hypervisor.Storage.ObservedAt != 1800000000 {
		t.Errorf("storage timestamp = %d", inv.Hypervisor.Storage.ObservedAt)
	}
	if !inv.Hypervisor.DatastoreAudit.Value {
		t.Error("datastore_audit = false with Datastore.Audit at the root — the verdict was not collected")
	}
}

// 🔴 TestFalhaDoStorageNaoApagaOsPools is invariant 2 on the new block. Silent
// amnesia is WORSE than stale data: "no storage" and "I have not been able to
// see the storage for 30 min" are opposite readings, and only the second sends
// the operator to look at the hypervisor.
func TestFalhaDoStorageNaoApagaOsPools(t *testing.T) {
	f := &fontePVEStorage{
		recursos: recursosRenomeados(),
		status:   statusDeTeste(),
		pools:    poolsDeTeste(),
		zpools:   zpoolsDeTeste(),
		perms:    map[string]map[string]int{"/": {"Datastore.Audit": 1}},
	}
	p, st := pollerDeStorage(t, f, 1800000000)
	if err := p.tick(context.Background()); err != nil {
		t.Fatalf("tick 1: %v", err)
	}

	// Tick 2, 5 min later: the three new calls fail.
	f.erroPools = errors.New("hipervisor mudo")
	f.erroZPools = errors.New("hipervisor mudo")
	f.erroPerms = errors.New("hipervisor mudo")
	p2 := NewPoller(st, f, Sources{}, PollerConfig{
		Now: func() time.Time { return time.Unix(1800000300, 0) },
	})
	if err := p2.tick(context.Background()); err != nil {
		t.Fatalf("tick 2 returned an error (%v) — a storage failure is NEWS, not a fatality for the tick", err)
	}

	inv, err := st.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if n := len(inv.Hypervisor.Storage.Value); n != 2 {
		t.Errorf("storages = %d after the failure, want the 2 OLD ones kept", n)
	}
	if inv.Hypervisor.Storage.ObservedAt != 1800000000 {
		t.Errorf("storage timestamp = %d, want the OLD one (1800000000) — stamping it again would hide the failure",
			inv.Hypervisor.Storage.ObservedAt)
	}
	if inv.Hypervisor.ZPools.ObservedAt != 1800000000 {
		t.Errorf("zpools timestamp = %d, want the OLD one", inv.Hypervisor.ZPools.ObservedAt)
	}
	if !inv.Hypervisor.DatastoreAudit.Value || inv.Hypervisor.DatastoreAudit.ObservedAt != 1800000000 {
		t.Errorf("verdict = %+v, want the OLD one intact — losing the verdict would make the screen say 'no permission' because of a network failure",
			inv.Hypervisor.DatastoreAudit)
	}
	// And the health, which answered, moved on: the DIVERGENT age is what tells.
	if inv.Hypervisor.MemUsed.ObservedAt != 1800000300 {
		t.Errorf("health timestamp = %d, want 1800000300 — it answered on this tick",
			inv.Hypervisor.MemUsed.ObservedAt)
	}
}

// TestSemNomeDeHipervisorNaoPergunta: with no discovery there is no node, and
// asking for the capacity of "" is fabricating a request with no target.
func TestSemNomeDeHipervisorNaoPergunta(t *testing.T) {
	f := &fontePVEStorage{recursos: []pve.Resource{}}
	p, _ := pollerDeStorage(t, f, 1800000000)
	if err := p.tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if f.chamadasPools != 0 {
		t.Errorf("StorageList was called %d time(s) with no hypervisor discovered", f.chamadasPools)
	}
}

// 🔴 TestSemPrivilegioOVereditoVira false: the hypervisor returns 200 with []
// and the panel has to record BOTH things — the empty list AND the reason for it.
func TestSemPrivilegioOVereditoViraFalso(t *testing.T) {
	f := &fontePVEStorage{
		recursos: recursosRenomeados(),
		status:   statusDeTeste(),
		pools:    nil, // this is EXACTLY what the hypervisor returns without the ACL
		zpools:   nil,
		perms:    map[string]map[string]int{"/vms/204": {"VM.Audit": 1}, "/nodes": {"Sys.Audit": 1}},
	}
	p, st := pollerDeStorage(t, f, 1800000000)
	if err := p.tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	inv, _ := st.Snapshot()
	if inv.Hypervisor.DatastoreAudit.Value {
		t.Error("verdict = true with Datastore.Audit on no path at all")
	}
	if inv.Hypervisor.DatastoreAudit.ObservedAt != 1800000000 {
		t.Error("the NEGATIVE verdict needs a timestamp too — otherwise the screen cannot tell whether it is current")
	}
}
