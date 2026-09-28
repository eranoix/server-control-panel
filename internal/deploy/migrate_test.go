package deploy

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"server-control-panel/internal/auth"
)

func setupLegacyAppsDir(t *testing.T, apps ...App) string {
	t.Helper()
	dataDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dataDir, "deploy"), 0o700); err != nil {
		t.Fatalf("mkdir deploy: %v", err)
	}
	raw, err := json.MarshalIndent(apps, "", " ")
	if err != nil {
		t.Fatalf("marshal v1: %v", err)
	}
	if err := os.WriteFile(appsPath(dataDir), raw, 0o600); err != nil {
		t.Fatalf("write apps.json: %v", err)
	}
	return dataDir
}

func writeRawApps(t *testing.T, body string) string {
	t.Helper()
	dataDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dataDir, "deploy"), 0o700); err != nil {
		t.Fatalf("mkdir deploy: %v", err)
	}
	if err := os.WriteFile(appsPath(dataDir), []byte(body), 0o600); err != nil {
		t.Fatalf("write apps.json: %v", err)
	}
	return dataDir
}

func readEnvelope(t *testing.T, dataDir string) File {
	t.Helper()
	raw, err := os.ReadFile(appsPath(dataDir))
	if err != nil {
		t.Fatalf("read apps.json: %v", err)
	}
	var f File
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("apps.json is not a v2 envelope: %v — content: %s", err, raw)
	}
	return f
}

func bakCount(t *testing.T, dataDir string) int {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dataDir, "deploy"))
	if err != nil {
		t.Fatalf("readdir deploy: %v", err)
	}
	n := 0
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "apps.json.bak.") {
			n++
		}
	}
	return n
}

