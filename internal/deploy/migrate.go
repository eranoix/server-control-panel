// migrate.go — migration of apps.json from the v1 format (a RAW array of App,
// tied to a single node) to the v2 envelope
// {schema_version, projects, deployments} of the multi-node vocabulary.
//
// The mould is internal/config/migrate.go (MigrateV1ToV2): a fail-closed flock,
// a re-read AFTER acquiring the lock, a backup before any write, a rollback if
// the write fails, and a non-fatal audit event. Two real differences in this
// file, measured before a line of it was written:
//
//  1. The v1 apps.json is a RAW ARRAY (`[{"name":"hello",…}]`) — it has nowhere
//     to keep a schema_version, unlike config.json. That is why detection here
//     goes BY SHAPE (see DetectShape), not by a version field.
//  2. This file has THREE possible writers (the HTTP server, the queue runner
//     and the vpsmctl behind the post-receive hook), and only the server
//     migrates: vpsmctl REFUSES an envelope it does not understand instead of
//     rewriting it (see GuardCLI).
package deploy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"server-control-panel/internal/auth"
)

// AppsSchemaVersion is the envelope version THIS binary reads and writes.
// An envelope on a different version is refused, never rewritten (an old binary
// that rewrites a newer document erases the fields it does not know about).
const AppsSchemaVersion = 2

// DefaultNodeID is the node where the deploys that predate the multi-node model
// live. Every v1 App becomes a Deployment stamped with it — "no node_id" is not
// a state v2 admits.
const DefaultNodeID = "vps-187"

// File is the v2 envelope written to <dataDir>/deploy/apps.json.
type File struct {
	SchemaVersion int          `json:"schema_version"`
	Projects      []Project    `json:"projects"`
	Deployments   []Deployment `json:"deployments"`
}

// Project is the publishable application, independent of where it runs.
//
// The function that used to return the compose namespace was called Project and
// was renamed to ComposeProject (store.go) — the domain name takes precedence
// over the docker detail, and Go does not admit a type and a function of the
// same name in the same package.
type Project struct {
	ID   string `json:"id"`   // stable slug; today the same as the v1 app name
	Name string `json:"name"` // displayed label
}

// Deployment is the publication of a Project on a Node. It is DeployRecord
// raised to a first-class citizen: it holds the configuration v1 kept inside
// App (branch, compose, env, history) plus the NodeID v1 did not have.
type Deployment struct {
	ID          string            `json:"id"`         // "<project_id>@<node_id>"
	ProjectID   string            `json:"project_id"` // points at Project.ID
	NodeID      string            `json:"node_id"`    // the node this deployment runs on
	Branch      string            `json:"branch"`
	ComposeFile string            `json:"compose_file"`
	Domain      string            `json:"domain"`
	Port        int               `json:"port"`
	Env         map[string]string `json:"env"`
	PreviewEnv  map[string]string `json:"preview_env"`
	Autodeploy  bool              `json:"autodeploy"`
	Created     int64             `json:"created"`
	Updated     int64             `json:"updated"`
	Deploys     []DeployRecord    `json:"deploys"`
}

// Shape is the shape of apps.json on disk. It exists because v1 has no version:
// classifying the file is the first decision any reader of it makes.
type Shape string

const (
	ShapeAusente      Shape = "ausente"      // file does not exist — fresh install
	ShapeV1Array      Shape = "v1-array"     // array cru de App (formato antigo)
	ShapeV2           Shape = "v2"           // envelope {schema_version: 2, …}
	ShapeDesconhecida Shape = "desconhecida" // anything else: a future version, garbage, or truncated
)

// ErrConcurrentAppsMigration is returned when the apps.json lock is already
// taken — another process (the server, the queue runner or the vpsmctl behind
// the hook) is in the middle of a write. It fails CLOSED, like config's
// ErrConcurrentMigration: systemd retries the boot and the second attempt sees
// v2 and does nothing.
var ErrConcurrentAppsMigration = errors.New("deploy: concurrent apps.json migration in progress")

// appsPath is the path of the registry. It mirrors Store.file() on purpose: the
// migration and the store contend for the SAME file and the SAME lock.
func appsPath(dataDir string) string {
	return filepath.Join(dataDir, "deploy", "apps.json")
}

// appsLockPath is the store's .apps.lock — reused here on purpose. A lock of
// the migration's own would not stop the post-receive hook from writing to the
// file in the middle of it; it is the same file, so it has to be the same lock.
func appsLockPath(dataDir string) string {
	return filepath.Join(dataDir, "deploy", ".apps.lock")
}

