package inventory

import (
	"reflect"
	"testing"
	"time"

	"server-control-panel/internal/pve"
)

func testPools() []pve.Storage {
	return []pve.Storage{
		{Storage: "local-zfs", Type: "zfspool", Content: "images,rootdir",
			Total: 978416107520, Used: 67198091264, Avail: 911218016256,
			UsedFraction: 0.068680483433912, Active: 1, Enabled: 1, Shared: 0},
		{Storage: "pbs", Type: "pbs", Content: "backup",
			Total: 916405092352, Used: 46299873280, Avail: 870105219072,
			UsedFraction: 0.0505233697045147, Active: 1, Enabled: 1, Shared: 1},
	}
}

func testZPools() []pve.ZPool {
	return []pve.ZPool{
		{Name: "backup", Health: "ONLINE", Size: 996432412672, Alloc: 95457288192, Free: 900975124480, Frag: 0},
		{Name: "rpool", Health: "ONLINE", Size: 1013612281856, Alloc: 70999646208, Free: 942612635648, Frag: 17},
	}
}

func TestStorageAgeDoesNotPiggybackOnHealth(t *testing.T) {
	var inv Inventory
	applyStorage(&inv, testPools(), 1800000000)
	applyZPools(&inv, testZPools(), 1800000000)
	applyHypervisor(&inv, "pve", testStatus(), 1800000300)

	now := time.Unix(1800000300, 0)
	if v := ViewHypervisor(inv.Hypervisor, 90*time.Second, now); v.AgeSeconds != 0 {
		t.Errorf("health age = %d, want 0 — it was JUST observed", v.AgeSeconds)
	}
	vs := ViewStorage(inv.Hypervisor, 90*time.Second, now)
	if vs.AgeSeconds != 300 {
		t.Errorf("storage age = %d, want 300 — it is riding on the health timestamp (A-5)", vs.AgeSeconds)
	}
	if !vs.Stale {
		t.Error("a 5-minute-old storage with a 90 s TTL has to count as expired")
	}
	vz := ViewZPools(inv.Hypervisor, 90*time.Second, now)
	if vz.AgeSeconds != 300 {
		t.Errorf("zpools age = %d, want 300 (its own timestamp)", vz.AgeSeconds)
	}
}

func TestHealthStampClassifiesEveryField(t *testing.T) {
	outsideHealth := map[string]bool{
		"Storage":        true,
		"ZPools":         true,
		"DatastoreAudit": true,
	}

	tp := reflect.TypeOf(Hypervisor{})
	for i := 0; i < tp.NumField(); i++ {
		f := tp.Field(i)
		if f.Name == "Node" {
			continue
		}
		h := reflect.New(tp).Elem()
		h.Field(i).FieldByName("ObservedAt").SetInt(1800000000)
		seen := hypervisorObservedAt(h.Interface().(Hypervisor)) == 1800000000

		if outsideHealth[f.Name] && seen {
			t.Errorf("%s feeds the HEALTH stamp: it would make the hypervisor card look fresh "+
				"with an observation that is not its own", f.Name)
		}
		if !outsideHealth[f.Name] && !seen {
			t.Errorf("%s does NOT feed the health stamp: its age would never count "+
				"(or it is a new field nobody classified)", f.Name)
		}
	}
}

func TestNeverObservedSaysSoInNewBlocks(t *testing.T) {
	now := time.Unix(1800000000, 0)
	vs := ViewStorage(Hypervisor{}, 90*time.Second, now)
	if vs.AgeSeconds != -1 || !vs.Stale {
		t.Errorf("never-observed storage = age %d stale %v, want -1/true", vs.AgeSeconds, vs.Stale)
	}
	if vs.Pools == nil {
		t.Error("Pools nil becomes `null` in the JSON; the screen needs [] to say 'nothing here'")
	}
	vz := ViewZPools(Hypervisor{}, 90*time.Second, now)
	if vz.AgeSeconds != -1 || !vz.Stale {
		t.Errorf("never-observed zpools = age %d stale %v, want -1/true", vz.AgeSeconds, vz.Stale)
	}
	if vz.Pools == nil {
		t.Error("Pools nil becomes `null` in the JSON")
	}
}

