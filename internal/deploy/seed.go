package deploy

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

func (s *Store) CreateAndSeed(ctx context.Context, tmpl Template, app App, env map[string]string) (App, error) {
	if app.Branch == "" {
		app.Branch = "main"
	}
	app.ComposeFile = "docker-compose.yml"
	if app.Port == 0 {
		app.Port = tmpl.Port
	}
	if env != nil {
		app.Env = env
	}
	created, err := s.Create(app)
	if err != nil {
		return App{}, err
	}
	if err := seedCommit(ctx, created.Name, created.Branch, map[string]string{
		"docker-compose.yml": tmpl.Compose,
	}, "seed: "+tmpl.Name); err != nil {
		_ = s.Destroy(context.Background(), created.Name, io.Discard)
		return App{}, fmt.Errorf("seed template: %w", err)
	}
	return created, nil
}

func (s *Store) CreateFromTemplate(ctx context.Context, tmpl Template, app App, env map[string]string, logW io.Writer) (App, DeployRecord, error) {
	created, err := s.CreateAndSeed(ctx, tmpl, app, env)
	if err != nil {
		return App{}, DeployRecord{}, err
	}
	rec, err := Deploy(ctx, s, Spec{App: created.Name, Trigger: "ui"}, logW)
	return created, rec, err
}

func seedCommit(ctx context.Context, name, branch string, files map[string]string, msg string) error {
	repo := RepoPath(name)
	work := WorkDir(name, "")
	if err := os.MkdirAll(work, 0o700); err != nil {
		return err
	}
	for rel, content := range files {
		p := filepath.Join(work, filepath.Clean(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			return err
		}
	}
	git := func(args ...string) error {
		c := exec.CommandContext(ctx, "git", args...)
		c.Env = append(os.Environ(),
			"GIT_DIR="+repo, "GIT_WORK_TREE="+work,
			"GIT_AUTHOR_NAME=panel", "GIT_AUTHOR_EMAIL=deploy@panel",
			"GIT_COMMITTER_NAME=panel", "GIT_COMMITTER_EMAIL=deploy@panel")
		out, err := c.CombinedOutput()
		if err != nil {
			return fmt.Errorf("%v: %s", err, out)
		}
		return nil
	}
	if err := git("symbolic-ref", "HEAD", "refs/heads/"+branch); err != nil {
		return err
	}
	if err := git("add", "-A"); err != nil {
		return err
	}
	return git("commit", "-m", msg)
}
