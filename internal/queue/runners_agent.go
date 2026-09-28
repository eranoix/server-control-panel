package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

type AgentRoutineArgs struct {
	Prompt       string `json:"prompt"`
	Repo         string `json:"repo,omitempty"`
	WorktreePath string `json:"worktree_path,omitempty"`
	Model        string `json:"model,omitempty"`
	SessionName  string `json:"session_name,omitempty"`
}

func (a AgentRoutineArgs) RepoPath() string {
	if strings.TrimSpace(a.WorktreePath) != "" {
		return strings.TrimSpace(a.WorktreePath)
	}
	return strings.TrimSpace(a.Repo)
}

type AgentRoutineRunner struct {
	Spawn func(ctx context.Context, a AgentRoutineArgs, logW io.Writer) (string, error)
}

func (AgentRoutineRunner) Kind() string { return "agent_routine" }

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
