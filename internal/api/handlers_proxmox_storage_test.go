package api

import (
	"net/http"
	"testing"

	"server-control-panel/internal/inventory"
	"server-control-panel/internal/pve"
)

// handlers_proxmox_storage_test.go — the pins for /api/proxmox/storage and
// /api/proxmox/zfs.
//
// 🔴 The permission guard from the earlier pass is still alive, and it is what
// these tests exercise IN BOTH STATES. The ACL was granted and the "this token
// cannot see /storage" banner disappears on its own — but it disappears because
// the verdict PASSED, not because somebody deleted the check. A guard that no
// longer knows how to fail has stopped being a guard and become decoration.

// stampCapacity puts capacity, zpools and the verdict into the store with the
// stamp asked for. It builds the document ALREADY NORMALIZED — normalization
// (content into a list, 0|1 into a boolean, fraction into a percentage) belongs to
// internal/inventory and has its own pin there. What is proved here is what the route DELIVERS.
func stampCapacity(t *testing.T, st *inventory.Store, pools []inventory.StoragePool, zs []inventory.ZPool, can bool, when int64) {
	t.Helper()
	if err := st.Replace(func(iv *inventory.Inventory) {
		iv.Hypervisor.Storage = inventory.Observe(pools, when)
		iv.Hypervisor.ZPools = inventory.Observe(zs, when)
		iv.Hypervisor.DatastoreAudit = inventory.Observe(can, when)
	}); err != nil {
		t.Fatal(err)
	}
}

// livePools and liveZPools are the NUMBERS MEASURED on the home hypervisor
// after the ACL: 4 storages and 2 zpools. The two most significant of each go
// here — the image pool and the PBS datastore; rpool and backup.
func livePools() []inventory.StoragePool {
	return []inventory.StoragePool{
		{ID: "local-zfs", Type: "zfspool", Content: []string{"images", "rootdir"},
			Total: 978416107520, Used: 67198091264, Avail: 911218016256,
			UsedPct: 6.8680483433912, IsActive: true, IsEnabled: true},
		{ID: "pbs", Type: "pbs", Content: []string{"backup"},
			Total: 916405092352, Used: 46299873280, Avail: 870105219072,
			UsedPct: 5.05233697045147, IsActive: true, IsEnabled: true, IsShared: true},
	}
}

func liveZPools() []inventory.ZPool {
	return []inventory.ZPool{
		{Name: "backup", Health: "ONLINE", Healthy: true, Size: 996432412672, Alloc: 95457288192, Free: 900975124480, FragPct: 0},
		{Name: "rpool", Health: "ONLINE", Healthy: true, Size: 1013612281856, Alloc: 70999646208, Free: 942612635648, FragPct: 17},
	}
}

// 🔴 TestCapacityComesFromStoreWithoutCallingHypervisor: capacity and zpool are a
// HEARTBEAT, exactly like health — and for the same reason. If the route dialled
// out, the age on display would always be "0 s" and the block would hide the very
// case it exists to show: the storage that STOPPED being observed.
func TestCapacityComesFromStoreWithoutCallingHypervisor(t *testing.T) {
	for _, path := range []string{"/api/proxmox/storage", "/api/proxmox/zfs"} {
		t.Run(path, func(t *testing.T) {
			r, st := newProxmoxRouter(t, defaultVault(), nil)
			stampCapacity(t, st, livePools(), liveZPools(), true, testNow-45)
			r.pveDial = func(tokenValue string) (hypervisorOps, error) {
				t.Fatalf("GET %s dialed the hypervisor — capacity is a heartbeat and comes out of the STORE", path)
				return nil, nil
			}

			w, out := callPVX(t, r, http.MethodGet, path, "")
			if w.Code != 200 {
				t.Fatalf("status = %d, body = %s", w.Code, w.Body)
			}
			age, has := out["age_seconds"]
			if !has {
				t.Fatalf("response without age_seconds: %s", w.Body)
			}
			if age.(float64) != 45 {
				t.Errorf("age_seconds = %v, want 45 (the block's own timestamp)", age)
			}
			if _, ok := out["stale"]; !ok {
				t.Error("response without stale")
			}
			if _, ok := out["ttl_seconds"]; !ok {
				t.Error("response without ttl_seconds")
			}
			if out["node"] != "pve" {
				t.Errorf("node = %v", out["node"])
			}
		})
	}
}

