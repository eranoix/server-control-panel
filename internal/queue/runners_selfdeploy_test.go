package queue

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// useStubDeployCommand points DeployCommandEnv at a script that prints
// three deterministic lines and exits with exitCode, so no test here ever
// runs a real deploy.
func useStubDeployCommand(t *testing.T, exitCode int) {
	t.Helper()
	script := "#!/bin/sh\necho stub-line-1\necho stub-line-2\necho stub-line-3\nexit " + strconv.Itoa(exitCode) + "\n"
	path := filepath.Join(t.TempDir(), "deploy")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(DeployCommandEnv, path)
}

func TestSelfDeployRunnerSuccess(t *testing.T) {
	useStubDeployCommand(t, 0)

	q := newTmpQueue(t)
	q.Register(SelfDeployRunner{})

	j, err := q.Enqueue("self_deploy", nil, "sam", "mobile")
	if err != nil {
		t.Fatal(err)
	}
	final := waitFor(t, q, j.ID, StatusDone)
	if final.Error != "" {
		t.Errorf("unexpected error on success: %s", final.Error)
	}

	logBytes, err := q.ReadLog(j.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	logStr := string(logBytes)
	i1 := strings.Index(logStr, "stub-line-1")
	i2 := strings.Index(logStr, "stub-line-2")
	i3 := strings.Index(logStr, "stub-line-3")
	if i1 < 0 || i2 < 0 || i3 < 0 || !(i1 < i2 && i2 < i3) {
		t.Errorf("expected stub-line-1/2/3 in order in log, got:\n%s", logStr)
	}
}

func TestSelfDeployRunnerFailure(t *testing.T) {
	useStubDeployCommand(t, 1)

	q := newTmpQueue(t)
	q.Register(SelfDeployRunner{})

	j, err := q.Enqueue("self_deploy", nil, "sam", "mobile")
	if err != nil {
		t.Fatal(err)
	}
	final := waitFor(t, q, j.ID, StatusFailed)
	if final.Error == "" {
		t.Error("expected non-empty Error on a failed self_deploy job")
	}
	if !strings.Contains(final.Error, "stub-line-3") {
		t.Errorf("expected failure summary to include the stub's last output, got: %s", final.Error)
	}

	logBytes, err := q.ReadLog(j.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logBytes), "stub-line-1") {
		t.Errorf("expected the full stub output still streamed to the log, got:\n%s", string(logBytes))
	}
}

func TestSelfDeployRunnerPrimaryOnly(t *testing.T) {
	r := SelfDeployRunner{}
	if r.AuthorizedFor("alice", false) {
		t.Error("self_deploy must be primary-only")
	}
	if !r.AuthorizedFor("alice", true) {
		t.Error("self_deploy should allow primary")
	}
}

func TestSelfDeployRunnerUnconfigured(t *testing.T) {
	t.Setenv(DeployCommandEnv, "")

	q := newTmpQueue(t)
	q.Register(SelfDeployRunner{})

	j, err := q.Enqueue("self_deploy", nil, "sam", "mobile")
	if err != nil {
		t.Fatal(err)
	}
	final := waitFor(t, q, j.ID, StatusFailed)
	if !strings.Contains(final.Error, DeployCommandEnv) {
		t.Errorf("the error should name %s, got: %s", DeployCommandEnv, final.Error)
	}
}
