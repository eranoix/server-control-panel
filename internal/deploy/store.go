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

const AppsRoot = "/srv/panel-apps"

const maxDeployHistory = 30

var nameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{1,38}[a-z0-9]$`)

type App struct {
	Name        string            `json:"name"`
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

type DeployRecord struct {
	ID       string `json:"id"`
	Ref      string `json:"ref"`
	Commit   string `json:"commit"`
	Preview  string `json:"preview,omitempty"`
	Project  string `json:"project"`
	Status   string `json:"status"`
	Started  int64  `json:"started"`
	Finished int64  `json:"finished,omitempty"`
	Message  string `json:"message,omitempty"`
	Error    string `json:"error,omitempty"`
}

type Store struct {
	dataDir string
}

var appsFileMu sync.Mutex

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

func Open(dataDir string) *Store { return &Store{dataDir: dataDir} }

func (s *Store) file() string { return filepath.Join(s.dataDir, "deploy", "apps.json") }
func (s *Store) logDir(name string) string {
	return filepath.Join(s.dataDir, "deploy", name)
}

func (s *Store) LogPath(name, deployID string) string {
	return filepath.Join(s.logDir(name), deployID+".log")
}

func RepoPath(name string) string { return filepath.Join(AppsRoot, name+".git") }
func WorkDir(name, preview string) string {
	if preview == "" {
		return filepath.Join(AppsRoot, name)
	}
	return filepath.Join(AppsRoot, name+"-pr-"+preview)
}

func ComposeProject(name, preview string) string {
	if preview == "" {
		return "panel-" + name
	}
	return "panel-" + name + "-pr-" + preview
}

func ValidName(name string) bool { return nameRe.MatchString(name) }

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
				"this binary reads schema_version=%d; start server-control-panel (cmd/server) once, "+
				"it migrates at boot; nothing was written", s.file(), AppsSchemaVersion)
	default:
		return File{}, fmt.Errorf(
			"apps.json in unknown format (schema_version=%d, this server-control-panel binary reads %d) at %s: "+
				"nothing was written", version, AppsSchemaVersion, s.file())
	}
}

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
			p = Project{ID: d.ProjectID, Name: d.ProjectID}
		}
		apps = append(apps, v2ToApp(p, d))
	}
	sort.Slice(apps, func(i, j int) bool { return apps[i].Name < apps[j].Name })
	return apps, nil
}

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
	return writeFileAtomic(s.file(), b, 0o600)
}

func (s *Store) List() ([]App, error) {
	defer s.lock()()
	return s.load()
}

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

func (s *Store) Save(a App) error {
	defer s.lock()()
	return s.saveLocked(a)
}

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
