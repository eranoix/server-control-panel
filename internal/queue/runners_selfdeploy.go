package queue

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// DeployCommandEnv names the environment variable holding the command a
// self_deploy job runs, through `sh -c`, as the user the server runs as.
// Whatever it points at owns the whole pipeline (build, health gate,
// rollback); this runner only starts it and streams its output.
const DeployCommandEnv = "PANEL_DEPLOY_COMMAND"

// SelfDeployRunner is the "self_deploy" job kind: it redeploys this server's
// own binary by running the operator's deploy command (see DeployCommandEnv),
// so a deploy can be triggered from the mobile app without reimplementing any
// of that command's safety checks here. With the variable unset the job fails
// with an explanation instead of guessing a command.
//
// This is a completely separate job kind from "app_deploy"
// (AppDeployRunner, runners_deploy.go), which deploys OTHER PaaS-hosted apps
// through internal/deploy; self_deploy must never import or reference that
// package or runner.
type SelfDeployRunner struct{}

func (SelfDeployRunner) Kind() string { return "self_deploy" }

// AuthorizedFor: primary-only, the same RBAC tier as every other
// system-mutating runner (RebootRunner, AptUpgradeRunner, ...), because a
// deploy restarts the running binary.
func (SelfDeployRunner) AuthorizedFor(_ string, isPrimary bool) bool { return isPrimary }

func (SelfDeployRunner) Run(ctx context.Context, _ json.RawMessage, logW io.Writer, progress func(int), step func(string)) error {
	command := strings.TrimSpace(os.Getenv(DeployCommandEnv))
	if command == "" {
		return fmt.Errorf("self deploy is not configured: set %s to the command that builds and deploys this server", DeployCommandEnv)
	}
	step("deploy")
	// Only the last lines go into job.Error: the full output is already in
	// the streamed log, and the error should read as a summary.
	tail := newTailBuffer(40)
	out := io.MultiWriter(logW, tail)
	fmt.Fprintln(out, "$ "+command)
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	if err := streamCommand(ctx, cmd, out, progress); err != nil {
		return fmt.Errorf("deploy command: %w\n--- last lines ---\n%s", err, tail.String())
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
