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

const DeployCommandEnv = "PANEL_DEPLOY_COMMAND"

type SelfDeployRunner struct{}

func (SelfDeployRunner) Kind() string { return "self_deploy" }

func (SelfDeployRunner) AuthorizedFor(_ string, isPrimary bool) bool { return isPrimary }

func (SelfDeployRunner) Run(ctx context.Context, _ json.RawMessage, logW io.Writer, progress func(int), step func(string)) error {
	command := strings.TrimSpace(os.Getenv(DeployCommandEnv))
	if command == "" {
		return fmt.Errorf("self deploy is not configured: set %s to the command that builds and deploys this server", DeployCommandEnv)
	}
	step("deploy")
	tail := newTailBuffer(40)
	out := io.MultiWriter(logW, tail)
	fmt.Fprintln(out, "$ "+command)
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	if err := streamCommand(ctx, cmd, out, progress); err != nil {
		return fmt.Errorf("deploy command: %w\n--- last lines ---\n%s", err, tail.String())
	}
	return nil
}

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