func sha256Of(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func TestMigrateAppsHappyPath(t *testing.T) {
	dataDir := setupLegacyAppsDir(t,
		App{
			Name: "hello", Branch: "main", ComposeFile: "docker-compose.yml",
			Port: 8091, Autodeploy: true, Created: 1784466254, Updated: 1784467047,
			Env: map[string]string{"FOO": "bar"},
			Deploys: []DeployRecord{{
				ID: "d20260719-131515-03d885", Ref: "refs/heads/main",
				Commit: "c34ea95f", Project: "panel-hello", Status: "running",
			}},
		},
		App{Name: "api", Branch: "prod", ComposeFile: "compose.yml", Domain: "api.x"},
	)

	if err := MigrateApps(AppsMigration{DataDir: dataDir}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	f := readEnvelope(t, dataDir)
	if f.SchemaVersion != AppsSchemaVersion {
		t.Fatalf("schema_version = %d, want %d", f.SchemaVersion, AppsSchemaVersion)
	}
	if len(f.Projects) != 2 || len(f.Deployments) != 2 {
		t.Fatalf("projects=%d deployments=%d, want 2/2", len(f.Projects), len(f.Deployments))
	}
	for _, d := range f.Deployments {
		if d.NodeID != DefaultNodeID {
			t.Fatalf("deployment %q with node_id %q, want %q", d.ID, d.NodeID, DefaultNodeID)
		}
		if d.ProjectID == "" || d.ID != d.ProjectID+"@"+DefaultNodeID {
			t.Fatalf("deployment id %q does not derive from project_id %q + node", d.ID, d.ProjectID)
		}
	}

	var hello Deployment
	for _, d := range f.Deployments {
		if d.ProjectID == "hello" {
			hello = d
		}
	}
	if hello.Port != 8091 || hello.Branch != "main" || !hello.Autodeploy ||
		hello.Env["FOO"] != "bar" || hello.Created != 1784466254 ||
		len(hello.Deploys) != 1 || hello.Deploys[0].ID != "d20260719-131515-03d885" {
		t.Fatalf("v1 fields lost in the conversion: %+v", hello)
	}

	if n := bakCount(t, dataDir); n != 1 {
		t.Fatalf("backups .bak.<ts> = %d, want exactly 1", n)
	}
}

func TestMigrateAppsIdempotent(t *testing.T) {
	dataDir := setupLegacyAppsDir(t, App{Name: "hello", Branch: "main"})

	if err := MigrateApps(AppsMigration{DataDir: dataDir}); err != nil {
		t.Fatalf("first migration: %v", err)
	}
	bak1 := bakCount(t, dataDir)
	sum1 := sha256Of(t, appsPath(dataDir))

	if err := MigrateApps(AppsMigration{DataDir: dataDir}); err != nil {
		t.Fatalf("second migration: %v", err)
	}
	bak2 := bakCount(t, dataDir)
	if bak2 != bak1 {
		t.Fatalf("the second migration created another backup (had %d, now %d)", bak1, bak2)
	}
	if sum2 := sha256Of(t, appsPath(dataDir)); sum2 != sum1 {
		t.Fatalf("the second migration REWROTE the file (sha %s → %s)", sum1, sum2)
	}
}

func TestMigrateAppsDetectByShape(t *testing.T) {
	t.Run("already-v2-is-a-no-op", func(t *testing.T) {
		dataDir := writeRawApps(t, `{"schema_version":2,"projects":[{"id":"a","name":"a"}],"deployments":[]}`)
		before := sha256Of(t, appsPath(dataDir))
		if err := MigrateApps(AppsMigration{DataDir: dataDir}); err != nil {
			t.Fatalf("migrate over v2: %v", err)
		}
		if n := bakCount(t, dataDir); n != 0 {
			t.Fatalf("the no-op created %d backup(s)", n)
		}
		if after := sha256Of(t, appsPath(dataDir)); after != before {
			t.Fatalf("the no-op rewrote the file")
		}
	})

	t.Run("unknown-shape-is-an-error-without-touching", func(t *testing.T) {
		for _, body := range []string{
			`{"schema_version":3,"projects":[]}`,
			`{"apps":[]}`,
			`"a stray string"`,
			`{`,
		} {
			dataDir := writeRawApps(t, body)
			before := sha256Of(t, appsPath(dataDir))
			err := MigrateApps(AppsMigration{DataDir: dataDir})
			if err == nil {
				t.Fatalf("shape %q was ACCEPTED by the migration", body)
			}
			if !strings.Contains(err.Error(), "apps.json") ||
				!strings.Contains(err.Error(), "unknown format") ||
				!strings.Contains(err.Error(), "schema_version") {
				t.Fatalf("the error does not classify the refused shape (shape %q): %v", body, err)
			}
			if after := sha256Of(t, appsPath(dataDir)); after != before {
				t.Fatalf("the refusal TOUCHED the file (shape %q)", body)
			}
			if n := bakCount(t, dataDir); n != 0 {
				t.Fatalf("the refusal created a backup (shape %q)", body)
			}
		}
	})

	t.Run("missing-is-a-no-op", func(t *testing.T) {
		dataDir := t.TempDir()
		if err := MigrateApps(AppsMigration{DataDir: dataDir}); err != nil {
			t.Fatalf("a missing apps.json should have been a no-op, got: %v", err)
		}
		if _, err := os.Stat(appsPath(dataDir)); !os.IsNotExist(err) {
			t.Fatalf("the migration CREATED apps.json where there was nothing")
		}
	})
}

func TestMigrateAppsRollbackOnWriteFailure(t *testing.T) {
	dataDir := setupLegacyAppsDir(t, App{Name: "hello", Branch: "main", Port: 8091})
	before := sha256Of(t, appsPath(dataDir))

	if err := os.MkdirAll(appsPath(dataDir)+".new", 0o700); err != nil {
		t.Fatalf("arming the failure: %v", err)
	}

	err := MigrateApps(AppsMigration{DataDir: dataDir})
	if err == nil {
		t.Fatalf("an impossible write did NOT fail the migration")
	}
	if !strings.Contains(err.Error(), "rollback") {
		t.Fatalf("the error does not mention the rollback that ran: %v", err)
	}
	if after := sha256Of(t, appsPath(dataDir)); after != before {
		t.Fatalf("the rollback did NOT restore the v1 apps.json (sha %s → %s)", before, after)
	}
	sh, _, derr := DetectShape(dataDir)
	if derr != nil || sh != ShapeV1Array {
		t.Fatalf("after the rollback the shape is %q (err=%v), want %q", sh, derr, ShapeV1Array)
	}
}

func TestMigrateAppsConcurrentLock(t *testing.T) {
	dataDir := setupLegacyAppsDir(t, App{Name: "hello", Branch: "main"})
	before := sha256Of(t, appsPath(dataDir))

	child := exec.Command(os.Args[0], "-test.run=TestHelperHoldsLock", "-test.v")
	child.Env = append(os.Environ(), "DEPLOY_LOCK_DATADIR="+dataDir)
	stdin, err := child.StdinPipe()
	if err != nil {
		t.Fatalf("child stdin: %v", err)
	}
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatalf("child stdout: %v", err)
	}
	if err := child.Start(); err != nil {
		t.Fatalf("starting the child: %v", err)
	}
	defer func() {
		_ = stdin.Close()
		_ = child.Wait()
	}()

	sc := bufio.NewScanner(stdout)
	locked := false
	for sc.Scan() {
		if strings.Contains(sc.Text(), "LOCKED") {
			locked = true
			break
		}
	}
	if !locked {
		t.Fatalf("the child could not hold the lock")
	}

	err = MigrateApps(AppsMigration{DataDir: dataDir})
	if !errors.Is(err, ErrConcurrentAppsMigration) {
		t.Fatalf("a migration with the lock held by another process returned %v, want ErrConcurrentAppsMigration", err)
	}
	if after := sha256Of(t, appsPath(dataDir)); after != before {
		t.Fatalf("the losing migration TOUCHED the file")
	}
	if n := bakCount(t, dataDir); n != 0 {
		t.Fatalf("the losing migration created %d backup(s)", n)
	}

	_ = stdin.Close()
	if err := child.Wait(); err != nil {
		t.Fatalf("child: %v", err)
	}
	if err := MigrateApps(AppsMigration{DataDir: dataDir}); err != nil {
		t.Fatalf("migration after releasing the lock: %v", err)
	}
	if sh, _, _ := DetectShape(dataDir); sh != ShapeV2 {
		t.Fatalf("after the retry the shape is %q", sh)
	}
}

func TestHelperHoldsLock(t *testing.T) {
	dataDir := os.Getenv("DEPLOY_LOCK_DATADIR")
	if dataDir == "" {
		t.Skip("helper for TestMigrateAppsConcurrentLock")
	}
	f, err := os.OpenFile(appsLockPath(dataDir), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("opening the lock: %v", err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatalf("flock: %v", err)
	}
	fmt.Println("LOCKED")
	_, _ = io.Copy(io.Discard, os.Stdin)
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}

func TestMigrateAppsAuditAppended(t *testing.T) {
	dataDir := setupLegacyAppsDir(t, App{Name: "hello", Branch: "main"})
	auditLog, err := auth.NewAuditLog(filepath.Join(dataDir, "audit.log"))
	if err != nil {
		t.Fatalf("audit: %v", err)
	}

	if err := MigrateApps(AppsMigration{DataDir: dataDir, Audit: auditLog}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	found := false
	for _, e := range auditLog.Tail(10) {
		if e.Action == "migration.apps.v2" && e.User == "system" && e.Target == appsPath(dataDir) {
			found = true
		}
	}
	if !found {
		t.Fatalf("the migration.apps.v2 event (user=system) is not on the audit trail: %+v", auditLog.Tail(10))
	}

	dataDir2 := setupLegacyAppsDir(t, App{Name: "hello"})
	if err := MigrateApps(AppsMigration{DataDir: dataDir2, Audit: nil}); err != nil {
		t.Fatalf("migration without audit: %v", err)
	}
}

func TestGuardCLIRejectsShapes(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		accepts bool
	}{
		{"v2-current", `{"schema_version":2,"projects":[],"deployments":[]}`, true},
		{"v1-array", `[{"name":"hello"}]`, false},
		{"future-version", `{"schema_version":3,"projects":[]}`, false},
		{"garbage", `{`, false},
		{"empty", ``, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dataDir := writeRawApps(t, c.body)
			before := sha256Of(t, appsPath(dataDir))
			err := GuardCLI(dataDir)
			if c.accepts {
				if err != nil {
					t.Fatalf("an acceptable shape was refused: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("shape %q was ACCEPTED by panelctl", c.body)
			}
			if !strings.Contains(err.Error(), "panelctl") {
				t.Fatalf("the refusal does not NAME the binary: %v", err)
			}
			if !strings.Contains(err.Error(), "schema_version") {
				t.Fatalf("the refusal does not mention schema_version: %v", err)
			}
			if after := sha256Of(t, appsPath(dataDir)); after != before {
				t.Fatalf("the refusal TOUCHED the file")
			}
		})
	}

	t.Run("missing-is-accepted", func(t *testing.T) {
		if err := GuardCLI(t.TempDir()); err != nil {
			t.Fatalf("fresh install refused: %v", err)
		}
	})
}

func TestMigrateAppsDryRunWithRealFile(t *testing.T) {
	origin := os.Getenv("DEPLOY_REHEARSAL_APPS")
	if origin == "" {
		t.Skip("set DEPLOY_REHEARSAL_APPS=<path to the real apps.json> for the rehearsal")
	}
	raw, err := os.ReadFile(origin)
	if err != nil {
		t.Fatalf("reading %s: %v", origin, err)
	}
	var v1 []App
	if err := json.Unmarshal(raw, &v1); err != nil {
		t.Fatalf("%s is not a v1 apps.json: %v", origin, err)
	}

	dataDir := writeRawApps(t, string(raw))
	before := sha256Of(t, appsPath(dataDir))

	if err := MigrateApps(AppsMigration{DataDir: dataDir}); err != nil {
		t.Fatalf("migration of the real file: %v", err)
	}
	f := readEnvelope(t, dataDir)
	if f.SchemaVersion != AppsSchemaVersion || len(f.Projects) != len(v1) || len(f.Deployments) != len(v1) {
		t.Fatalf("envelope: schema=%d projects=%d deployments=%d, want %d/%d/%d",
			f.SchemaVersion, len(f.Projects), len(f.Deployments), AppsSchemaVersion, len(v1), len(v1))
	}
	names := map[string]bool{}
	for _, a := range v1 {
		names[a.Name] = true
	}
	for _, d := range f.Deployments {
		if !names[d.ProjectID] {
			t.Fatalf("deployment %q matches no app from the real file", d.ProjectID)
		}
		delete(names, d.ProjectID)
	}
	if len(names) != 0 {
		t.Fatalf("apps from the real file that vanished in the migration: %v", names)
	}

	entries, _ := os.ReadDir(filepath.Join(dataDir, "deploy"))
	bak := ""
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "apps.json.bak.") {
			bak = filepath.Join(dataDir, "deploy", e.Name())
		}
	}
	if bak == "" {
		t.Fatalf("no backup to restore")
	}
	if err := os.Rename(bak, appsPath(dataDir)); err != nil {
		t.Fatalf("restoring the backup: %v", err)
	}
	if after := sha256Of(t, appsPath(dataDir)); after != before {
		t.Fatalf("the rollback of the REAL file did not give the original back (sha %s → %s)", before, after)
	}
	t.Logf("rehearsal ok: %d app(s) migrated and restored from the backup", len(v1))
}