// TestStorageDeliversFourBarNumbers: the screen draws a usage bar, and it
// needs the percentage AND the bytes. The percentage alone hides the difference
// between 90% of 1 GB and 90% of 1 TB.
func TestStorageDeliversFourBarNumbers(t *testing.T) {
	r, st := newProxmoxRouter(t, defaultVault(), nil)
	stampCapacity(t, st, livePools(), liveZPools(), true, testNow-10)

	w, out := callPVX(t, r, http.MethodGet, "/api/proxmox/storage", "")
	if w.Code != 200 {
		t.Fatalf("status = %d: %s", w.Code, w.Body)
	}
	pools, _ := out["pools"].([]any)
	if len(pools) != 2 {
		t.Fatalf("pools = %d, want 2: %s", len(pools), w.Body)
	}
	p := pools[0].(map[string]any)
	if p["id"] != "local-zfs" {
		t.Errorf("id = %v, want local-zfs first", p["id"])
	}
	for _, field := range []string{"used_pct", "used", "total", "avail", "type", "content", "ativo"} {
		if _, ok := p[field]; !ok {
			t.Errorf("pool without %q: %s", field, w.Body)
		}
	}
	if pct := p["used_pct"].(float64); pct < 6.8 || pct > 7.0 {
		t.Errorf("used_pct = %v, want ~6.87", pct)
	}
	if c, _ := p["content"].([]any); len(c) != 2 {
		t.Errorf("content = %v, want a list with 2 items", p["content"])
	}
}

// TestZfsDeliversHealthFragAndAllocation: health, frag and alloc/free — the three the
// operator cannot reach from outside the house today.
func TestZfsDeliversHealthFragAndAllocation(t *testing.T) {
	r, st := newProxmoxRouter(t, defaultVault(), nil)
	stampCapacity(t, st, livePools(), liveZPools(), true, testNow-10)

	w, out := callPVX(t, r, http.MethodGet, "/api/proxmox/zfs", "")
	if w.Code != 200 {
		t.Fatalf("status = %d: %s", w.Code, w.Body)
	}
	pools, _ := out["pools"].([]any)
	if len(pools) != 2 {
		t.Fatalf("pools = %d, want 2: %s", len(pools), w.Body)
	}
	rp := pools[1].(map[string]any)
	if rp["name"] != "rpool" || rp["health"] != "ONLINE" || rp["saudavel"] != true {
		t.Errorf("rpool = %+v", rp)
	}
	if rp["frag_pct"].(float64) != 17 {
		t.Errorf("frag_pct = %v, want 17", rp["frag_pct"])
	}
	for _, field := range []string{"alloc", "free", "size"} {
		if _, ok := rp[field]; !ok {
			t.Errorf("zpool without %q", field)
		}
	}
}

// 🔴 TestEmptyWithAndWithoutPrivilegeAreDIFFERENTResponses is that earlier guard,
// exercised in BOTH states from the SAME empty list. As long as this test passes,
// the screen never has to guess why the block is empty.
func TestEmptyWithAndWithoutPrivilegeAreDIFFERENTResponses(t *testing.T) {
	cases := []struct {
		name string
		can  bool
	}{
		{"with privilege: empty really is empty", true},
		{"without privilege: empty is the ACL filtering", false},
	}
	seen := map[bool]any{}
	for _, cs := range cases {
		t.Run(cs.name, func(t *testing.T) {
			r, st := newProxmoxRouter(t, defaultVault(), nil)
			stampCapacity(t, st, nil, nil, cs.can, testNow-5)

			w, out := callPVX(t, r, http.MethodGet, "/api/proxmox/storage", "")
			if w.Code != 200 {
				t.Fatalf("status = %d: %s", w.Code, w.Body)
			}
			if pools, _ := out["pools"].([]any); len(pools) != 0 {
				t.Fatalf("pools = %v, want empty (that is the test's premise)", pools)
			}
			da, _ := out["datastore_audit"].(map[string]any)
			if da == nil {
				t.Fatalf("response without datastore_audit — the screen has no way to tell the two empties apart: %s", w.Body)
			}
			if da["value"] != cs.can {
				t.Errorf("datastore_audit.value = %v, want %v", da["value"], cs.can)
			}
			if da["observed_at"].(float64) != float64(testNow-5) {
				t.Errorf("verdict without its own timestamp: %v", da["observed_at"])
			}
			seen[cs.can] = da["value"]
		})
	}
	if seen[true] == seen[false] {
		t.Fatal("the two empties produced the SAME response — the guard stopped telling them apart")
	}
}

