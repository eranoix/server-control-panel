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

const AppsSchemaVersion = 2

const DefaultNodeID = "vps-187"

type File struct {
	SchemaVersion int          `json:"schema_version"`
	Projects      []Project    `json:"projects"`
	Deployments   []Deployment `json:"deployments"`
}

type Project struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Deployment struct {
	ID          string            `json:"id"`
	ProjectID   string            `json:"project_id"`
	NodeID      string            `json:"node_id"`
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

type Shape string

const (
	ShapeMissing Shape = "missing"
	ShapeV1Array Shape = "v1-array"
	ShapeV2      Shape = "v2"
	ShapeUnknown Shape = "unknown"
)

var ErrConcurrentAppsMigration = errors.New("deploy: concurrent apps.json migration in progress")

func appsPath(dataDir string) string {
	return filepath.Join(dataDir, "deploy", "apps.json")
}

func appsLockPath(dataDir string) string {
	return filepath.Join(dataDir, "deploy", ".apps.lock")
}

func DetectShape(dataDir string) (Shape, int, error) {
	raw, err := os.ReadFile(appsPath(dataDir))
	if err != nil {
		if os.IsNotExist(err) {
			return ShapeMissing, 0, nil
		}
		return ShapeUnknown, 0, err
	}
	sh, ver := detectShapeBytes(raw)
	return sh, ver, nil
}

func detectShapeBytes(raw []byte) (Shape, int) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return ShapeUnknown, 0
	}
	switch trimmed[0] {
	case '[':
		var apps []App
		if json.Unmarshal(trimmed, &apps) != nil {
			return ShapeUnknown, 0
		}
		return ShapeV1Array, 0
	case '{':
		var envelope struct {
			SchemaVersion int `json:"schema_version"`
		}
		if json.Unmarshal(trimmed, &envelope) != nil {
			return ShapeUnknown, 0
		}
		if envelope.SchemaVersion != AppsSchemaVersion {
			return ShapeUnknown, envelope.SchemaVersion
		}
		var f File
		if json.Unmarshal(trimmed, &f) != nil {
			return ShapeUnknown, envelope.SchemaVersion
		}
		return ShapeV2, envelope.SchemaVersion
	default:
		return ShapeUnknown, 0
	}
}

const cliBinaryName = "panelctl"

func GuardCLI(dataDir string) error {
	shape, version, err := DetectShape(dataDir)
	if err != nil {
		return fmt.Errorf("%s: read %s: %w", cliBinaryName, appsPath(dataDir), err)
	}
	switch shape {
	case ShapeMissing, ShapeV2:
		return nil
	case ShapeV1Array:
		return fmt.Errorf(
			"%s: %s is still in v1 format (raw array, no schema_version) and %s does NOT migrate: "+
				"start server-control-panel (cmd/server) once, it migrates to schema_version=%d at boot, "+
				"then run %s again; nothing was written",
			cliBinaryName, appsPath(dataDir), cliBinaryName, AppsSchemaVersion, cliBinaryName)
	default:
		return fmt.Errorf(
			"%s: %s in unknown format (schema_version=%d): this binary %s reads schema_version=%d — "+
				"update %s (it ships together with server-control-panel) instead of letting it rewrite the file; "+
				"nothing was written",
			cliBinaryName, appsPath(dataDir), version, cliBinaryName, AppsSchemaVersion, cliBinaryName)
	}
}

type AppsMigration struct {
	DataDir string
	Audit   *auth.AuditLog
	NodeID  string
}

func MigrateApps(d AppsMigration) error {
	if d.DataDir == "" {
		return errors.New("deploy: migrate: DataDir empty")
	}
	if d.NodeID == "" {
		d.NodeID = DefaultNodeID
	}
	path := appsPath(d.DataDir)

	if sh, _, err := DetectShape(d.DataDir); err == nil && (sh == ShapeV2 || sh == ShapeMissing) {
		return nil
	}

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

	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("deploy: migrate: read apps.json: %w", err)
	}

	shape, version := detectShapeBytes(raw)
	switch shape {
	case ShapeV2:
		return nil
	case ShapeV1Array:
	default:
		return fmt.Errorf(
			"deploy: migrate: apps.json in unknown format (schema_version=%d, this binary reads %d) at %s: "+
				"nothing was written; update the binary instead of letting it rewrite the file",
			version, AppsSchemaVersion, path)
	}

	var apps []App
	if err := json.Unmarshal(raw, &apps); err != nil {
		return fmt.Errorf("deploy: migrate: apps.json v1 unreadable at %s: %w", path, err)
	}

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
	if err := writeFileAtomic(path, out, 0o600); err != nil {
		return rollback("write envelope", err)
	}

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
	if df, err := os.Open(dir); err == nil {
		_ = df.Sync()
		_ = df.Close()
	}
	return nil
}
