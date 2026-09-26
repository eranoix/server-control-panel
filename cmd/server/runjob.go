package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"server-control-panel/internal/aimodel"
	"server-control-panel/internal/aiprompts"
	"server-control-panel/internal/claudeacct"
	"server-control-panel/internal/config"
	"server-control-panel/internal/jira"
	"server-control-panel/internal/jiraai"
	"server-control-panel/internal/queue"
	"server-control-panel/internal/scope"
	"server-control-panel/internal/secrets"
)

// runDetachedJob is the entrypoint for `vps-manager run-job <id>`. It runs a
// single queued job OUT OF PROCESS — launched by the main vps-manager into its
// own systemd scope so a deploy/restart can't kill it. It reconstructs the
// request-independent runner wiring (per-owner Jira client + repo map +
// prompt registry, all derived from scope.New(user)+vault — zero HTTP), runs
// the job, and reports status ONLY through the per-job detached file. It never
// touches state.json — that strict writer split is what makes the two live
// processes race-free.
//
// Currently only jira_ai_analysis is launched detached, but the dispatch is
// kind-generic: any registered runner could run here.
func runDetachedJob(id string) error {
	if id == "" {
		return errors.New("run-job: empty job id")
	}
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	queueRoot := filepath.Join(cfg.DataDir, "queue")

	job, err := readJobFromState(queueRoot, id)
	if err != nil {
		return err
	}

	// Cancel on SIGTERM/SIGINT (e.g. `systemctl stop <scope>`), so the runner
	// can unwind and we can record an honest terminal status.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	// Open the job's log file (same LogPath the main process recorded). Fresh
	// truncate — this is the run of record for this id.
	logPath := job.LogPath
	if logPath == "" {
		logPath = filepath.Join(queueRoot, "runs", id+".log")
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		return fmt.Errorf("mkdir log dir: %w", err)
	}
	logF, err := os.Create(logPath)
	if err != nil {
		return fmt.Errorf("create log: %w", err)
	}
	defer logF.Close()

	runner, err := buildDetachedRunner(cfg, job.Kind)
	if err != nil {
		// Record the failure so the main process surfaces it instead of
		// eventually marking the job interrupted.
		_ = queue.WriteDetachedStatus(queueRoot, id, queue.DetachedStatus{
			Status: queue.StatusFailed, Error: err.Error(),
			Started: time.Now().Unix(), Finished: time.Now().Unix(),
		})
		return err
	}

	started := time.Now().Unix()
	cur := queue.DetachedStatus{Status: queue.StatusRunning, Started: started}
	write := func() { _ = queue.WriteDetachedStatus(queueRoot, id, cur) }
	write() // announce running immediately

	progress := func(p int) {
		if p < 0 {
			p = 0
		}
		if p > 100 {
			p = 100
		}
		cur.Progress = p
		write()
	}
	step := func(s string) {
		cur.Step = s
		write()
	}

	runErr := runner.Run(ctx, job.Args, logF, progress, step)

	cur.Finished = time.Now().Unix()
	switch {
	case ctx.Err() != nil:
		// Asked to stop (scope stopped). Honest interrupted, re-runnable.
		cur.Status = queue.StatusInterrupted
		cur.Error = "interrupted: detached job stopped"
	case runErr != nil:
		cur.Status = queue.StatusFailed
		cur.Error = runErr.Error()
		fmt.Fprintf(logF, "\n--- FAILED: %v ---\n", runErr)
	default:
		cur.Status = queue.StatusDone
		cur.Progress = 100
	}
	write()
	return runErr
}

// readJobFromState reads one job record out of state.json without spinning up
// a full Queue (which would start workers + reconcile). Read-only.
func readJobFromState(queueRoot, id string) (*queue.Job, error) {
	data, err := os.ReadFile(filepath.Join(queueRoot, "state.json"))
	if err != nil {
		return nil, fmt.Errorf("read state.json: %w", err)
	}
	var stored struct {
		Jobs []queue.Job `json:"jobs"`
	}
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil, fmt.Errorf("parse state.json: %w", err)
	}
	for i := range stored.Jobs {
		if stored.Jobs[i].ID == id {
			return &stored.Jobs[i], nil
		}
	}
	return nil, fmt.Errorf("run-job: job %s not found in state.json", id)
}

// buildDetachedRunner reconstructs the runner for a kind in a standalone
// process. Mirrors the Router's wiring (api.go NewRunner) but without any HTTP
// dependency — the factories derive everything from scope.New(user)+vault.
func buildDetachedRunner(cfg *config.Config, kind string) (queue.Runner, error) {
	switch kind {
	case "jira_ai_analysis":
		vault, err := secrets.Open(filepath.Join(cfg.DataDir, "secrets.vault"), cfg.JWTSecret)
		if err != nil {
			return nil, fmt.Errorf("open vault: %w", err)
		}
		clientFor := func(user string) (*jira.Client, error) {
			if user == "" {
				return nil, errors.New("unauthorized")
			}
			u, err := scope.New(user)
			if err != nil {
				return nil, err
			}
			uv := scope.NewUserVault(vault, u)
			site, _ := uv.Get("jira_site")
			email, _ := uv.Get("jira_email")
			token, _ := uv.Get("jira_token")
			if site == "" || email == "" || token == "" {
				return nil, jira.ErrNotConfigured
			}
			return jira.New(site, email, token)
		}
		repoMapFor := func(owner string) map[string]string {
			u, err := scope.New(owner)
			if err != nil {
				return nil
			}
			raw, _ := scope.NewUserVault(vault, u).Get("jira_project_repos")
			if raw == "" {
				return nil
			}
			var m map[string]string
			if err := json.Unmarshal([]byte(raw), &m); err != nil {
				return nil
			}
			return m
		}
		// The detached job has to run on the SAME account assigned to the "jobs"
		// consumer. Without this, a job assigned to Sam would fall back to the
		// default account (jordan) when run detached (the primary path). A failing
		// Open → nil resolver → inherits the default, no regression.
		var jobsDir func() string
		if cas, err := claudeacct.Open(cfg.DataDir, cfg.ClaudeHome); err == nil {
			jobsDir = func() string { return cas.ConfigDirFor(claudeacct.ConsumerJobs) }
		}
		// Same model tier as the in-process path. cfg is read from disk when
		// runjob starts, so an edit made in the UI (persisted into config.json)
		// applies to the next detached job; the env overrides it.
		jobsModel := func() string { return aimodel.For(aimodel.JiraAI, cfg.AIModels.JiraAI) }
		return jiraai.NewRunner(clientFor, repoMapFor, aiprompts.New(cfg.DataDir), jobsDir, jobsModel), nil
	default:
		return nil, fmt.Errorf("run-job: kind %q not supported detached", kind)
	}
}
