package deploy

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"
)

func (s *Store) Create(a App) (App, error) {
	if !ValidName(a.Name) {
		return App{}, fmt.Errorf("invalid name %q (use [a-z0-9-], 3-40 chars)", a.Name)
	}
	if _, ok, _ := s.Get(a.Name); ok {
		return App{}, fmt.Errorf("app %q already exists", a.Name)
	}
	if a.Branch == "" {
		a.Branch = "main"
	}
	if a.ComposeFile == "" {
		a.ComposeFile = "docker-compose.yml"
	}
	if a.Env == nil {
		a.Env = map[string]string{}
	}
	if a.PreviewEnv == nil {
		a.PreviewEnv = map[string]string{}
	}
	a.Created = time.Now().Unix()
	a.Updated = a.Created
	if err := Provision(a.Name); err != nil {
		return App{}, err
	}
	if err := s.Save(a); err != nil {
		return App{}, err
	}
	return a, nil
}

func (s *Store) Destroy(ctx context.Context, name string, w io.Writer) error {
	app, ok, err := s.Get(name)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("app %q does not exist", name)
	}
	for _, p := range s.Previews(app) {
		_ = TeardownPreview(ctx, s, app, p, w)
	}
	removeVhost(ctx, ComposeProject(name, ""))
	if err := Deprovision(ctx, name); err != nil {
		return err
	}
	return s.Remove(name)
}

func (s *Store) Previews(app App) []string {
	seen := map[string]bool{}
	var out []string
	for _, d := range app.Deploys {
		if d.Preview != "" && d.Status == "running" && !seen[d.Preview] {
			seen[d.Preview] = true
			out = append(out, d.Preview)
		}
	}
	return out
}

func TeardownPreview(ctx context.Context, s *Store, app App, preview string, w io.Writer) error {
	project := ComposeProject(app.Name, preview)
	_ = ComposeDown(ctx, project, WorkDir(app.Name, preview), app.ComposeFile, w)
	removeVhost(ctx, project)
	_ = os.RemoveAll(WorkDir(app.Name, preview))
	fresh, ok, _ := s.Get(app.Name)
	if ok {
		for i := range fresh.Deploys {
			if fresh.Deploys[i].Preview == preview && fresh.Deploys[i].Status == "running" {
				fresh.Deploys[i].Status = "rolled_back"
			}
		}
		_ = s.Save(fresh)
	}
	return nil
}

func LastProdDeploy(app App) (DeployRecord, bool) {
	for i := len(app.Deploys) - 1; i >= 0; i-- {
		if app.Deploys[i].Preview == "" {
			return app.Deploys[i], true
		}
	}
	return DeployRecord{}, false
}

func PreviousCommit(app App) string {
	var prodRunning []string
	for _, d := range app.Deploys {
		if d.Preview == "" && d.Status == "running" && d.Commit != "" {
			prodRunning = append(prodRunning, d.Commit)
		}
	}
	if len(prodRunning) < 2 {
		return ""
	}
	top := prodRunning[len(prodRunning)-1]
	for i := len(prodRunning) - 2; i >= 0; i-- {
		if prodRunning[i] != top {
			return prodRunning[i]
		}
	}
	return ""
}
