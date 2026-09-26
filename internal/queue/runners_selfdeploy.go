package queue

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// SelfDeployRunner is the "self_deploy" job kind: it shells out to the
// existing `agentctl deploy` pipeline (gate -> converge canon -> check
// invariants -> build -> health-gated deploy w/ auto-rollback -> live
// invariant verify -> advance/propagate canon) so a deploy of vps-manager's
// own binary can be triggered remotely without reimplementing any of that
// safety machinery — this runner never touches scripts/deploy.sh directly
// and never re-derives the deploy steps itself.
//
// No fields: agentctl resolves its own repo root/coord paths (VPSM_ROOT env
// var, default /opt/panel) — there is nothing for the caller to inject.
//
// This is a completely separate job kind from "app_deploy"
// (AppDeployRunner, runners_deploy.go), which deploys OTHER PaaS-hosted apps
// through internal/deploy — self_deploy must never import or reference that
// package or runner.
type SelfDeployRunner struct{}

func (SelfDeployRunner) Kind() string { return "self_deploy" }

// AuthorizedFor: primary-only, the same RBAC tier as every other
// system-mutating runner (RebootRunner, AptUpgradeRunner, ...) — a deploy
// restarts the running binary.
func (SelfDeployRunner) AuthorizedFor(_ string, isPrimary bool) bool { return isPrimary }

func (SelfDeployRunner) Run(ctx context.Context, _ json.RawMessage, logW io.Writer, progress func(int), step func(string)) error {
	step("agentctl deploy")
	// tail keeps only the last few lines of combined output so a failure's
	// job.Error carries a useful summary without duplicating the entire
	// (already-streamed) log there.
	tail := newTailBuffer(40)
	out := io.MultiWriter(logW, tail)
	fmt.Fprintln(out, "$ agentctl deploy")
	cmd := exec.CommandContext(ctx, "agentctl", "deploy")
	if err := streamCommand(ctx, cmd, out, progress); err != nil {
		return fmt.Errorf("agentctl deploy: %w\n--- last lines ---\n%s", err, tail.String())
	}
	return nil
}

// tailBuffer is an io.Writer that retains only the last n lines written to
// it. Used to summarize a failed subprocess's output for a job's terminal
// Error field.
type tailBuffer struct {
	n     int
	lines []string
}

func newTailBuffer(n int) *tailBuffer { return &tailBuffer{n: n} }

func (t *tailBuffer) Write(p []byte) (int, error) {
	for _, line := range bytes.Split(bytes.TrimRight(p, "\n"), []byte("\n")) {
		t.lines = append(t.lines, string(line))
		if len(t.lines) > t.n {
			t.lines = t.lines[len(t.lines)-t.n:]
		}
	}
	return len(p), nil
}

func (t *tailBuffer) String() string { return strings.Join(t.lines, "\n") }