func TestEmptyWithPrivilegeDiffersFromEmptyWithoutPrivilege(t *testing.T) {
	now := time.Unix(1800000000, 0)

	var withPriv Inventory
	applyStorage(&withPriv, nil, 1800000000)
	applyDatastoreAudit(&withPriv, true, 1800000000)

	var noPriv Inventory
	applyStorage(&noPriv, nil, 1800000000)
	applyDatastoreAudit(&noPriv, false, 1800000000)

	a := ViewStorage(withPriv.Hypervisor, 90*time.Second, now)
	b := ViewStorage(noPriv.Hypervisor, 90*time.Second, now)
	if len(a.Pools) != 0 || len(b.Pools) != 0 {
		t.Fatal("both lists have to be empty — that is the premise")
	}
	if a.DatastoreAudit.Value == b.DatastoreAudit.Value {
		t.Fatal("the privilege verdict does not tell the two kinds of empty apart — the screen has no way to")
	}
	if !a.DatastoreAudit.Value {
		t.Error("with privilege, datastore_audit has to be true (empty = there really is no storage)")
	}
	if b.DatastoreAudit.Value {
		t.Error("without privilege, datastore_audit has to be false (empty = the list was filtered by the ACL)")
	}
	if a.DatastoreAudit.ObservedAt != 1800000000 {
		t.Error("the verdict travels WITHOUT a timestamp — an old verdict presented as live")
	}
}

func TestNeverObservedVerdictDoesNotFakeGreen(t *testing.T) {
	v := ViewStorage(Hypervisor{}, 90*time.Second, time.Unix(1800000000, 0))
	if v.DatastoreAudit.ObservedAt != 0 {
		t.Errorf("ObservedAt = %d, want 0 (never observed)", v.DatastoreAudit.ObservedAt)
	}
	if v.DatastoreAudit.Value {
		t.Error("a never-observed verdict cannot be born true")
	}
}

func TestPoolsArriveNormalized(t *testing.T) {
	var inv Inventory
	applyStorage(&inv, testPools(), 1800000000)
	ps := inv.Hypervisor.Storage.Value
	if len(ps) != 2 {
		t.Fatalf("len = %d", len(ps))
	}
	lz := ps[0]
	if lz.ID != "local-zfs" {
		t.Fatalf("order/ID = %q, want local-zfs first", lz.ID)
	}
	if len(lz.Content) != 2 || lz.Content[0] != "images" || lz.Content[1] != "rootdir" {
		t.Errorf("Content = %v, want [images rootdir]", lz.Content)
	}
	if !lz.IsActive || !lz.IsEnabled || lz.IsShared {
		t.Errorf("flags = active %v enabled %v shared %v", lz.IsActive, lz.IsEnabled, lz.IsShared)
	}
	if lz.UsedPct < 6.8 || lz.UsedPct > 7.0 {
		t.Errorf("UsedPct = %v, want ~6.87 (from the PVE's used_fraction)", lz.UsedPct)
	}
	if ps[1].ID != "pbs" || !ps[1].IsShared {
		t.Errorf("pbs = %+v, want shared", ps[1])
	}
}

func TestUsedPctFallsBackToComputedWhenPVEOmitsIt(t *testing.T) {
	var inv Inventory
	applyStorage(&inv, []pve.Storage{{
		Storage: "almost-full", Type: "dir",
		Total: 1000, Used: 950, Avail: 50, UsedFraction: 0, Active: 1, Enabled: 1,
	}}, 1800000000)
	p := inv.Hypervisor.Storage.Value[0]
	if p.UsedPct < 94.9 || p.UsedPct > 95.1 {
		t.Errorf("UsedPct = %v, want 95 — with no used_fraction, used/total is the way out, not 0", p.UsedPct)
	}
	var inv2 Inventory
	applyStorage(&inv2, []pve.Storage{{Storage: "empty", Total: 0, Used: 0}}, 1800000000)
	if got := inv2.Hypervisor.Storage.Value[0].UsedPct; got != 0 {
		t.Errorf("UsedPct of a storage with no total = %v, want 0", got)
	}
}

func TestZPoolsArriveWithLiteralHealth(t *testing.T) {
	var inv Inventory
	applyZPools(&inv, []pve.ZPool{
		{Name: "rpool", Health: "DEGRADED", Size: 100, Alloc: 40, Free: 60, Frag: 3},
	}, 1800000000)
	p := inv.Hypervisor.ZPools.Value[0]
	if p.Health != "DEGRADED" {
		t.Errorf("Health = %q", p.Health)
	}
	if p.Healthy {
		t.Error("Healthy = true for DEGRADED — on a single-disk pool that is the most expensive news in the lab")
	}
	if p.FragPct != 3 {
		t.Errorf("FragPct = %d, want 3", p.FragPct)
	}
}
