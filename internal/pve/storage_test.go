package pve

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
)

// storage_test.go — the pins for the two CAPACITY routes and for the privilege
// verdict that goes with them.
//
// An earlier pass measured these three routes with the audit token, before it
// had any privilege outside /vms and /nodes:
//
//	/nodes/pve/storage    → 200 with []  ← not an error: an empty list lying
//	/nodes/pve/disks/zfs  → 403
//	/access/permissions   → 200, with no /storage in the map
//
// After `pveum acl modify / --roles PVEAuditor … --propagate 1` (applied by the
// operator), all three return real data. And it was `/`, not `/storage`,
// because that is what this hypervisor's Perl source demands:
// `/nodes/{n}/disks/zfs` asks for **Sys.Audit on `/`** (Disks/ZFS.pm:62-64) and
// `/nodes/{n}/storage` filters the list by `Datastore.Audit` storage by storage
// (Storage/Status.pm:72-76). That earlier pass wrote "they require an ACL on
// /storage" — it was false, and an ACL on /storage would have unlocked half the
// problem in silence. The fixtures in this file are the LITERAL response of the
// hypervisor after the ACL, saved off the wire.
//
// 🔴 The test that matters most here is TestPodeAuditarDatastoreExigeOPrivilegio.
// It defends the distinction that whole earlier pass existed to install: `200`
// with an empty list is indistinguishable from "does not exist", and what breaks
// the tie is the privilege MEASURED — not the presence of the path in the map.

const (
	fixtureStorage = "testdata/node-storage.json"
	fixtureZFS     = "testdata/node-disks-zfs.json"
	fixturePerms   = "testdata/access-permissions-auditor.json"
)

func corpoDaFixture(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(raw)
}

// ------------------------------------------------------------ StorageList ---

// TestStorageListLeAFormaReal uses the LITERAL response of /nodes/pve/storage —
// 4 storages, with `active`/`enabled`/`shared` arriving as 0|1 (the hypervisor
// does not send JSON booleans) and `content` as a comma-separated list, not as
// an array.
func TestStorageListLeAFormaReal(t *testing.T) {
	c, vista := capturaURL(t, corpoDaFixture(t, fixtureStorage))

	ss, err := c.StorageList(context.Background(), "pve")
	if err != nil {
		t.Fatalf("StorageList: %v", err)
	}
	if *vista != "/api2/json/nodes/pve/storage" {
		t.Errorf("URL = %q, want /api2/json/nodes/pve/storage", *vista)
	}
	if len(ss) != 4 {
		t.Fatalf("len = %d, want 4 storages (backupusb, local, local-zfs, pbs)", len(ss))
	}
	porID := map[string]Storage{}
	for _, s := range ss {
		porID[s.Storage] = s
	}
	lz, ok := porID["local-zfs"]
	if !ok {
		t.Fatalf("local-zfs missing: %+v", porID)
	}
	if lz.Type != "zfspool" {
		t.Errorf("local-zfs.Type = %q, want zfspool", lz.Type)
	}
	if lz.Total != 978416107520 || lz.Used != 67198091264 {
		t.Errorf("local-zfs total/used = %d/%d, want 978416107520/67198091264", lz.Total, lz.Used)
	}
	if lz.UsedFraction < 0.068 || lz.UsedFraction > 0.069 {
		t.Errorf("local-zfs.UsedFraction = %v, want ~0.0687 (the PVE sends the fraction READY-MADE)", lz.UsedFraction)
	}
	// content is "images,rootdir": a string, not an array. Whoever wants a list
	// uses Conteudos(), and that is the one the screen consumes.
	if got := lz.Conteudos(); strings.Join(got, ",") != "images,rootdir" {
		t.Errorf("local-zfs.Conteudos() = %v, want [images rootdir] in a stable order", got)
	}
	if !lz.Ativo() || !lz.Habilitado() {
		t.Errorf("local-zfs ativo=%v habilitado=%v — the PVE sends 1, not true", lz.Ativo(), lz.Habilitado())
	}
	pbs, ok := porID["pbs"]
	if !ok {
		t.Fatal("pbs missing")
	}
	if !pbs.Compartilhado() {
		t.Error("pbs.Compartilhado() = false — the fixture carries shared=1")
	}
	if porID["local"].Compartilhado() {
		t.Error("local.Compartilhado() = true — the fixture carries shared=0")
	}
}

