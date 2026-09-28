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

const (
	fixtureStorage = "testdata/node-storage.json"
	fixtureZFS     = "testdata/node-disks-zfs.json"
	fixturePerms   = "testdata/access-permissions-auditor.json"
)

func fixtureBody(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(raw)
}

func TestStorageListReadsRealShape(t *testing.T) {
	c, seenURL := captureURL(t, fixtureBody(t, fixtureStorage))

	ss, err := c.StorageList(context.Background(), "pve")
	if err != nil {
		t.Fatalf("StorageList: %v", err)
	}
	if *seenURL != "/api2/json/nodes/pve/storage" {
		t.Errorf("URL = %q, want /api2/json/nodes/pve/storage", *seenURL)
	}
	if len(ss) != 4 {
		t.Fatalf("len = %d, want 4 storages (backupusb, local, local-zfs, pbs)", len(ss))
	}
	byID := map[string]Storage{}
	for _, s := range ss {
		byID[s.Storage] = s
	}
	lz, ok := byID["local-zfs"]
	if !ok {
		t.Fatalf("local-zfs missing: %+v", byID)
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
	if got := lz.ContentList(); strings.Join(got, ",") != "images,rootdir" {
		t.Errorf("local-zfs.ContentList() = %v, want [images rootdir] in a stable order", got)
	}
	if !lz.IsActive() || !lz.IsEnabled() {
		t.Errorf("local-zfs active=%v enabled=%v: the PVE sends 1, not true", lz.IsActive(), lz.IsEnabled())
	}
	pbs, ok := byID["pbs"]
	if !ok {
		t.Fatal("pbs missing")
	}
	if !pbs.IsShared() {
		t.Error("pbs.IsShared() = false: the fixture carries shared=1")
	}
	if byID["local"].IsShared() {
		t.Error("local.IsShared() = true: the fixture carries shared=0")
	}
}

func TestStorageListOrderIsStable(t *testing.T) {
	const body = `{"data":[
	  {"storage":"pbs","type":"pbs"},
	  {"storage":"local","type":"dir"},
	  {"storage":"backupusb","type":"dir"},
	  {"storage":"local-zfs","type":"zfspool"}
	]}`
	c, _ := captureURL(t, body)
	ss, err := c.StorageList(context.Background(), "pve")
	if err != nil {
		t.Fatalf("StorageList: %v", err)
	}
	var ids []string
	for _, s := range ss {
		ids = append(ids, s.Storage)
	}
	want := "backupusb,local,local-zfs,pbs"
	if strings.Join(ids, ",") != want {
		t.Errorf("order = %v, want %s (sorted by id)", ids, want)
	}
}

func TestZFSListReadsRealShape(t *testing.T) {
	c, seenURL := captureURL(t, fixtureBody(t, fixtureZFS))

	ps, err := c.ZFSList(context.Background(), "pve")
	if err != nil {
		t.Fatalf("ZFSList: %v", err)
	}
	if *seenURL != "/api2/json/nodes/pve/disks/zfs" {
		t.Errorf("URL = %q, want /api2/json/nodes/pve/disks/zfs", *seenURL)
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

func TestZFSListDegradedPoolIsNotOnline(t *testing.T) {
	const body = `{"data":[{"name":"rpool","health":"DEGRADED","size":1,"alloc":1,"free":0,"frag":0,"dedup":1}]}`
	c, _ := captureURL(t, body)
	ps, err := c.ZFSList(context.Background(), "pve")
	if err != nil {
		t.Fatalf("ZFSList: %v", err)
	}
	if len(ps) != 1 || ps[0].Health != "DEGRADED" {
		t.Fatalf("pools = %+v, want health DEGRADED preserved literally", ps)
	}
	if ps[0].Healthy() {
		t.Error("Healthy() = true for DEGRADED — only ONLINE counts as healthy")
	}
}

func TestEmptyIsNotErrorOnNewRoutes(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{"empty list", `{"data":[]}`, false},
		{"data null", `{"data":null}`, false},
		{"no envelope", `{"not-data":[]}`, true},
	}
	for _, cs := range cases {
		t.Run(cs.name+"/storage", func(t *testing.T) {
			c, _ := captureURL(t, cs.body)
			ss, err := c.StorageList(context.Background(), "pve")
			if (err != nil) != cs.wantErr {
				t.Fatalf("error = %v, wantErr = %v", err, cs.wantErr)
			}
			if err == nil && len(ss) != 0 {
				t.Errorf("len = %d, want 0", len(ss))
			}
		})
		t.Run(cs.name+"/zfs", func(t *testing.T) {
			c, _ := captureURL(t, cs.body)
			ps, err := c.ZFSList(context.Background(), "pve")
			if (err != nil) != cs.wantErr {
				t.Fatalf("error = %v, wantErr = %v", err, cs.wantErr)
			}
			if err == nil && len(ps) != 0 {
				t.Errorf("len = %d, want 0", len(ps))
			}
		})
	}
}

func TestNewRoutesRequireNode(t *testing.T) {
	c, _ := captureURL(t, `{"data":[]}`)
	if _, err := c.StorageList(context.Background(), ""); err == nil {
		t.Error("StorageList with an empty node should fail")
	}
	if _, err := c.ZFSList(context.Background(), ""); err == nil {
		t.Error("ZFSList with an empty node should fail")
	}
}

func TestZFSListForbiddenStaysForbidden(t *testing.T) {
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

func TestCanAuditDatastoreRequiresPrivilege(t *testing.T) {
	cases := []struct {
		name  string
		perms map[string]map[string]int
		want  bool
	}{
		{"empty map", map[string]map[string]int{}, false},
		{"nil", nil, false},
		{
			"only /vms and /nodes, no storage",
			map[string]map[string]int{
				"/vms/204": {"VM.Audit": 1},
				"/nodes":   {"Sys.Audit": 1, "VM.Audit": 1},
			},
			false,
		},
		{
			"path present WITHOUT Datastore.Audit: presence is not enough",
			map[string]map[string]int{"/storage": {"VM.Audit": 1, "Sys.Audit": 1}},
			false,
		},
		{
			"privilege explicitly zeroed",
			map[string]map[string]int{"/storage": {"Datastore.Audit": 0}},
			false,
		},
		{
			"Datastore.Audit em /storage",
			map[string]map[string]int{"/storage": {"Datastore.Audit": 1}},
			true,
		},
		{
			"Datastore.Audit on a specific storage",
			map[string]map[string]int{"/storage/local-zfs": {"Datastore.Audit": 1}},
			true,
		},
		{
			"Datastore.Audit at the ROOT (propagation), the live case",
			map[string]map[string]int{"/": {"Datastore.Audit": 1}},
			true,
		},
		{
			"Datastore.Allocate implies audit",
			map[string]map[string]int{"/storage": {"Datastore.Allocate": 1}},
			true,
		},
		{
			"a NEIGHBOUR path does not count (/storagefoo is not /storage/...)",
			map[string]map[string]int{"/storagefoo": {"Datastore.Audit": 1}},
			false,
		},
	}
	for _, cs := range cases {
		t.Run(cs.name, func(t *testing.T) {
			if got := CanAuditDatastore(cs.perms); got != cs.want {
				t.Errorf("CanAuditDatastore = %v, want %v", got, cs.want)
			}
		})
	}
}

func TestCanAuditDatastoreOnLiveMap(t *testing.T) {
	var env struct {
		Data map[string]map[string]int `json:"data"`
	}
	if err := json.Unmarshal([]byte(fixtureBody(t, fixturePerms)), &env); err != nil {
		t.Fatalf("parsing the fixture: %v", err)
	}
	if len(env.Data) == 0 {
		t.Fatal("empty permissions fixture")
	}
	if !CanAuditDatastore(env.Data) {
		t.Errorf("the LIVE map (%d paths) does not authorize datastore — the 2026-08-20 ACL should have changed that",
			len(env.Data))
	}
	for path, privs := range env.Data {
		if path == "/" || path == "/storage" || strings.HasPrefix(path, "/storage/") {
			delete(privs, "Datastore.Audit")
			delete(privs, "Datastore.Allocate")
			delete(privs, "Datastore.AllocateSpace")
			delete(privs, "Datastore.AllocateTemplate")
		}
	}
	if CanAuditDatastore(env.Data) {
		t.Error("with no Datastore.* on any storage path, the verdict stayed true")
	}
}
