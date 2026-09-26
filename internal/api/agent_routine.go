package api

// agent_routine.go — VPSM Wave-3 #56: the Router-side spawn closure injected
// into queue.AgentRoutineRunner. Kept next to the other agent-ops glue.
//
// It reuses the shared work-session spawn path (ptysvc.SpawnClaudeWorkSession),
// then records the name→cwd mapping (so the cost aggregator + hook resolve the
// session) and claims ownership for the primary. agent_routine is primary-only,
// so the spawned root session is always owned by the primary.

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"time"

	ptysvc "server-control-panel/internal/pty"
	"server-control-panel/internal/queue"
)

// runAgentRoutineJob spawns one scheduled routine's Claude session. Injected as
// queue.AgentRoutineRunner.Spawn. Returns the session name.
func (r *Router) runAgentRoutineJob(ctx context.Context, a queue.AgentRoutineArgs, logW io.Writer) (string, error) {
	repo := a.RepoPath()
	name := a.SessionName
	if name == "" {
		// Deterministic-ish, collision-resistant default: vpsm-routine-<unixnano>.
		name = "vpsm-routine-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	name = ptysvc.SafeSessionName(name)

	fmt.Fprintf(logW, "agent routine → session %s (cwd=%s)\n", name, repo)
	created, err := ptysvc.SpawnClaudeWorkSession(name, repo, a.Prompt, r.jobsConfigDir(), a.Model)
	if err != nil {
		return "", fmt.Errorf("spawn routine: %w", err)
	}
	// Bookkeeping so telemetry (cost aggregator) + the CC hook can resolve it.
	if r.agentCWD != nil {
		r.agentCWD.Put(created, repo)
	}
	if r.sessionOwn != nil && r.cfg != nil && r.cfg.Primary != "" {
		_ = r.sessionOwn.Claim(created, r.cfg.Primary)
	}
	fmt.Fprintf(logW, "✓ session %s is live — attach from the terminal to follow it\n", created)
	return created, nil
}
