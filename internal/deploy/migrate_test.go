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

// setupLegacyAppsDir builds a <dataDir>/deploy/apps.json in the REAL v1 shape
// measured in production (a RAW array, no envelope) and returns the dataDir.
// The analogue of setupLegacyDataDir in internal/config/migrate_test.go.
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

// writeRawApps writes an arbitrary apps.json (used for the shapes the
// migration has to REFUSE).
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

// bakCount counts the .bak.<ts> backups next to apps.json. Cast from
// TestMigrateV1ToV2_Idempotent, which counts <dataDir>.bak.* in the parent directory.
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

// TestMigrateAppsHappyPath: a v1 array with 2 apps → a v2 envelope with 2
// projects and 2 deployments stamped with the local node, a backup taken before
// the write, and not one v1 field lost along the way.
func TestMigrateAppsHappyPath(t *testing.T) {
	dataDir := setupLegacyAppsDir(t,
		App{
			Name: "hello", Branch: "main", ComposeFile: "docker-compose.yml",
			Port: 8091, Autodeploy: true, Created: 1784466254, Updated: 1784467047,
			Env: map[string]string{"FOO": "bar"},
			Deploys: []DeployRecord{{
				ID: "d20260719-131515-03d885", Ref: "refs/heads/main",
				Commit: "c34ea95f", Project: "vpsm-hello", Status: "running",
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

	// No v1 field may have evaporated: the migration is of the FORMAT, not of the data.
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

// TestMigrateAppsIdempotent: running it twice creates no new backup and does
// not rewrite. Cast exactly from TestMigrateV1ToV2_Idempotent.
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

// TestMigrateAppsDetectByShape: detection goes BY SHAPE (v1 apps.json has
// nowhere to keep a version — one of the traps the research named). BOTH
// branches: already-v2 is a no-op with no backup; an unknown shape is a hard
// error WITHOUT touching the file.
func TestMigrateAppsDetectByShape(t *testing.T) {
	t.Run("already-v2-is-a-no-op", func(t *testing.T) {
		dataDir := writeRawApps(t, `{"schema_version":2,"projects":[{"id":"a","name":"a"}],"deployments":[]}`)
		antes := sha256Of(t, appsPath(dataDir))
		if err := MigrateApps(AppsMigration{DataDir: dataDir}); err != nil {
			t.Fatalf("migrate over v2: %v", err)
		}
		if n := bakCount(t, dataDir); n != 0 {
			t.Fatalf("the no-op created %d backup(s)", n)
		}
		if depois := sha256Of(t, appsPath(dataDir)); depois != antes {
			t.Fatalf("the no-op rewrote the file")
		}
	})

	t.Run("unknown-shape-is-an-error-without-touching", func(t *testing.T) {
		for _, body := range []string{
			`{"schema_version":3,"projects":[]}`,
			`{"apps":[]}`,
			`"uma string solta"`,
			`{`,
		} {
			dataDir := writeRawApps(t, body)
			antes := sha256Of(t, appsPath(dataDir))
			err := MigrateApps(AppsMigration{DataDir: dataDir})
			if err == nil {
				t.Fatalf("shape %q was ACCEPTED by the migration", body)
			}
			// The message has to say WHICH shape this is, not just "it errored":
			// without "unknown format"+schema_version, a binary that merely
			// trips over deserialisation by accident would pass this pin
			// (measured: the mutation "treat unknown as v1" passed until this
			// assertion existed).
			if !strings.Contains(err.Error(), "apps.json") ||
				!strings.Contains(err.Error(), "unknown format") ||
				!strings.Contains(err.Error(), "schema_version") {
				t.Fatalf("the error does not classify the refused shape (shape %q): %v", body, err)
			}
			if depois := sha256Of(t, appsPath(dataDir)); depois != antes {
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

// TestMigrateAppsRollbackOnWriteFailure: proving the migration works does not
// prove the ROLLBACK works. The failure is injected where it can really happen
// — in the atomic write — by pre-creating the .new temporary as a DIRECTORY
// (EISDIR brings the open down even for root, unlike a permission bit).
// After the failure apps.json has to still be the original v1, byte for byte.
func TestMigrateAppsRollbackOnWriteFailure(t *testing.T) {
	dataDir := setupLegacyAppsDir(t, App{Name: "hello", Branch: "main", Port: 8091})
	antes := sha256Of(t, appsPath(dataDir))

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
	if depois := sha256Of(t, appsPath(dataDir)); depois != antes {
		t.Fatalf("the rollback did NOT restore the v1 apps.json (sha %s → %s)", antes, depois)
	}
	// The restored v1 has to still read as v1 (not a half-written envelope).
	sh, _, derr := DetectShape(dataDir)
	if derr != nil || sh != ShapeV1Array {
		t.Fatalf("after the rollback the shape is %q (err=%v), want %q", sh, derr, ShapeV1Array)
	}
}

// TestMigrateAppsConcurrentLock: cast from TestMigrateV1ToV2_ConcurrentLock,
// but with a real PROCESS holding the lock.
//
// Goroutines would not do: appsFileMu is package-level, so the mutex alone
// would already serialise the two — the test would pass with the flock REMOVED,
// and the flock is precisely what covers the real case (the post-receive hook
// runs inside vpsmctl, which is another process). The child is this very test
// binary, re-executed with an environment variable, and it holds the lock until
// the parent closes its stdin — no sleep, no waiting on a clock.
func TestMigrateAppsConcurrentLock(t *testing.T) {
	dataDir := setupLegacyAppsDir(t, App{Name: "hello", Branch: "main"})
	antes := sha256Of(t, appsPath(dataDir))

	filho := exec.Command(os.Args[0], "-test.run=TestAuxiliarSeguraTrava", "-test.v")
	filho.Env = append(os.Environ(), "DEPLOY_TRAVA_DATADIR="+dataDir)
	stdin, err := filho.StdinPipe()
	if err != nil {
		t.Fatalf("child stdin: %v", err)
	}
	stdout, err := filho.StdoutPipe()
	if err != nil {
		t.Fatalf("child stdout: %v", err)
	}
	if err := filho.Start(); err != nil {
		t.Fatalf("starting the child: %v", err)
	}
	defer func() {
		_ = stdin.Close()
		_ = filho.Wait()
	}()

	// Wait for the child to announce that the lock is his.
	sc := bufio.NewScanner(stdout)
	travado := false
	for sc.Scan() {
		if strings.Contains(sc.Text(), "TRAVADO") {
			travado = true
			break
		}
	}
	if !travado {
		t.Fatalf("the child could not hold the lock")
	}

	err = MigrateApps(AppsMigration{DataDir: dataDir})
	if !errors.Is(err, ErrConcurrentAppsMigration) {
		t.Fatalf("a migration with the lock held by another process returned %v, want ErrConcurrentAppsMigration", err)
	}
	if depois := sha256Of(t, appsPath(dataDir)); depois != antes {
		t.Fatalf("the losing migration TOUCHED the file")
	}
	if n := bakCount(t, dataDir); n != 0 {
		t.Fatalf("the losing migration created %d backup(s)", n)
	}

	// Release the lock and prove the migration now completes (failing closed is
	// for retrying, not for giving up — systemd retries the boot).
	_ = stdin.Close()
	if err := filho.Wait(); err != nil {
		t.Fatalf("child: %v", err)
	}
	if err := MigrateApps(AppsMigration{DataDir: dataDir}); err != nil {
		t.Fatalf("migration after releasing the lock: %v", err)
	}
	if sh, _, _ := DetectShape(dataDir); sh != ShapeV2 {
		t.Fatalf("after the retry the shape is %q", sh)
	}
}

// TestAuxiliarSeguraTrava is not a test: it is TestMigrateAppsConcurrentLock's
// child process. Without the environment variable, it skips.
func TestAuxiliarSeguraTrava(t *testing.T) {
	dataDir := os.Getenv("DEPLOY_TRAVA_DATADIR")
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
	fmt.Println("TRAVADO")
	// Hold on until the parent closes stdin.
	_, _ = io.Copy(io.Discard, os.Stdin)
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}

// TestMigrateAppsAuditAppended: cast from TestMigrateV1ToV2_AuditAppended.
func TestMigrateAppsAuditAppended(t *testing.T) {
	dataDir := setupLegacyAppsDir(t, App{Name: "hello", Branch: "main"})
	auditLog, err := auth.NewAuditLog(filepath.Join(dataDir, "audit.log"))
	if err != nil {
		t.Fatalf("audit: %v", err)
	}

	if err := MigrateApps(AppsMigration{DataDir: dataDir, Audit: auditLog}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	achou := false
	for _, e := range auditLog.Tail(10) {
		if e.Action == "migration.apps.v2" && e.User == "system" && e.Target == appsPath(dataDir) {
			achou = true
		}
	}
	if !achou {
		t.Fatalf("the migration.apps.v2 event (user=system) is not on the audit trail: %+v", auditLog.Tail(10))
	}

	// An unavailable audit must NOT undo a migration that is already committed.
	dataDir2 := setupLegacyAppsDir(t, App{Name: "hello"})
	if err := MigrateApps(AppsMigration{DataDir: dataDir2, Audit: nil}); err != nil {
		t.Fatalf("migration without audit: %v", err)
	}
}

// TestGuardCLIRecusaFormas: the guard vpsmctl calls BEFORE touching the file.
// It accepts only what this binary knows how to read; the rest is a refusal
// that NAMES the binary — never a rewrite.
func TestGuardCLIRecusaFormas(t *testing.T) {
	casos := []struct {
		nome   string
		body   string
		aceita bool
	}{
		{"v2-corrente", `{"schema_version":2,"projects":[],"deployments":[]}`, true},
		{"v1-array", `[{"name":"hello"}]`, false},
		{"versao-futura", `{"schema_version":3,"projects":[]}`, false},
		{"lixo", `{`, false},
		{"vazio", ``, false},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			dataDir := writeRawApps(t, c.body)
			antes := sha256Of(t, appsPath(dataDir))
			err := GuardCLI(dataDir)
			if c.aceita {
				if err != nil {
					t.Fatalf("an acceptable shape was refused: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("shape %q was ACCEPTED by vpsmctl", c.body)
			}
			if !strings.Contains(err.Error(), "vpsmctl") {
				t.Fatalf("the refusal does not NAME the binary: %v", err)
			}
			if !strings.Contains(err.Error(), "schema_version") {
				t.Fatalf("the refusal does not mention schema_version: %v", err)
			}
			if depois := sha256Of(t, appsPath(dataDir)); depois != antes {
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

// TestMigrateAppsEnsaioComArquivoReal runs the migration over a COPY of the
// production apps.json and proves both directions: the way out (it becomes v2
// without losing an app) and the way back (restoring the .bak gives the file
// back byte for byte).
//
// Skipped by default — proving a migration with real data demands the real data:
//
//	DEPLOY_ENSAIO_APPS=/caminho/para/apps.json go test ./internal/deploy/ \
//	    -run TestMigrateAppsEnsaioComArquivoReal -v
//
// The file it points at is NOT modified: the test works on a copy.
func TestMigrateAppsEnsaioComArquivoReal(t *testing.T) {
	origem := os.Getenv("DEPLOY_ENSAIO_APPS")
	if origem == "" {
		t.Skip("set DEPLOY_ENSAIO_APPS=<path to the real apps.json> for the rehearsal")
	}
	raw, err := os.ReadFile(origem)
	if err != nil {
		t.Fatalf("reading %s: %v", origem, err)
	}
	var v1 []App
	if err := json.Unmarshal(raw, &v1); err != nil {
		t.Fatalf("%s is not a v1 apps.json: %v", origem, err)
	}

	dataDir := writeRawApps(t, string(raw))
	antes := sha256Of(t, appsPath(dataDir))

	if err := MigrateApps(AppsMigration{DataDir: dataDir}); err != nil {
		t.Fatalf("migration of the real file: %v", err)
	}
	f := readEnvelope(t, dataDir)
	if f.SchemaVersion != AppsSchemaVersion || len(f.Projects) != len(v1) || len(f.Deployments) != len(v1) {
		t.Fatalf("envelope: schema=%d projects=%d deployments=%d, want %d/%d/%d",
			f.SchemaVersion, len(f.Projects), len(f.Deployments), AppsSchemaVersion, len(v1), len(v1))
	}
	// Set against set: no app may vanish and none may appear.
	nomes := map[string]bool{}
	for _, a := range v1 {
		nomes[a.Name] = true
	}
	for _, d := range f.Deployments {
		if !nomes[d.ProjectID] {
			t.Fatalf("deployment %q matches no app from the real file", d.ProjectID)
		}
		delete(nomes, d.ProjectID)
	}
	if len(nomes) != 0 {
		t.Fatalf("apps from the real file that vanished in the migration: %v", nomes)
	}

	// The way back: the operator restores the .bak and the file is identical to the original.
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
	if depois := sha256Of(t, appsPath(dataDir)); depois != antes {
		t.Fatalf("the rollback of the REAL file did not give the original back (sha %s → %s)", antes, depois)
	}
	t.Logf("rehearsal ok: %d app(s) migrated and restored from the backup", len(v1))
}