// TestStorageListOrdemEEstavel: the hypervisor promises no ordering, and a
// document that changes order on every tick becomes a noise diff in the
// persisted inventory.
func TestStorageListOrdemEEstavel(t *testing.T) {
	const corpo = `{"data":[
	  {"storage":"pbs","type":"pbs"},
	  {"storage":"local","type":"dir"},
	  {"storage":"backupusb","type":"dir"},
	  {"storage":"local-zfs","type":"zfspool"}
	]}`
	c, _ := capturaURL(t, corpo)
	ss, err := c.StorageList(context.Background(), "pve")
	if err != nil {
		t.Fatalf("StorageList: %v", err)
	}
	var ids []string
	for _, s := range ss {
		ids = append(ids, s.Storage)
	}
	quer := "backupusb,local,local-zfs,pbs"
	if strings.Join(ids, ",") != quer {
		t.Errorf("order = %v, want %s (sorted by id)", ids, quer)
	}
}

// ---------------------------------------------------------------- ZFSList ---

// TestZFSListLeAFormaReal uses the LITERAL response of /nodes/pve/disks/zfs —
// the route that returned 403 before the ACL and now brings back both pools.
func TestZFSListLeAFormaReal(t *testing.T) {
	c, vista := capturaURL(t, corpoDaFixture(t, fixtureZFS))

	ps, err := c.ZFSList(context.Background(), "pve")
	if err != nil {
		t.Fatalf("ZFSList: %v", err)
	}
	if *vista != "/api2/json/nodes/pve/disks/zfs" {
		t.Errorf("URL = %q, want /api2/json/nodes/pve/disks/zfs", *vista)
	}
	if len(ps) != 2 {
		t.Fatalf("len = %d, want 2 pools (backup, rpool)", len(ps))
	}
	if ps[0].Name != "backup" || ps[1].Name != "rpool" {
		t.Errorf("order = %q,%q — want backup,rpool (sorted by name)", ps[0].Name, ps[1].Name)
	}
	if ps[1].Health != "ONLINE" {
		t.Errorf("rpool.Health = %q, want ONLINE", ps[1].Health)
	}
	if ps[1].Frag != 17 {
		t.Errorf("rpool.Frag = %d, want 17 (measured)", ps[1].Frag)
	}
	if ps[0].Frag != 0 {
		t.Errorf("backup.Frag = %d, want 0 — and ZERO is a measurement, not an absence", ps[0].Frag)
	}
	if ps[1].Alloc != 70999646208 || ps[1].Free != 942612635648 {
		t.Errorf("rpool alloc/free = %d/%d", ps[1].Alloc, ps[1].Free)
	}
	if ps[1].Size != 1013612281856 {
		t.Errorf("rpool.Size = %d", ps[1].Size)
	}
}

// 🔴 TestZFSListPoolDegradadoNaoVemComoOnline: the reason this block exists. A
// DEGRADED pool on a SINGLE-DISK server is the most expensive news in the lab,
// and the parser cannot normalise that away into nothing.
func TestZFSListPoolDegradadoNaoVemComoOnline(t *testing.T) {
	const corpo = `{"data":[{"name":"rpool","health":"DEGRADED","size":1,"alloc":1,"free":0,"frag":0,"dedup":1}]}`
	c, _ := capturaURL(t, corpo)
	ps, err := c.ZFSList(context.Background(), "pve")
	if err != nil {
		t.Fatalf("ZFSList: %v", err)
	}
	if len(ps) != 1 || ps[0].Health != "DEGRADED" {
		t.Fatalf("pools = %+v, want health DEGRADED preserved literally", ps)
	}
	if ps[0].Saudavel() {
		t.Error("Saudavel() = true for DEGRADED — only ONLINE counts as healthy")
	}
}

