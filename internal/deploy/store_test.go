package deploy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// migrado builds a dataDir already in the v2 envelope (the normal state after boot).
func migrado(t *testing.T, apps ...App) string {
	t.Helper()
	dataDir := setupLegacyAppsDir(t, apps...)
	if err := MigrateApps(AppsMigration{DataDir: dataDir}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return dataDir
}

// TestStoreReadsV2: the compatibility layer — with the v2 envelope on disk, the
// methods handlers_deploy.go and the hook use return exactly what they returned
// before the migration.
func TestStoreReadsV2(t *testing.T) {
	orig := App{
		Name: "hello", Branch: "main", ComposeFile: "docker-compose.yml",
		Port: 8091, Domain: "hello.x", Autodeploy: true,
		Created: 1784466254, Updated: 1784467047,
		Env:     map[string]string{"FOO": "bar"},
		Deploys: []DeployRecord{{ID: "d1", Status: "running", Commit: "abc"}},
	}
	dataDir := migrado(t, orig, App{Name: "api", Branch: "prod"})
	st := Open(dataDir)

	apps, err := st.List()
	if err != nil {
		t.Fatalf("List over v2: %v", err)
	}
	if len(apps) != 2 {
		t.Fatalf("List returned %d apps, want 2", len(apps))
	}
	if apps[0].Name != "api" || apps[1].Name != "hello" {
		t.Fatalf("ordering by name was lost: %q, %q", apps[0].Name, apps[1].Name)
	}

	got, ok, err := st.Get("hello")
	if err != nil || !ok {
		t.Fatalf("Get(hello) = ok:%v err:%v", ok, err)
	}
	if got.Port != orig.Port || got.Branch != orig.Branch || got.Domain != orig.Domain ||
		got.ComposeFile != orig.ComposeFile || got.Autodeploy != orig.Autodeploy ||
		got.Created != orig.Created || got.Env["FOO"] != "bar" ||
		len(got.Deploys) != 1 || got.Deploys[0].ID != "d1" {
		t.Fatalf("app data changed on the way through v2: %+v", got)
	}
}

// TestStoreRefusesV1AfterMigration: no write from the store may put the v1
// array back. It is the most likely data-loss mode in this work — one forgotten
// write path silently undoes the migration on the first deploy.
func TestStoreRefusesV1AfterMigration(t *testing.T) {
	dataDir := migrado(t, App{Name: "hello", Branch: "main"})
	st := Open(dataDir)

	if err := st.Save(App{Name: "hello", Branch: "main", Port: 9090}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := st.UpdateEnv("hello", false, map[string]string{"K": "V"}, nil); err != nil {
		t.Fatalf("UpdateEnv: %v", err)
	}
	if err := st.AppendDeploy("hello", DeployRecord{ID: "d2", Status: "building"}); err != nil {
		t.Fatalf("AppendDeploy: %v", err)
	}
	if err := st.Remove("nao-existe"); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	sh, ver, err := DetectShape(dataDir)
	if err != nil {
		t.Fatalf("DetectShape: %v", err)
	}
	if sh != ShapeV2 || ver != AppsSchemaVersion {
		t.Fatalf("after the writes apps.json became %q (schema_version=%d)", sh, ver)
	}

	// And the data survived the round-trip.
	got, ok, err := st.Get("hello")
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if got.Port != 9090 || got.Env["K"] != "V" || len(got.Deploys) != 1 || got.Deploys[0].ID != "d2" {
		t.Fatalf("the v2 round-trip lost data: %+v", got)
	}
}

// TestStorePreservaNodeIDAlheio: the compat layer speaks App, which has NO
// node_id. A naive persist would stamp every deployment with the local node and
// silently erase the one piece of information the multi-node model exists to keep.
func TestStorePreservaNodeIDAlheio(t *testing.T) {
	dataDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dataDir, "deploy"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	envelope := `{"schema_version":2,
	 "projects":[{"id":"hello","name":"Hello Mundo"},{"id":"api","name":"api"}],
	 "deployments":[
	   {"id":"hello@casa-apps","project_id":"hello","node_id":"casa-apps","branch":"main"},
	   {"id":"api@vps-187","project_id":"api","node_id":"vps-187","branch":"prod"}]}`
	if err := os.WriteFile(appsPath(dataDir), []byte(envelope), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	st := Open(dataDir)
	if err := st.Save(App{Name: "api", Branch: "prod", Port: 1234}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	f := readEnvelope(t, dataDir)
	var achou bool
	for _, d := range f.Deployments {
		if d.ProjectID == "hello" {
			achou = true
			if d.NodeID != "casa-apps" {
				t.Fatalf("the node_id of another node was rewritten to %q", d.NodeID)
			}
			if d.ID != "hello@casa-apps" {
				t.Fatalf("another node's deployment id became %q", d.ID)
			}
		}
	}
	if !achou {
		t.Fatalf("another node's deployment VANISHED from the envelope: %+v", f.Deployments)
	}
	for _, p := range f.Projects {
		if p.ID == "hello" && p.Name != "Hello Mundo" {
			t.Fatalf("the project name was flattened to %q", p.Name)
		}
	}
}

// TestStoreRecusaV1SemMigrar: the store does NOT migrate (the server's boot is
// what migrates). Meeting v1 here is defence in depth — an error that NAMES
// what to do, never an empty list (an empty list makes the UI say "no apps" and
// the next Save wipe the whole registry).
func TestStoreRecusaV1SemMigrar(t *testing.T) {
	dataDir := setupLegacyAppsDir(t, App{Name: "hello", Branch: "main"})
	antes := sha256Of(t, appsPath(dataDir))

	st := Open(dataDir)
	apps, err := st.List()
	if err == nil {
		t.Fatalf("List ACCEPTED a v1 array and returned %d app(s)", len(apps))
	}
	if !strings.Contains(err.Error(), "vps-manager") || !strings.Contains(err.Error(), "schema_version") {
		t.Fatalf("the error says neither which binary migrates nor mentions schema_version: %v", err)
	}
	if depois := sha256Of(t, appsPath(dataDir)); depois != antes {
		t.Fatalf("the store's refusal TOUCHED the file")
	}
}

// TestStoreAusenteEVazio: a fresh install goes on working (v1 treated a missing
// file as an empty list, and that must not regress).
func TestStoreAusenteEVazio(t *testing.T) {
	st := Open(t.TempDir())
	apps, err := st.List()
	if err != nil {
		t.Fatalf("apps.json missing: %v", err)
	}
	if len(apps) != 0 {
		t.Fatalf("a missing apps.json returned %d app(s)", len(apps))
	}
}

// TestStoreWritePathIsDurable is a STRUCTURAL pin over the source: the store's
// write path has to go through writeFileAtomic (fsync of the file AND of the
// directory). A behavioural test does not tell rename-with-fsync from
// rename-without-fsync — the difference only shows up in a power cut, and this
// house has a UPS with no data cable. Cast from inventory.TestStoreWritePathIsDurable.
func TestStoreWritePathIsDurable(t *testing.T) {
	src, err := os.ReadFile("store.go")
	if err != nil {
		t.Fatalf("reading store.go: %v", err)
	}
	if strings.Contains(string(src), "os.WriteFile(") {
		t.Fatalf("store.go went back to writing with os.WriteFile — with no fsync, the rename can land before the content")
	}
	if !strings.Contains(string(src), "writeFileAtomic(") {
		t.Fatalf("persistLocked does not use writeFileAtomic")
	}
	mig, err := os.ReadFile("migrate.go")
	if err != nil {
		t.Fatalf("reading migrate.go: %v", err)
	}
	if n := strings.Count(string(mig), ".Sync()"); n < 2 {
		t.Fatalf("writeFileAtomic has %d Sync() call(s); durability is DOUBLE (file and directory)", n)
	}
}

// TestStorePersistIsAtomicJSON: what lands on disk after a write is still JSON
// decodable into the current envelope (a guard against accidental
// concatenation or append).
func TestStorePersistIsAtomicJSON(t *testing.T) {
	dataDir := migrado(t, App{Name: "hello"})
	st := Open(dataDir)
	for i := 0; i < 5; i++ {
		if err := st.Save(App{Name: "hello", Port: 8000 + i}); err != nil {
			t.Fatalf("Save %d: %v", i, err)
		}
	}
	raw, err := os.ReadFile(appsPath(dataDir))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var f File
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("apps.json stopped being valid JSON: %v", err)
	}
	if f.SchemaVersion != AppsSchemaVersion || len(f.Deployments) != 1 {
		t.Fatalf("the envelope degraded: %+v", f)
	}
}
