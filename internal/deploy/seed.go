package deploy

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

// CreateAndSeed creates an app from a template and seeds the
// docker-compose.yml as the initial commit on the production branch — WITHOUT
// deploying (fast, no build). The caller queues the deploy so the build can be
// streamed. `env` initialises App.Env (EnvHints already resolved by the caller).
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
		_ = s.Destroy(context.Background(), created.Name, io.Discard) // undo the half-made app
		return App{}, fmt.Errorf("seed template: %w", err)
	}
	return created, nil
}

// CreateFromTemplate = CreateAndSeed + an inline Deploy (the CLI and test path).
func (s *Store) CreateFromTemplate(ctx context.Context, tmpl Template, app App, env map[string]string, logW io.Writer) (App, DeployRecord, error) {
	created, err := s.CreateAndSeed(ctx, tmpl, app, env)
	if err != nil {
		return App{}, DeployRecord{}, err
	}
	rec, err := Deploy(ctx, s, Spec{App: created.Name, Trigger: "ui"}, logW)
	return created, rec, err
}

// seedCommit writes the files into the app's work-tree and makes a commit on
// the given branch, straight against the bare repo (GIT_DIR + GIT_WORK_TREE +
// the bare repo's own index). It leaves HEAD pointing at the branch → Deploy
// resolves the ref.
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
	// point the bare repo's HEAD at the target branch before committing
	git := func(args ...string) error {
		c := exec.CommandContext(ctx, "git", args...)
		c.Env = append(os.Environ(),
			"GIT_DIR="+repo, "GIT_WORK_TREE="+work,
			"GIT_AUTHOR_NAME=vpsm", "GIT_AUTHOR_EMAIL=deploy@vpsm",
			"GIT_COMMITTER_NAME=vpsm", "GIT_COMMITTER_EMAIL=deploy@vpsm")
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