// -------------------------------------------------- empty is not an error ---

// 🔴 TestVazioNaoEErroNasRotasNovas: the hypervisor envelope has THREE shapes of
// "nothing" and only ONE of them is a protocol defect.
//
//	{"data":[]}    → empty list. A legitimate answer ("there is no storage here").
//	{"data":null}  → the same: the RawMessage receives the 4 bytes `null` and
//	                 deserialises into a nil slice. Measured, not assumed.
//	{}             → NO envelope. That is the hypervisor speaking another
//	                 language, and there a HARD error is the right reading.
//
// Without this pin, one of the first two would become "hypervisor error" on a
// screen that should be saying "nothing here" — and the operator would go
// hunting for a defect in the panel.
func TestVazioNaoEErroNasRotasNovas(t *testing.T) {
	casos := []struct {
		nome    string
		corpo   string
		querErr bool
	}{
		{"lista vazia", `{"data":[]}`, false},
		{"data null", `{"data":null}`, false},
		{"sem envelope", `{"nao-e-data":[]}`, true},
	}
	for _, cs := range casos {
		t.Run(cs.nome+"/storage", func(t *testing.T) {
			c, _ := capturaURL(t, cs.corpo)
			ss, err := c.StorageList(context.Background(), "pve")
			if (err != nil) != cs.querErr {
				t.Fatalf("error = %v, wantErr = %v", err, cs.querErr)
			}
			if err == nil && len(ss) != 0 {
				t.Errorf("len = %d, want 0", len(ss))
			}
		})
		t.Run(cs.nome+"/zfs", func(t *testing.T) {
			c, _ := capturaURL(t, cs.corpo)
			ps, err := c.ZFSList(context.Background(), "pve")
			if (err != nil) != cs.querErr {
				t.Fatalf("error = %v, wantErr = %v", err, cs.querErr)
			}
			if err == nil && len(ps) != 0 {
				t.Errorf("len = %d, want 0", len(ps))
			}
		})
	}
}

// TestRotasNovasExigemNo: an empty node builds /nodes//storage, which the
// hypervisor answers with something that is not what was asked for. Refusing
// here is cheaper.
func TestRotasNovasExigemNo(t *testing.T) {
	c, _ := capturaURL(t, `{"data":[]}`)
	if _, err := c.StorageList(context.Background(), ""); err == nil {
		t.Error("StorageList with an empty node should fail")
	}
	if _, err := c.ZFSList(context.Background(), ""); err == nil {
		t.Error("ZFSList with an empty node should fail")
	}
}