// DetectShape classifies the apps.json on disk. The second return is the
// schema_version observed (0 when the document has none), used in the refusal
// messages — saying "formato desconhecido" without saying WHICH format forces
// the operator to open the file by hand.
func DetectShape(dataDir string) (Shape, int, error) {
	raw, err := os.ReadFile(appsPath(dataDir))
	if err != nil {
		if os.IsNotExist(err) {
			return ShapeAusente, 0, nil
		}
		return ShapeDesconhecida, 0, err
	}
	sh, ver := detectShapeBytes(raw)
	return sh, ver, nil
}

// detectShapeBytes is the detection BY SHAPE (difference 1 at the top of this
// file). An empty file counts as unknown, not as an empty list: zero bytes is
// the signature of a power cut between the rename and the flush — and this
// house has a UPS with no data cable. Treating it as "no apps" would wipe the
// registry.
func detectShapeBytes(raw []byte) (Shape, int) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return ShapeDesconhecida, 0
	}
	switch trimmed[0] {
	case '[':
		var apps []App
		if json.Unmarshal(trimmed, &apps) != nil {
			return ShapeDesconhecida, 0
		}
		return ShapeV1Array, 0
	case '{':
		var envelope struct {
			SchemaVersion int `json:"schema_version"`
		}
		if json.Unmarshal(trimmed, &envelope) != nil {
			return ShapeDesconhecida, 0
		}
		if envelope.SchemaVersion != AppsSchemaVersion {
			return ShapeDesconhecida, envelope.SchemaVersion
		}
		var f File
		if json.Unmarshal(trimmed, &f) != nil {
			// Claims to be v2 but does not decode as v2: garbage with the right label.
			return ShapeDesconhecida, envelope.SchemaVersion
		}
		return ShapeV2, envelope.SchemaVersion
	default:
		return ShapeDesconhecida, 0
	}
}

// cliBinaryName is the binary that does NOT migrate. Naming it in the message
// is the whole point of the guard: whoever reads the error needs to know WHICH
// binary is out of date, otherwise the symptom is a rejected `git push` with
// text the operator cannot decipher.
const cliBinaryName = "vpsmctl"

// GuardCLI is the refusal: vpsmctl shares apps.json with the server but NEVER
// migrates it. When it meets a shape this binary does not write, it fails
// CLOSED — without opening the store, without writing, without a backup —
// instead of rewriting the file into the format it knows.
//
// It mirrors internal/config/config_io.go:57 (a config whose schema_version is
// higher than the binary's aborts instead of being rewritten). apps.json had no
// such protection: an old vpsmctl opening the store would write the v2 back as
// a v1 array on the first `git push`, erasing the node_id of every deployment.
//
// Operational consequence: the server and vpsmctl have to be released TOGETHER.
func GuardCLI(dataDir string) error {
	shape, version, err := DetectShape(dataDir)
	if err != nil {
		return fmt.Errorf("%s: read %s: %w", cliBinaryName, appsPath(dataDir), err)
	}
	switch shape {
	case ShapeAusente, ShapeV2:
		return nil
	case ShapeV1Array:
		return fmt.Errorf(
			"%s: %s is still in v1 format (raw array, no schema_version) and %s does NOT migrate: "+
				"suba o vps-manager (cmd/server) uma vez — ele migra para schema_version=%d no boot — "+
				"e rode o %s de novo; nada foi escrito",
			cliBinaryName, appsPath(dataDir), cliBinaryName, AppsSchemaVersion, cliBinaryName)
	default:
		return fmt.Errorf(
			"%s: %s in unknown format (schema_version=%d): this binary %s reads schema_version=%d — "+
				"atualize o %s (ele e o vps-manager são publicados juntos) em vez de deixá-lo reescrever o arquivo; "+
				"nada foi escrito",
			cliBinaryName, appsPath(dataDir), version, cliBinaryName, AppsSchemaVersion, cliBinaryName)
	}
}

// AppsMigration gathers what MigrateApps needs. A struct (and not loose
// arguments) for the same reason as config.MigrationDeps: the caller does not
// have to memorise an order, and the next migration extends it without breaking
// existing calls.
type AppsMigration struct {
	// DataDir is the state directory; the registry lives at <DataDir>/deploy/apps.json.
	DataDir string
	// Audit, when non-nil, receives the "migration.apps.v2" event. An audit
	// failure does NOT undo the migration — it is already committed to disk.
	Audit *auth.AuditLog
	// NodeID is the node that inherits the single-node deploys. A field (rather
	// than the constant used directly) so a test can vary it, like config's Primary.
	NodeID string
}

