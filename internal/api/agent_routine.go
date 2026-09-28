package api

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"time"

	ptysvc "server-control-panel/internal/pty"
	"server-control-panel/internal/queue"
)

func (r *Router) runAgentRoutineJob(ctx context.Context, a queue.AgentRoutineArgs, logW io.Writer) (string, error) {
	repo := a.RepoPath()
	name := a.SessionName
	if name == "" {
		name = "panel-routine-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	name = ptysvc.SafeSessionName(name)

	fmt.Fprintf(logW, "agent routine → session %s (cwd=%s)\n", name, repo)
	created, err := ptysvc.SpawnClaudeWorkSession(name, repo, a.Prompt, r.jobsConfigDir(), a.Model)
	if err != nil {
		return "", fmt.Errorf("spawn routine: %w", err)
	}
	if r.agentCWD != nil {
		r.agentCWD.Put(created, repo)
	}
	if r.sessionOwn != nil && r.cfg != nil && r.cfg.Primary != "" {
		_ = r.sessionOwn.Claim(created, r.cfg.Primary)
	}
	fmt.Fprintf(logW, "✓ session %s is live — attach from the terminal to follow it\n", created)
	return created, nil
}