// TestZFSListForbiddenContinuaSendoForbidden: before the ACL this route returned
// 403, and that state has to keep ARRIVING as "no permission" — never as an
// empty list. It is half of the distinction the permission guard defends.
func TestZFSListForbiddenContinuaSendoForbidden(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"data":null,"errors":{"path":"Permission check failed"}}`))
	})
	_, err := c.ZFSList(context.Background(), "pve")
	if err == nil {
		t.Fatal("a 403 turned into success")
	}
	var pe *Error
	if !errors.As(err, &pe) || pe.Kind != KindForbidden {
		t.Fatalf("error = %v (%T), want Kind=KindForbidden", err, err)
	}
}

// ---------------------------------------------- the privilege verdict ------

// 🔴 TestPodeAuditarDatastoreExigeOPrivilegio is the guard from that earlier
// pass, hardened.
//
// That pass answered "storage visible" with `strings.HasPrefix(caminho,
// "/storage")` — presence of a PATH. Presence of a path only says the token has
// SOME privilege there. A token with PVEVMUser propagated from the root puts
// /storage in the map without Datastore.Audit, the list comes back 200 with [],
// and the screen announces "it already sees /storage" over an empty block: the
// false-green is back, now with the guard's blessing.
//
// The correct verdict is the PRIVILEGE, on any path that covers the storage.
func TestPodeAuditarDatastoreExigeOPrivilegio(t *testing.T) {
	casos := []struct {
		nome  string
		perms map[string]map[string]int
		quer  bool
	}{
		{"mapa vazio", map[string]map[string]int{}, false},
		{"nil", nil, false},
		{
			"onda 1: só /vms e /nodes, sem storage",
			map[string]map[string]int{
				"/vms/204": {"VM.Audit": 1},
				"/nodes":   {"Sys.Audit": 1, "VM.Audit": 1},
			},
			false,
		},
		{
			"🔴 caminho presente SEM Datastore.Audit — presença não basta",
			map[string]map[string]int{"/storage": {"VM.Audit": 1, "Sys.Audit": 1}},
			false,
		},
		{
			"privilégio zerado explicitamente",
			map[string]map[string]int{"/storage": {"Datastore.Audit": 0}},
			false,
		},
		{
			"Datastore.Audit em /storage",
			map[string]map[string]int{"/storage": {"Datastore.Audit": 1}},
			true,
		},
		{
			"Datastore.Audit num storage específico",
			map[string]map[string]int{"/storage/local-zfs": {"Datastore.Audit": 1}},
			true,
		},
		{
			"Datastore.Audit na RAIZ (propagação) — é o caso vivo desta onda",
			map[string]map[string]int{"/": {"Datastore.Audit": 1}},
			true,
		},
		{
			"Datastore.Allocate implica poder auditar",
			map[string]map[string]int{"/storage": {"Datastore.Allocate": 1}},
			true,
		},
		{
			"🔴 caminho VIZINHO não conta (/storagefoo não é /storage/…)",
			map[string]map[string]int{"/storagefoo": {"Datastore.Audit": 1}},
			false,
		},
	}
	for _, cs := range casos {
		t.Run(cs.nome, func(t *testing.T) {
			if got := PodeAuditarDatastore(cs.perms); got != cs.quer {
				t.Errorf("PodeAuditarDatastore = %v, want %v", got, cs.quer)
			}
		})
	}
}

// TestPodeAuditarDatastoreNoMapaVIVO: the same verdict, over the LITERAL
// response of /access/permissions after the ACL. It is the pin that ties the
// guard to the real hypervisor — without it the test above proves only my
// arithmetic.
func TestPodeAuditarDatastoreNoMapaVIVO(t *testing.T) {
	var env struct {
		Data map[string]map[string]int `json:"data"`
	}
	if err := json.Unmarshal([]byte(corpoDaFixture(t, fixturePerms)), &env); err != nil {
		t.Fatalf("parsing the fixture: %v", err)
	}
	if len(env.Data) == 0 {
		t.Fatal("empty permissions fixture")
	}
	if !PodeAuditarDatastore(env.Data) {
		t.Errorf("the LIVE map (%d paths) does not authorize datastore — the 2026-08-20 ACL should have changed that",
			len(env.Data))
	}
	// And the negative half, on the same map: removing the privilege from EVERY
	// path that covers storage has to fail again. Without this half, the test would
	// pass with a function that returns a fixed `true`.
	for caminho, privs := range env.Data {
		if caminho == "/" || caminho == "/storage" || strings.HasPrefix(caminho, "/storage/") {
			delete(privs, "Datastore.Audit")
			delete(privs, "Datastore.Allocate")
			delete(privs, "Datastore.AllocateSpace")
			delete(privs, "Datastore.AllocateTemplate")
		}
	}
	if PodeAuditarDatastore(env.Data) {
		t.Error("with no Datastore.* on any storage path, the verdict stayed true")
	}
}