// MigrateApps converts the v1 apps.json into the v2 envelope. It is idempotent
// (a cheap no-op once it is already v2), reentrant across PROCESSES (the server
// and the vpsmctl behind the post-receive hook can both find it pending at the
// same time) and atomic (backup first, write via tmp+rename with fsync,
// rollback on error).
//
// The steps, in order:
//
//  1. A cheap read BEFORE the lock: v2 or absent leave without touching a thing.
//  2. Process mutex (TryLock) + flock LOCK_EX|LOCK_NB on the store's .apps.lock.
//  3. RE-READ from disk: the other process may have migrated in the meantime.
//  4. Classify by shape; an unknown one aborts WITHOUT writing anything.
//  5. Backup at apps.json.bak.<unix-ts> (hardlink; a copy if that is not possible).
//  6. Convert each App into 1 Project + 1 Deployment carrying NodeID.
//  7. Write the envelope with writeFileAtomic (fsync of file and directory).
//  8. Audit "migration.apps.v2" (non-fatal).
//
// An error at 7 restores the backup over apps.json (rollback).
func MigrateApps(d AppsMigration) error {
	if d.DataDir == "" {
		return errors.New("deploy: migrate: DataDir empty")
	}
	if d.NodeID == "" {
		d.NodeID = DefaultNodeID
	}
	path := appsPath(d.DataDir)

	// Step 1: the cheap no-op. Without it, every boot would create the lock file
	// and contend for the flock with the hook for nothing.
	if sh, _, err := DetectShape(d.DataDir); err == nil && (sh == ShapeV2 || sh == ShapeAusente) {
		return nil
	}

	// Step 2: both locks, in the SAME order Store.lock() takes them — an
	// inverted order between two paths is the recipe for deadlock. TryLock (and
	// not Lock) because the semantics here are to fail closed, like config.
	if !appsFileMu.TryLock() {
		return ErrConcurrentAppsMigration
	}
	defer appsFileMu.Unlock()

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("deploy: migrate: mkdir: %w", err)
	}
	lockF, err := os.OpenFile(appsLockPath(d.DataDir), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("deploy: migrate: open lock: %w", err)
	}
	defer lockF.Close()
	if err := syscall.Flock(int(lockF.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return ErrConcurrentAppsMigration
	}
	defer func() { _ = syscall.Flock(int(lockF.Fd()), syscall.LOCK_UN) }()

	// Step 3: RE-READ after the lock. Between step 1 and now, the other process
	// may have migrated the whole file.
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("deploy: migrate: read apps.json: %w", err)
	}

	// Passo 4: classificar.
	shape, version := detectShapeBytes(raw)
	switch shape {
	case ShapeV2:
		return nil // another process migrated while we were waiting for the lock
	case ShapeV1Array:
		// segue
	default:
		return fmt.Errorf(
			"deploy: migrate: apps.json in unknown format (schema_version=%d, this binary reads %d) at %s: "+
				"nada foi escrito — atualize o binário em vez de deixá-lo reescrever o arquivo",
			version, AppsSchemaVersion, path)
	}

	var apps []App
	if err := json.Unmarshal(raw, &apps); err != nil {
		return fmt.Errorf("deploy: migrate: apps.json v1 unreadable at %s: %w", path, err)
	}

	// Step 5: the backup, BEFORE any write.
	bakPath := fmt.Sprintf("%s.bak.%d", path, time.Now().Unix())
	if err := snapshotFile(path, bakPath); err != nil {
		return fmt.Errorf("deploy: migrate: backup: %w", err)
	}
	log.Printf("migrate apps v1→v2: backup at %s", bakPath)

	rollback := func(label string, cause error) error {
		log.Printf("migrate apps v1→v2 FAILED at %s: %v — restoring from %s", label, cause, bakPath)
		if rmErr := os.Remove(path); rmErr != nil && !os.IsNotExist(rmErr) {
			return fmt.Errorf("migrate apps %s: rollback also failed (rm: %v) — restore from %s manually: %w", label, rmErr, bakPath, cause)
		}
		if lnErr := snapshotFile(bakPath, path); lnErr != nil {
			return fmt.Errorf("migrate apps %s: rollback also failed (restore: %v) — restore from %s manually: %w", label, lnErr, bakPath, cause)
		}
		if rmErr := os.Remove(bakPath); rmErr != nil && !os.IsNotExist(rmErr) {
			log.Printf("migrate apps: backup %s restored but not removed: %v", bakPath, rmErr)
		}
		return fmt.Errorf("migrate apps %s: rollback from %s: %w", label, bakPath, cause)
	}

	// Step 6: the conversion. One v1 App = one Project + one Deployment on the local node.
	f := File{
		SchemaVersion: AppsSchemaVersion,
		Projects:      make([]Project, 0, len(apps)),
		Deployments:   make([]Deployment, 0, len(apps)),
	}
	for _, a := range apps {
		p, dep := appToV2(a, d.NodeID)
		f.Projects = append(f.Projects, p)
		f.Deployments = append(f.Deployments, dep)
	}

	out, err := json.MarshalIndent(&f, "", " ")
	if err != nil {
		return rollback("marshal envelope", err)
	}
	// Step 7: the durable write. A rename without fsync leaves a zero-byte file
	// on an abrupt power cut — an expected failure mode in this house.
	if err := writeFileAtomic(path, out, 0o600); err != nil {
		return rollback("escrever envelope", err)
	}

	// Step 8: the audit. Non-fatal: the migration is already on disk.
	if d.Audit != nil {
		d.Audit.Append(auth.Event{
			User:   "system",
			Action: "migration.apps.v2",
			Target: path,
		})
	}
	log.Printf("migrate apps v1→v2: %d app(s) → %d project(s)/%d deployment(s) on node %s; backup at %s",
		len(apps), len(f.Projects), len(f.Deployments), d.NodeID, bakPath)
	return nil
}

