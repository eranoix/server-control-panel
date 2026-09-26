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

// pveStorageSource is the fakePVE from poller_test.go extended with the three
// new calls. A double of its own (and not more fields on fakePVE) because these
// tests need to register an error PER CALL — that is what separates "the
// storage failed" from "the tick failed".
type pveStorageSource struct {
	resources []pve.Resource

	status    pve.NodeStatus
	statusErr error

	pools    []pve.Storage
	poolsErr error
	noPools  string

	zpools    []pve.ZPool
	zpoolsErr error
	noZPools  string

	perms    map[string]map[string]int
	permsErr error

	poolCalls int
}

func (f *pveStorageSource) ClusterResources(ctx context.Context) ([]pve.Resource, error) {
	return f.resources, nil
}
func (f *pveStorageSource) GuestAddress(ctx context.Context, node string, vmid int, typ string) (string, error) {
	return "", nil
}
func (f *pveStorageSource) NodeStatus(ctx context.Context, node string) (pve.NodeStatus, error) {
	return f.status, f.statusErr
}
func (f *pveStorageSource) StorageList(ctx context.Context, node string) ([]pve.Storage, error) {
	f.poolCalls++
	f.noPools = node
	return f.pools, f.poolsErr
}
func (f *pveStorageSource) ZFSList(ctx context.Context, node string) ([]pve.ZPool, error) {
	f.noZPools = node
	return f.zpools, f.zpoolsErr
}
func (f *pveStorageSource) Permissions(ctx context.Context) (map[string]map[string]int, error) {
	return f.perms, f.permsErr
}

// renamedResources returns the discovery with a node name that is NOT "pve".
// It is the instrument of invariant 3: if somebody nails the hostname into the
// poller, the tests in this file point at the wrong name.
func renamedResources() []pve.Resource {
	return []pve.Resource{
		{ID: "lxc/204", Type: "lxc", VMID: 204, Name: "lab", Node: "hipervisor-renomeado", Status: "running"},
	}
}

func storagePoller(t *testing.T, f *pveStorageSource, now int64) (*Poller, *Store) {
	t.Helper()
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := NewPoller(st, f, Sources{}, PollerConfig{
		Now: func() time.Time { return time.Unix(now, 0) },
	})
	return p, st
}

// TestTickCollectsCapacityAndZpool: the happy path, with the node name coming
// from the DISCOVERY (invariant 3) — no "pve" nailed into the poller.
func TestTickCollectsCapacityAndZpool(t *testing.T) {
	f := &pveStorageSource{
		resources: renamedResources(),
		status:    testStatus(),
		pools:     testPools(),
		zpools:    testZPools(),
		perms:     map[string]map[string]int{"/": {"Datastore.Audit": 1}},
	}
	p, st := storagePoller(t, f, 1800000000)
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

// 🔴 TestStorageFailureKeepsPools is invariant 2 on the new block. Silent
// amnesia is WORSE than stale data: "no storage" and "I have not been able to
// see the storage for 30 min" are opposite readings, and only the second sends
// the operator to look at the hypervisor.
func TestStorageFailureKeepsPools(t *testing.T) {
	f := &pveStorageSource{
		resources: renamedResources(),
		status:    testStatus(),
		pools:     testPools(),
		zpools:    testZPools(),
		perms:     map[string]map[string]int{"/": {"Datastore.Audit": 1}},
	}
	p, st := storagePoller(t, f, 1800000000)
	if err := p.tick(context.Background()); err != nil {
		t.Fatalf("tick 1: %v", err)
	}

	// Tick 2, 5 min later: the three new calls fail.
	f.poolsErr = errors.New("hipervisor mudo")
	f.zpoolsErr = errors.New("hipervisor mudo")
	f.permsErr = errors.New("hipervisor mudo")
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

// TestNoHypervisorNameDoesNotAsk: with no discovery there is no node, and
// asking for the capacity of "" is fabricating a request with no target.
func TestNoHypervisorNameDoesNotAsk(t *testing.T) {
	f := &pveStorageSource{resources: []pve.Resource{}}
	p, _ := storagePoller(t, f, 1800000000)
	if err := p.tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if f.poolCalls != 0 {
		t.Errorf("StorageList was called %d time(s) with no hypervisor discovered", f.poolCalls)
	}
}

// 🔴 TestSemPrivilegioOVereditoVira false: the hypervisor returns 200 with []
// and the panel has to record BOTH things — the empty list AND the reason for it.
func TestNoPrivilegeVerdictBecomesFalse(t *testing.T) {
	f := &pveStorageSource{
		resources: renamedResources(),
		status:    testStatus(),
		pools:     nil, // this is EXACTLY what the hypervisor returns without the ACL
		zpools:    nil,
		perms:     map[string]map[string]int{"/vms/204": {"VM.Audit": 1}, "/nodes": {"Sys.Audit": 1}},
	}
	p, st := storagePoller(t, f, 1800000000)
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
