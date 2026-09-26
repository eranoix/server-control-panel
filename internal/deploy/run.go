package deploy

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// hookScript is the post-receive installed in every bare repo. It streams the
// build output back to the client that ran `git push` (the Heroku experience).
// It delegates to vpsmctl, which opens config and vault and runs the Deploy
// core. Do NOT edit by hand — Provision rewrites it.
const hookScript = `#!/bin/sh
# vps-manager post-receive — deploy Heroku-style. Gerado por vpsmctl; não editar.
# Lê as linhas <oldrev> <newrev> <refname> no stdin e as repassa ao vpsmctl.
exec vpsmctl app-deploy --name %q --hook
`

// Provision creates (idempotently) the bare repo, installs the post-receive
// hook and ensures the production work-tree root. Called by Create and by repair.
func Provision(name string) error {
	if !ValidName(name) {
		return fmt.Errorf("invalid app name: %q", name)
	}
	repo := RepoPath(name)
	if _, err := os.Stat(filepath.Join(repo, "HEAD")); err != nil {
		if err := os.MkdirAll(repo, 0o700); err != nil {
			return err
		}
		if out, err := exec.Command("git", "init", "--bare", repo).CombinedOutput(); err != nil {
			return fmt.Errorf("git init --bare: %v: %s", err, out)
		}
	}
	hook := filepath.Join(repo, "hooks", "post-receive")
	if err := os.MkdirAll(filepath.Dir(hook), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(hook, []byte(fmt.Sprintf(hookScript, name)), 0o755); err != nil {
		return fmt.Errorf("write hook: %w", err)
	}
	return os.MkdirAll(WorkDir(name, ""), 0o700)
}

// Deprovision removes the bare repo, the work-tree and the production stack.
// Previews must be brought down first (Destroy in the store does the whole set).
func Deprovision(ctx context.Context, name string) error {
	_ = ComposeDown(ctx, ComposeProject(name, ""), WorkDir(name, ""), "docker-compose.yml", io.Discard)
	_ = os.RemoveAll(RepoPath(name))
	_ = os.RemoveAll(WorkDir(name, ""))
	return nil
}

// Spec describes a deploy request. An empty Ref/Commit is resolved from the
// app's production Branch.
type Spec struct {
	App      string
	Ref      string // refs/heads/<branch>
	Commit   string // sha; empty → resolved from the ref
	Preview  string // preview slug; empty → production
	Trigger  string // push|ui|rollback
	DeployID string // opcional; gerado se vazio
}

// NewID generates a deploy id (the UI passes it along to follow the right log).
func NewID() string { return newDeployID() }

func newDeployID() string {
	var b [3]byte
	_, _ = rand.Read(b[:])
	return "d" + time.Now().UTC().Format("20060102-150405") + "-" + hex.EncodeToString(b[:])
}

// gitDir runs git against the app's bare repo.
func gitDir(ctx context.Context, repo string, args ...string) (string, error) {
	full := append([]string{"--git-dir=" + repo}, args...)
	out, err := exec.CommandContext(ctx, "git", full...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// Deploy is the shared core (both the hook and the queue runner call it). It
// writes progress to logW (streamed live) AND to a log file the UI tails.
// Returns the final DeployRecord (Status running|failed).
func Deploy(ctx context.Context, st *Store, spec Spec, logW io.Writer) (DeployRecord, error) {
	app, ok, err := st.Get(spec.App)
	if err != nil {
		return DeployRecord{}, err
	}
	if !ok {
		return DeployRecord{}, fmt.Errorf("app %q does not exist", spec.App)
	}
	branch := app.Branch
	if branch == "" {
		branch = "main"
	}
	composeFile := app.ComposeFile
	if composeFile == "" {
		composeFile = "docker-compose.yml"
	}
	ref := spec.Ref
	if ref == "" {
		ref = "refs/heads/" + branch
	}
	repo := RepoPath(app.Name)
	commit := spec.Commit
	if commit == "" {
		commit, err = gitDir(ctx, repo, "rev-parse", ref)
		if err != nil {
			return DeployRecord{}, fmt.Errorf("resolve %s: %w", ref, err)
		}
	}
	id := spec.DeployID
	if id == "" {
		id = newDeployID()
	}
	project := ComposeProject(app.Name, spec.Preview)
	workdir := WorkDir(app.Name, spec.Preview)

	// tee: logW (live) + the persisted file the UI streams.
	logPath := st.LogPath(app.Name, id)
	_ = os.MkdirAll(filepath.Dir(logPath), 0o700)
	lf, _ := os.Create(logPath)
	var w io.Writer = logW
	if lf != nil {
		defer lf.Close()
		w = io.MultiWriter(logW, lf)
	}

	subject, _ := gitDir(ctx, repo, "log", "-1", "--format=%s", commit)
	rec := DeployRecord{
		ID: id, Ref: ref, Commit: commit, Preview: spec.Preview, Project: project,
		Status: "building", Started: time.Now().Unix(), Message: subject,
	}
	_ = st.AppendDeploy(app.Name, rec)

	fmt.Fprintf(w, "\n\033[1m▸ vps-manager deploy\033[0m  app=%s  ref=%s  commit=%s\n", app.Name, ref, short(commit))
	if spec.Preview != "" {
		fmt.Fprintf(w, "  preview env: %s  (project %s)\n", spec.Preview, project)
	}
	if subject != "" {
		fmt.Fprintf(w, "  %s\n", subject)
	}

	fail := func(step string, e error) (DeployRecord, error) {
		fmt.Fprintf(w, "\n\033[31m✗ failed at %s: %v\033[0m\n", step, e)
		rec.Status = "failed"
		rec.Error = fmt.Sprintf("%s: %v", step, e)
		rec.Finished = time.Now().Unix()
		_ = st.AppendDeploy(app.Name, rec)
		return rec, fmt.Errorf("%s: %w", step, e)
	}

	// 1) Materialise the commit's tree in the work-tree (forced checkout).
	fmt.Fprintf(w, "\n→ checkout %s → %s\n", short(commit), workdir)
	if err := os.MkdirAll(workdir, 0o700); err != nil {
		return fail("mkdir work-tree", err)
	}
	co := exec.CommandContext(ctx, "git", "-c", "advice.detachedHead=false",
		"--git-dir="+repo, "--work-tree="+workdir, "checkout", "-qf", commit)
	co.Stdout, co.Stderr = w, w
	if err := co.Run(); err != nil {
		return fail("git checkout", err)
	}

	// 2) Write .env (production env; a preview merges PreviewEnv + its own PORT).
	env := map[string]string{}
	for k, v := range app.Env {
		env[k] = v
	}
	if spec.Preview != "" {
		for k, v := range app.PreviewEnv {
			env[k] = v
		}
	}
	// PORT: the PaaS contract — the app listens on $PORT. Production uses
	// app.Port; a preview gets a free port allocated (avoids stack collisions).
	port := app.Port
	if spec.Preview != "" {
		port = previewPort(app.Name, spec.Preview)
	}
	if port > 0 {
		if _, set := env["PORT"]; !set {
			env["PORT"] = strconv.Itoa(port)
		}
	}
	if err := writeDotEnv(filepath.Join(workdir, ".env"), env); err != nil {
		return fail("write .env", err)
	}
	fmt.Fprintf(w, "→ .env written (%d vars)\n", len(env))

	// 3) compose up --build.
	composePath := filepath.Join(workdir, composeFile)
	if _, err := os.Stat(composePath); err != nil {
		return fail("compose file", fmt.Errorf("%s not found in the repo", composeFile))
	}
	fmt.Fprintf(w, "\n→ docker compose up -d --build (project %s)\n", project)
	up := exec.CommandContext(ctx, "docker", "compose", "-p", project, "-f", composePath,
		"up", "-d", "--build", "--remove-orphans")
	up.Dir = workdir
	up.Stdout, up.Stderr = w, w
	if err := up.Run(); err != nil {
		return fail("docker compose up", err)
	}

	// 4) nginx vhost (best-effort; only when the domain is known). A failure
	//    here does NOT fail the deploy — the container is already up.
	if dom := deployDomain(app, spec.Preview); dom != "" && port > 0 {
		if err := writeVhost(ctx, project, dom, port, w); err != nil {
			fmt.Fprintf(w, "\033[33m! nginx not configured: %v\033[0m\n", err)
		} else {
			fmt.Fprintf(w, "→ nginx: https://%s → 127.0.0.1:%d\n", dom, port)
		}
	}

	rec.Status = "running"
	rec.Finished = time.Now().Unix()
	_ = st.AppendDeploy(app.Name, rec)
	fmt.Fprintf(w, "\n\033[32m✓ deploy ready\033[0m  %s  (%ds)\n", project, rec.Finished-rec.Started)
	return rec, nil
}

// deployDomain resolves the vhost's server_name: production uses app.Domain;
// a preview uses <slug>.<app.Domain> (only if the app has a base domain).
func deployDomain(app App, preview string) string {
	if app.Domain == "" {
		return ""
	}
	if preview == "" {
		return app.Domain
	}
	return preview + "." + app.Domain
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// writeDotEnv writes KEY=VALUE (one per line), skipping keys whose value
// contains a newline (injection). Sorted for a stable diff.
func writeDotEnv(path string, env map[string]string) error {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("# generated by vps-manager — do not edit (use the app's env UI)\n")
	for _, k := range keys {
		v := env[k]
		if strings.ContainsAny(v, "\n\r") {
			continue
		}
		fmt.Fprintf(&b, "%s=%s\n", k, v)
	}
	return os.WriteFile(path, []byte(b.String()), 0o600)
}

// ParseHookStdin reads the post-receive lines (<old> <new> <ref>) and returns
// the updates. Deletions (new=zero) come back with Delete=true — the hook uses
// that to bring down the preview of the branch that was removed.
type HookRef struct {
	Ref    string
	Commit string
	Delete bool
}

func ParseHookStdin(r io.Reader) []HookRef {
	var out []HookRef
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) != 3 {
			continue
		}
		newRev, ref := f[1], f[2]
		if strings.Trim(newRev, "0") == "" { // branch deletion
			out = append(out, HookRef{Ref: ref, Delete: true})
			continue
		}
		out = append(out, HookRef{Ref: ref, Commit: newRev})
	}
	return out
}
