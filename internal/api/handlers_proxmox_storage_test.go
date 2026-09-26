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

// carimbaCapacidade puts capacity, zpools and the verdict into the store with the
// stamp asked for. It builds the document ALREADY NORMALIZED — normalization
// (content into a list, 0|1 into a boolean, fraction into a percentage) belongs to
// internal/inventory and has its own pin there. What is proved here is what the route DELIVERS.
func carimbaCapacidade(t *testing.T, st *inventory.Store, pools []inventory.StoragePool, zs []inventory.ZPool, pode bool, quando int64) {
	t.Helper()
	if err := st.Replace(func(iv *inventory.Inventory) {
		iv.Hypervisor.Storage = inventory.Observe(pools, quando)
		iv.Hypervisor.ZPools = inventory.Observe(zs, quando)
		iv.Hypervisor.DatastoreAudit = inventory.Observe(pode, quando)
	}); err != nil {
		t.Fatal(err)
	}
}

// poolsVivos and zpoolsVivos are the NUMBERS MEASURED on the home hypervisor
// after the ACL: 4 storages and 2 zpools. The two most significant of each go
// here — the image pool and the PBS datastore; rpool and backup.
func poolsVivos() []inventory.StoragePool {
	return []inventory.StoragePool{
		{ID: "local-zfs", Type: "zfspool", Content: []string{"images", "rootdir"},
			Total: 978416107520, Used: 67198091264, Avail: 911218016256,
			UsedPct: 6.8680483433912, Ativo: true, Habilitado: true},
		{ID: "pbs", Type: "pbs", Content: []string{"backup"},
			Total: 916405092352, Used: 46299873280, Avail: 870105219072,
			UsedPct: 5.05233697045147, Ativo: true, Habilitado: true, Compartilhado: true},
	}
}

func zpoolsVivos() []inventory.ZPool {
	return []inventory.ZPool{
		{Name: "backup", Health: "ONLINE", Saudavel: true, Size: 996432412672, Alloc: 95457288192, Free: 900975124480, FragPct: 0},
		{Name: "rpool", Health: "ONLINE", Saudavel: true, Size: 1013612281856, Alloc: 70999646208, Free: 942612635648, FragPct: 17},
	}
}

