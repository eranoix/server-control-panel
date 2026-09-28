package pins

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func repoRootDir(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("could not find the repo root (go.mod) walking up from the test dir")
	return ""
}

func RunBash(t *testing.T, script string) {
	t.Helper()
	path := filepath.Join(repoRootDir(t), "scripts", script)
	output, err := exec.Command("bash", path).CombinedOutput()
	verify(t, script, string(output), err)
}

func Run(t *testing.T, script string, env ...string) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("node not found in PATH: the screen pins cannot run, and skipping would fake coverage (%v)", err)
	}
	cmd := exec.Command(node, filepath.Join(repoRootDir(t), "scripts", script))
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	output, err := cmd.CombinedOutput()
	verify(t, script, string(output), err)
}

func verify(t *testing.T, script, text string, err error) {
	t.Helper()
	if err != nil {
		t.Errorf("%s failed:\n%s", script, text)
		return
	}
	if !strings.Contains(text, "PASS") {
		t.Errorf("%s exited with code 0 but printed no PASS, so it probably asserted nothing:\n%s", script, text)
	}
}
