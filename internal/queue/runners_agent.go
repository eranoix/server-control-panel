package queue

// runners_agent.go — scheduled agent routines (kind "agent_routine").
//
// On each fire it spawns a DETACHED Claude session in a given repo/worktree with
// a given prompt — e.g. a nightly "triage the new tickets" or "review the
// agents' work". The spawn itself (dtach create + prompt paste + the
// name→cwd/ownership bookkeeping) lives in the Router, injected here as a
// closure (same pattern as SessionBackupRunner) because Run() has no
// http.Request / Router context. The HTTP/scheduler layer fixes the owner and
// authz.
//
// Anti-injection: prompt/repo/model/session_name flow to the spawner as
// discrete values (argv-exec), never shell-concatenated.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// AgentRoutineArgs are the args of an agent_routine schedule.
type AgentRoutineArgs struct {
	Prompt string `json:"prompt"` // what the agent should do this run
	// Repo/WorktreePath: where the agent works. Either key is accepted;
	// WorktreePath wins when both are set. Must be an absolute path.
	Repo         string `json:"repo,omitempty"`
	WorktreePath string `json:"worktree_path,omitempty"`
	Model        string `json:"model,omitempty"`        // "" = inherit process default
	SessionName  string `json:"session_name,omitempty"` // "" = auto-generated
}

// RepoPath returns the effective working directory (worktree_path preferred).
func (a AgentRoutineArgs) RepoPath() string {
	if strings.TrimSpace(a.WorktreePath) != "" {
		return strings.TrimSpace(a.WorktreePath)
	}
	return strings.TrimSpace(a.Repo)
}

// AgentRoutineRunner spawns a detached Claude session per fire. Spawn is injected
// by the api layer (r.runAgentRoutineJob): it performs the actual spawn, records
// the name→cwd mapping and ownership, and writes a human summary to logW.
type AgentRoutineRunner struct {
	// Spawn spawns the routine's session and returns the session name. logW gets
	// a readable line for the job transcript. nil => runner degrades to an error.
	Spawn func(ctx context.Context, a AgentRoutineArgs, logW io.Writer) (string, error)
}

func (AgentRoutineRunner) Kind() string { return "agent_routine" }

// AuthorizedFor: PRIMARY-ONLY. A routine spawns a root-privileged Claude session
// (same posture as shell/host-wide runners) — never open to non-primary users.
func (AgentRoutineRunner) AuthorizedFor(_ string, isPrimary bool) bool { return isPrimary }

func (r AgentRoutineRunner) Run(ctx context.Context, args json.RawMessage, logW io.Writer, progress func(int), step func(string)) error {
	var a AgentRoutineArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return fmt.Errorf("args: %w", err)
	}
	if strings.TrimSpace(a.Prompt) == "" {
		return errors.New("prompt missing from the schedule")
	}
	if repo := a.RepoPath(); repo == "" || !strings.HasPrefix(repo, "/") || strings.Contains(repo, "..") {
		return errors.New("repo/worktree_path must be an absolute path free of ..")
	}
	if r.Spawn == nil {
		return errors.New("routine spawn unavailable")
	}
	step("starting agent routine")
	progress(10)
	name, err := r.Spawn(ctx, a, logW)
	if err != nil {
		return err
	}
	progress(100)
	step("session " + name + " started")
	return nil
}