// appToV2 converts a v1 App into the Project+Deployment pair. The deployment's
// ID is derived (project@node) instead of drawn at random: the same app on the
// same node has to produce the same ID on any rerun, or the migration is not idempotent.
func appToV2(a App, nodeID string) (Project, Deployment) {
	return Project{ID: a.Name, Name: a.Name},
		Deployment{
			ID:          a.Name + "@" + nodeID,
			ProjectID:   a.Name,
			NodeID:      nodeID,
			Branch:      a.Branch,
			ComposeFile: a.ComposeFile,
			Domain:      a.Domain,
			Port:        a.Port,
			Env:         a.Env,
			PreviewEnv:  a.PreviewEnv,
			Autodeploy:  a.Autodeploy,
			Created:     a.Created,
			Updated:     a.Updated,
			Deploys:     a.Deploys,
		}
}

// v2ToApp is the compatibility layer's way back: the handlers and the hook go
// on speaking App while the disk is already v2.
//
// App.Name receives the project's ID, NOT the display Project.Name: Name is the
// app's identity throughout the rest of the subsystem (the DNS-safe slug
// validated by ValidName, the name of the bare repo, of the work-tree and of
// the compose project). Using the label here would give a renamed project a new
// identity on every round-trip — measured: the other node's deployment VANISHED
// from the envelope, because the upsert by name no longer found the earlier entry.
func v2ToApp(p Project, d Deployment) App {
	name := d.ProjectID
	if name == "" {
		name = p.ID
	}
	return App{
		Name:        name,
		Branch:      d.Branch,
		ComposeFile: d.ComposeFile,
		Domain:      d.Domain,
		Port:        d.Port,
		Env:         d.Env,
		PreviewEnv:  d.PreviewEnv,
		Autodeploy:  d.Autodeploy,
		Created:     d.Created,
		Updated:     d.Updated,
		Deploys:     d.Deploys,
	}
}

// snapshotFile creates dst as a hardlink of src (the single-file equivalent of
// the `cp -al` config uses), falling back to a copy when the link is not
// possible. The hardlink is what makes the backup cheap AND immune to being
// rewritten by a rename: the rename swaps the name, not the inode the backup holds.
func snapshotFile(src, dst string) error {
	if err := os.Link(src, dst); err == nil {
		return nil
	}
	raw, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, raw, 0o600)
}

// writeFileAtomic writes via .new+rename with an fsync of the file AND of the
// directory. A deliberate copy of internal/config/migrate.go:451-482 (and not
// an import) so as not to create a deploy→config dependency: config is loaded
// by everyone, and the cycle would show up the first time config needed to talk
// about deploy.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp := path + ".new"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	// Without this Sync the rename can reach the disk before the content.
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	// Without this Sync of the DIRECTORY the rename itself may not survive the cut.
	if df, err := os.Open(dir); err == nil {
		_ = df.Sync()
		_ = df.Close()
	}
	return nil
}
