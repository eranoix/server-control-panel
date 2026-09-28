package inventory

import (
	"context"
	"errors"
	"testing"
	"time"

	"server-control-panel/internal/pve"
)

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

func renamedResources() []pve.Resource {
	return []pve.Resource{
		{ID: "lxc/204", Type: "lxc", VMID: 204, Name: "lab", Node: "renamed-hypervisor", Status: "running"},
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
	if f.noPools != "renamed-hypervisor" || f.noZPools != "renamed-hypervisor" {
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

	f.poolsErr = errors.New("hypervisor silent")
	f.zpoolsErr = errors.New("hypervisor silent")
	f.permsErr = errors.New("hypervisor silent")
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
	if inv.Hypervisor.MemUsed.ObservedAt != 1800000300 {
		t.Errorf("health timestamp = %d, want 1800000300 — it answered on this tick",
			inv.Hypervisor.MemUsed.ObservedAt)
	}
}

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

func TestNoPrivilegeVerdictBecomesFalse(t *testing.T) {
	f := &pveStorageSource{
		resources: renamedResources(),
		status:    testStatus(),
		pools:     nil,
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