// 🔴 TestCapacidadeVemDoStoreSemChamarOHipervisor: capacity and zpool are a
// HEARTBEAT, exactly like health — and for the same reason. If the route dialled
// out, the age on display would always be "0 s" and the block would hide the very
// case it exists to show: the storage that STOPPED being observed.
func TestCapacidadeVemDoStoreSemChamarOHipervisor(t *testing.T) {
	for _, caminho := range []string{"/api/proxmox/storage", "/api/proxmox/zfs"} {
		t.Run(caminho, func(t *testing.T) {
			r, st := novoRouterProxmox(t, cofrePadrao(), nil)
			carimbaCapacidade(t, st, poolsVivos(), zpoolsVivos(), true, agoraDeTeste-45)
			r.pveDial = func(tokenValor string) (hypervisorOps, error) {
				t.Fatalf("GET %s dialed the hypervisor — capacity is a heartbeat and comes out of the STORE", caminho)
				return nil, nil
			}

			w, out := chamaPVX(t, r, http.MethodGet, caminho, "")
			if w.Code != 200 {
				t.Fatalf("status = %d, body = %s", w.Code, w.Body)
			}
			idade, tem := out["age_seconds"]
			if !tem {
				t.Fatalf("response without age_seconds: %s", w.Body)
			}
			if idade.(float64) != 45 {
				t.Errorf("age_seconds = %v, want 45 (the block's own timestamp)", idade)
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

// TestStorageEntregaOsQuatroNumerosDaBarra: the screen draws a usage bar, and it
// needs the percentage AND the bytes. The percentage alone hides the difference
// between 90% of 1 GB and 90% of 1 TB.
func TestStorageEntregaOsQuatroNumerosDaBarra(t *testing.T) {
	r, st := novoRouterProxmox(t, cofrePadrao(), nil)
	carimbaCapacidade(t, st, poolsVivos(), zpoolsVivos(), true, agoraDeTeste-10)

	w, out := chamaPVX(t, r, http.MethodGet, "/api/proxmox/storage", "")
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
	for _, campo := range []string{"used_pct", "used", "total", "avail", "type", "content", "ativo"} {
		if _, ok := p[campo]; !ok {
			t.Errorf("pool without %q: %s", campo, w.Body)
		}
	}
	if pct := p["used_pct"].(float64); pct < 6.8 || pct > 7.0 {
		t.Errorf("used_pct = %v, want ~6.87", pct)
	}
	if c, _ := p["content"].([]any); len(c) != 2 {
		t.Errorf("content = %v, want a list with 2 items", p["content"])
	}
}

// TestZfsEntregaSaudeFragEAlocacao: health, frag and alloc/free — the three the
// operator cannot reach from outside the house today.
func TestZfsEntregaSaudeFragEAlocacao(t *testing.T) {
	r, st := novoRouterProxmox(t, cofrePadrao(), nil)
	carimbaCapacidade(t, st, poolsVivos(), zpoolsVivos(), true, agoraDeTeste-10)

	w, out := chamaPVX(t, r, http.MethodGet, "/api/proxmox/zfs", "")
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
	for _, campo := range []string{"alloc", "free", "size"} {
		if _, ok := rp[campo]; !ok {
			t.Errorf("zpool without %q", campo)
		}
	}
}

// 🔴 TestVazioComEsemPrivilegioSaoRespostasDIFERENTES is that earlier guard,
// exercised in BOTH states from the SAME empty list. As long as this test passes,
// the screen never has to guess why the block is empty.
func TestVazioComEsemPrivilegioSaoRespostasDIFERENTES(t *testing.T) {
	casos := []struct {
		nome string
		pode bool
	}{
		{"com privilégio: vazio é vazio de verdade", true},
		{"sem privilégio: vazio é a ACL filtrando", false},
	}
	vistos := map[bool]any{}
	for _, cs := range casos {
		t.Run(cs.nome, func(t *testing.T) {
			r, st := novoRouterProxmox(t, cofrePadrao(), nil)
			carimbaCapacidade(t, st, nil, nil, cs.pode, agoraDeTeste-5)

			w, out := chamaPVX(t, r, http.MethodGet, "/api/proxmox/storage", "")
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
			if da["value"] != cs.pode {
				t.Errorf("datastore_audit.value = %v, want %v", da["value"], cs.pode)
			}
			if da["observed_at"].(float64) != float64(agoraDeTeste-5) {
				t.Errorf("verdict without its own timestamp: %v", da["observed_at"])
			}
			vistos[cs.pode] = da["value"]
		})
	}
	if vistos[true] == vistos[false] {
		t.Fatal("the two empties produced the SAME response — the guard stopped telling them apart")
	}
}

// 🔴 TestPermissoesUsamOMesmoVereditoDeDatastore: the dashboard may have only ONE
// answer to "does this token see storage?". The /permissions route and the
// capacity block have to come out of the SAME function (pve.PodeAuditarDatastore)
// — two copies of the rule diverge in silence, and this repo has already paid for
// that (chaveDeCredencial, handlers_nodes.go).
//
// And both states are exercised: the path being present WITHOUT the privilege has
// to FAIL, which is the mutation the earlier pass could not catch.
func TestPermissoesUsamOMesmoVereditoDeDatastore(t *testing.T) {
	casos := []struct {
		nome  string
		perms map[string]map[string]int
		quer  bool
	}{
		{
			"antes da ACL: nem caminho, nem privilégio",
			map[string]map[string]int{"/vms/204": {"VM.Audit": 1}, "/nodes": {"Sys.Audit": 1}},
			false,
		},
		{
			"🔴 caminho presente, privilégio ausente — o falso-verde que presença-de-caminho deixaria passar",
			map[string]map[string]int{"/storage": {"VM.Audit": 1}},
			false,
		},
		{
			"depois da ACL: Datastore.Audit propagado da raiz (é o estado VIVO de hoje)",
			map[string]map[string]int{
				"/":        {"Datastore.Audit": 1, "Sys.Audit": 1, "VM.Audit": 1},
				"/storage": {"Datastore.Audit": 1, "Sys.Audit": 1},
				"/vms/204": {"VM.Audit": 1},
			},
			true,
		},
	}
	for _, cs := range casos {
		t.Run(cs.nome, func(t *testing.T) {
			fake := &pveFalso{perms: cs.perms}
			r, _ := novoRouterProxmox(t, cofrePadrao(), fake)

			w, out := chamaPVX(t, r, http.MethodGet, "/api/proxmox/permissions", "")
			if w.Code != 200 {
				t.Fatalf("status = %d: %s", w.Code, w.Body)
			}
			if out["storage_visivel"] != cs.quer {
				t.Errorf("storage_visivel = %v, want %v — the screen's verdict diverged from pve.PodeAuditarDatastore",
					out["storage_visivel"], cs.quer)
			}
			// The source of truth, called directly: the two have to agree
			// ALWAYS, and not only in the cases I remembered to write down.
			if pve.PodeAuditarDatastore(cs.perms) != cs.quer {
				t.Fatalf("the test case is wrong, not the handler")
			}
		})
	}
}

// TestCapacidadeSoResponsdeGET: both routes are pure reads. A POST here is
// neither 404 nor 500 — it is 405, and saying so saves an investigation.
func TestCapacidadeSoRespondeGET(t *testing.T) {
	for _, caminho := range []string{"/api/proxmox/storage", "/api/proxmox/zfs"} {
		r, _ := novoRouterProxmox(t, cofrePadrao(), nil)
		w, _ := chamaPVX(t, r, http.MethodPost, caminho, "")
		if w.Code != 405 {
			t.Errorf("POST %s = %d, want 405", caminho, w.Code)
		}
	}
}

// TestCapacidadeNuncaObservadaDizIsso: before the first tick, age -1 and an empty
// list — and the verdict WITHOUT a stamp, so the screen does not report a missing
// permission that nobody measured.
func TestCapacidadeNuncaObservadaDizIsso(t *testing.T) {
	r, _ := novoRouterProxmox(t, cofrePadrao(), nil)
	w, out := chamaPVX(t, r, http.MethodGet, "/api/proxmox/storage", "")
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