// 🔴 TestPermissionsUseSameDatastoreVerdict: the dashboard may have only ONE
// answer to "does this token see storage?". The /permissions route and the
// capacity block have to come out of the SAME function (pve.CanAuditDatastore)
// — two copies of the rule diverge in silence, and this repo has already paid for
// that (credentialKey, handlers_nodes.go).
//
// And both states are exercised: the path being present WITHOUT the privilege has
// to FAIL, which is the mutation the earlier pass could not catch.
func TestPermissionsUseSameDatastoreVerdict(t *testing.T) {
	cases := []struct {
		name  string
		perms map[string]map[string]int
		want  bool
	}{
		{
			"before the ACL: neither path nor privilege",
			map[string]map[string]int{"/vms/204": {"VM.Audit": 1}, "/nodes": {"Sys.Audit": 1}},
			false,
		},
		{
			"path present, privilege missing: the false green a path-presence check would let through",
			map[string]map[string]int{"/storage": {"VM.Audit": 1}},
			false,
		},
		{
			"after the ACL: Datastore.Audit propagated from the root (the live state)",
			map[string]map[string]int{
				"/":        {"Datastore.Audit": 1, "Sys.Audit": 1, "VM.Audit": 1},
				"/storage": {"Datastore.Audit": 1, "Sys.Audit": 1},
				"/vms/204": {"VM.Audit": 1},
			},
			true,
		},
	}
	for _, cs := range cases {
		t.Run(cs.name, func(t *testing.T) {
			fake := &fakePVE{perms: cs.perms}
			r, _ := newProxmoxRouter(t, defaultVault(), fake)

			w, out := callPVX(t, r, http.MethodGet, "/api/proxmox/permissions", "")
			if w.Code != 200 {
				t.Fatalf("status = %d: %s", w.Code, w.Body)
			}
			if out["storage_visivel"] != cs.want {
				t.Errorf("storage_visivel = %v, want %v — the screen's verdict diverged from pve.CanAuditDatastore",
					out["storage_visivel"], cs.want)
			}
			// The source of truth, called directly: the two have to agree
			// ALWAYS, and not only in the cases I remembered to write down.
			if pve.CanAuditDatastore(cs.perms) != cs.want {
				t.Fatalf("the test case is wrong, not the handler")
			}
		})
	}
}

// TestCapacityOnlyAnswersGET: both routes are pure reads. A POST here is
// neither 404 nor 500 — it is 405, and saying so saves an investigation.
func TestCapacityOnlyAnswersGET(t *testing.T) {
	for _, path := range []string{"/api/proxmox/storage", "/api/proxmox/zfs"} {
		r, _ := newProxmoxRouter(t, defaultVault(), nil)
		w, _ := callPVX(t, r, http.MethodPost, path, "")
		if w.Code != 405 {
			t.Errorf("POST %s = %d, want 405", path, w.Code)
		}
	}
}

// TestCapacityNeverObservedSaysSo: before the first tick, age -1 and an empty
// list — and the verdict WITHOUT a stamp, so the screen does not report a missing
// permission that nobody measured.
func TestCapacityNeverObservedSaysSo(t *testing.T) {
	r, _ := newProxmoxRouter(t, defaultVault(), nil)
	w, out := callPVX(t, r, http.MethodGet, "/api/proxmox/storage", "")
	if w.Code != 200 {
		t.Fatalf("status = %d: %s", w.Code, w.Body)
	}
	if out["age_seconds"].(float64) != -1 {
		t.Errorf("age_seconds = %v, want -1 (never observed, never 0)", out["age_seconds"])
	}
	if out["stale"] != true {
		t.Error("never observed has to count as expired")
	}
	if pools, ok := out["pools"].([]any); !ok || pools == nil {
		t.Errorf("pools = %v, want [] and never null", out["pools"])
	}
	da, _ := out["datastore_audit"].(map[string]any)
	if da == nil || da["observed_at"].(float64) != 0 {
		t.Errorf("datastore_audit = %v, want a 0 timestamp (nobody has asked yet)", da)
	}
}
