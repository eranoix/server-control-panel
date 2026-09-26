// Package deploy implements the Heroku-style PaaS of this control plane: each
// App is a BARE git repository on the VPS whose post-receive hook fires a
// build + `docker compose up`. The same core (Deploy) is called from two
// triggers — the hook (output streamed back to `git push`) and the app_deploy
// queue runner (a deploy from the UI, streamed over WebSocket).
//
// On-disk layout:
//
//	/srv/vpsm-apps/<name>.git        bare repo (the target of `git push`)
//	/srv/vpsm-apps/<name>            work-tree of the production deploy
//	/srv/vpsm-apps/<name>-pr-<slug>  work-tree of a preview env
//	<DataDir>/deploy/apps.json       registry (list of App, written atomically)
//	<DataDir>/deploy/<name>/<id>.log log of each deploy (streamed by the UI)
package deploy

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"syscall"
	"time"
)

// AppsRoot is where the bare repos and work-trees live. Outside DataDir on
// purpose: it is the target of the git remote
// (`root@vps:/srv/vpsm-apps/<name>.git`) and must not end up in control-plane
// state backups.
const AppsRoot = "/srv/vpsm-apps"

// maxDeployHistory caps how many DeployRecord entries we keep per app (the
// oldest are pruned). Keeps apps.json small while a rollback still reaches
// several commits back.
const maxDeployHistory = 30

var nameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{1,38}[a-z0-9]$`)

// App is a publishable application managed by this control plane.
type App struct {
	Name        string            `json:"name"`         // slug DNS-safe [a-z0-9-]
	Branch      string            `json:"branch"`       // production branch (default main)
	ComposeFile string            `json:"compose_file"` // rel. path in the work-tree (default docker-compose.yml)
	Domain      string            `json:"domain"`       // nginx server_name (optional)
	Port        int               `json:"port"`         // container's internal port for the nginx proxy (optional)
	Env         map[string]string `json:"env"`          // production env → written to .env
	PreviewEnv  map[string]string `json:"preview_env"`  // overrides p/ stacks de preview (#37)
	Autodeploy  bool              `json:"autodeploy"`   // if true, a push on Branch deploys automatically
	Created     int64             `json:"created"`
	Updated     int64             `json:"updated"`
	Deploys     []DeployRecord    `json:"deploys"` // history, newest last, pruned at maxDeployHistory
}

// DeployRecord is one deploy attempt (production or preview).
type DeployRecord struct {
	ID       string `json:"id"`                // sc-like id, also the log file's name
	Ref      string `json:"ref"`               // refs/heads/<branch>
	Commit   string `json:"commit"`            // sha completo
	Preview  string `json:"preview,omitempty"` // preview slug; empty = production
	Project  string `json:"project"`           // name of the compose project (-p) used
	Status   string `json:"status"`            // building|running|failed|rolled_back
	Started  int64  `json:"started"`
	Finished int64  `json:"finished,omitempty"`
	Message  string `json:"message,omitempty"` // the commit subject
	Error    string `json:"error,omitempty"`
}

// Store is the persisted registry of apps. Atomic write (tmp+rename) + lock.
type Store struct {
	dataDir string
}

// appsFileMu serialises access to apps.json BETWEEN goroutines of the same
// process. Package-level on purpose: every deploy.Open() creates a different
// *Store (queue runner vs HTTP), so a per-instance mutex serialised NOTHING (a
// measured bug: apps.json was overwritten whole under concurrent deploys). The
// flock on .apps.lock covers the CROSS-PROCESS case (the post-receive hook runs
// inside vpsmctl, a separate process). Together they guarantee an atomic
// read-modify-write.
var appsFileMu sync.Mutex

// lock acquires the process mutex plus an exclusive flock on the lock file.
// Returns the release function (used as `defer s.lock()()`).
func (s *Store) lock() func() {
	appsFileMu.Lock()
	lp := filepath.Join(s.dataDir, "deploy", ".apps.lock")
	_ = os.MkdirAll(filepath.Dir(lp), 0o700)
	f, err := os.OpenFile(lp, os.O_CREATE|os.O_RDWR, 0o600)
	if err == nil {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
	}
	return func() {
		if f != nil {
			_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
			_ = f.Close()
		}
		appsFileMu.Unlock()
	}
}

// Open creates a Store over <dataDir>/deploy/apps.json. No I/O happens here.
func Open(dataDir string) *Store { return &Store{dataDir: dataDir} }

func (s *Store) file() string { return filepath.Join(s.dataDir, "deploy", "apps.json") }
func (s *Store) logDir(name string) string {
	return filepath.Join(s.dataDir, "deploy", name)
}

// LogPath is the path of a deploy's log (streamed by the UI).
func (s *Store) LogPath(name, deployID string) string {
	return filepath.Join(s.logDir(name), deployID+".log")
}

// RepoPath / WorkDir derive from AppsRoot. WorkDir varies for previews.
func RepoPath(name string) string { return filepath.Join(AppsRoot, name+".git") }
func WorkDir(name, preview string) string {
	if preview == "" {
		return filepath.Join(AppsRoot, name)
	}
	return filepath.Join(AppsRoot, name+"-pr-"+preview)
}

// ComposeProject is the compose project name (the network/volume/container namespace).
// It used to be called Project; renamed because the multi-node vocabulary needs
// the name Project for the domain TYPE (migrate.go) and Go does not allow both.
// No consumer outside internal/deploy used the function (measured).
func ComposeProject(name, preview string) string {
	if preview == "" {
		return "vpsm-" + name
	}
	return "vpsm-" + name + "-pr-" + preview
}

// ValidName reports whether name is a valid app slug.
func ValidName(name string) bool { return nameRe.MatchString(name) }

// loadFile reads the v2 envelope from disk. The store does NOT migrate: only
// the server's boot does. The three non-v2 shapes have deliberately distinct
// outcomes — absent is a fresh install (a legitimate empty), v1 is "migration
// pending" and anything else is "wrong binary". Collapsing the three into
// "empty list" is what would make the UI say "no apps" and the next Save wipe
// the whole registry.
func (s *Store) loadFile() (File, error) {
	b, err := os.ReadFile(s.file())
	if err != nil {
		if os.IsNotExist(err) {
			return File{SchemaVersion: AppsSchemaVersion}, nil
		}
		return File{}, err
	}
	shape, version := detectShapeBytes(b)
	switch shape {
	case ShapeV2:
		var f File
		if err := json.Unmarshal(b, &f); err != nil {
			return File{}, fmt.Errorf("apps.json corrupted: %w", err)
		}
		return f, nil
	case ShapeV1Array:
		return File{}, fmt.Errorf(
			"apps.json still in v1 format (raw array, no schema_version) at %s: "+
				"este binário lê schema_version=%d — suba o vps-manager (cmd/server) uma vez, "+
				"que ele migra no boot; nada foi escrito", s.file(), AppsSchemaVersion)
	default:
		return File{}, fmt.Errorf(
			"apps.json in unknown format (schema_version=%d, this vps-manager binary reads %d) at %s: "+
				"nada foi escrito", version, AppsSchemaVersion, s.file())
	}
}

// load is the compatibility layer: the disk is already v2, but the handlers,
// the queue runner and the post-receive hook still speak App. It comes out
// sorted by name, the way v1 always did.
func (s *Store) load() ([]App, error) {
	f, err := s.loadFile()
	if err != nil {
		return nil, err
	}
	byID := make(map[string]Project, len(f.Projects))
	for _, p := range f.Projects {
		byID[p.ID] = p
	}
	apps := make([]App, 0, len(f.Deployments))
	for _, d := range f.Deployments {
		p, ok := byID[d.ProjectID]
		if !ok {
			// Orphan deployment: with no project the app name would come out
			// empty and the upsert by name would collide with any other orphan.
			p = Project{ID: d.ProjectID, Name: d.ProjectID}
		}
		apps = append(apps, v2ToApp(p, d))
	}
	sort.Slice(apps, func(i, j int) bool { return apps[i].Name < apps[j].Name })
	return apps, nil
}

// persistLocked writes the app list back into the v2 envelope. ALWAYS called
// with the lock held (process mutex + flock), which is what makes it safe to
// re-read the file in here without a race.
//
// The re-read is not a luxury: App is the compat view and has NO node_id and no
// project name. Rebuilding the envelope from it alone would stamp every
// deployment with the local node and flatten the names — silently erasing
// exactly the information the multi-node model exists to keep.
func (s *Store) persistLocked(apps []App) error {
	sort.Slice(apps, func(i, j int) bool { return apps[i].Name < apps[j].Name })
	prev, err := s.loadFile()
	if err != nil {
		return err
	}
	prevProj := make(map[string]Project, len(prev.Projects))
	for _, p := range prev.Projects {
		prevProj[p.ID] = p
	}
	prevDep := make(map[string]Deployment, len(prev.Deployments))
	for _, d := range prev.Deployments {
		prevDep[d.ProjectID] = d
	}

	f := File{
		SchemaVersion: AppsSchemaVersion,
		Projects:      make([]Project, 0, len(apps)),
		Deployments:   make([]Deployment, 0, len(apps)),
	}
	for _, a := range apps {
		p, d := appToV2(a, DefaultNodeID)
		if old, ok := prevProj[a.Name]; ok {
			p.Name = old.Name
		}
		if old, ok := prevDep[a.Name]; ok {
			d.NodeID = old.NodeID
			d.ID = old.ID
		}
		f.Projects = append(f.Projects, p)
		f.Deployments = append(f.Deployments, d)
	}

	b, err := json.MarshalIndent(&f, "", " ")
	if err != nil {
		return err
	}
	// writeFileAtomic (migrate.go) fsyncs the file AND the directory. The
	// previous path was os.WriteFile + os.Rename with no Sync at all: on an
	// abrupt power cut — an EXPECTED failure mode in this house, whose UPS has no
	// data cable — the rename reaches the disk before the content and apps.json
	// becomes a zero-byte file.
	return writeFileAtomic(s.file(), b, 0o600)
}

// List returns every app, sorted by name.
func (s *Store) List() ([]App, error) {
	defer s.lock()()
	return s.load()
}

// Get returns an app by name.
func (s *Store) Get(name string) (App, bool, error) {
	defer s.lock()()
	apps, err := s.load()
	if err != nil {
		return App{}, false, err
	}
	for _, a := range apps {
		if a.Name == name {
			return a, true, nil
		}
	}
	return App{}, false, nil
}

// Save inserts or updates an app (upsert by name) and persists. It does not
// touch the repo or work-tree — use Create to provision a new app.
func (s *Store) Save(a App) error {
	defer s.lock()()
	return s.saveLocked(a)
}

// UpdateEnv applies set/unset to an app's env (production or preview) under
// ONE single lock — avoiding the lost update of the
// get-outside-the-lock→mutate→save pattern.
func (s *Store) UpdateEnv(name string, preview bool, set map[string]string, unset []string) (App, error) {
	defer s.lock()()
	apps, err := s.load()
	if err != nil {
		return App{}, err
	}
	for i := range apps {
		if apps[i].Name != name {
			continue
		}
		if apps[i].Env == nil {
			apps[i].Env = map[string]string{}
		}
		if apps[i].PreviewEnv == nil {
			apps[i].PreviewEnv = map[string]string{}
		}
		target := apps[i].Env
		if preview {
			target = apps[i].PreviewEnv
		}
		for k, v := range set {
			target[k] = v
		}
		for _, k := range unset {
			delete(target, k)
		}
		apps[i].Updated = time.Now().Unix()
		if err := s.persistLocked(apps); err != nil {
			return App{}, err
		}
		return apps[i], nil
	}
	return App{}, fmt.Errorf("app %q does not exist", name)
}

func (s *Store) saveLocked(a App) error {
	apps, err := s.load()
	if err != nil {
		return err
	}
	a.Updated = time.Now().Unix()
	if len(a.Deploys) > maxDeployHistory {
		a.Deploys = a.Deploys[len(a.Deploys)-maxDeployHistory:]
	}
	found := false
	for i := range apps {
		if apps[i].Name == a.Name {
			apps[i] = a
			found = true
			break
		}
	}
	if !found {
		apps = append(apps, a)
	}
	return s.persistLocked(apps)
}

// AppendDeploy writes or updates a DeployRecord in the app's history (matched
// by ID) and persists. Used by the Deploy core to record building→running/failed.
func (s *Store) AppendDeploy(name string, rec DeployRecord) error {
	defer s.lock()()
	apps, err := s.load()
	if err != nil {
		return err
	}
	for i := range apps {
		if apps[i].Name != name {
			continue
		}
		updated := false
		for j := range apps[i].Deploys {
			if apps[i].Deploys[j].ID == rec.ID {
				apps[i].Deploys[j] = rec
				updated = true
				break
			}
		}
		if !updated {
			apps[i].Deploys = append(apps[i].Deploys, rec)
		}
		if len(apps[i].Deploys) > maxDeployHistory {
			apps[i].Deploys = apps[i].Deploys[len(apps[i].Deploys)-maxDeployHistory:]
		}
		apps[i].Updated = time.Now().Unix()
		return s.persistLocked(apps)
	}
	return fmt.Errorf("app %q does not exist", name)
}

// Remove drops the app from the registry (it does not remove the repo,
// work-tree or stack — the caller must bring those down first).
func (s *Store) Remove(name string) error {
	defer s.lock()()
	apps, err := s.load()
	if err != nil {
		return err
	}
	out := apps[:0]
	for _, a := range apps {
		if a.Name != name {
			out = append(out, a)
		}
	}
	return s.persistLocked(out)
}
