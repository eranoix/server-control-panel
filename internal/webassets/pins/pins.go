// Package pinos runs the screen harnesses (scripts/test-*.mjs and *.sh) from
// inside `go test`.
//
// It exists as its OWN package for an operational reason, not an aesthetic one:
// the agentctl deploy gate charges by PACKAGE, not by test. While every screen
// pin lived in internal/webassets, bringing the package into the gate cost
// ~53 s — of which 33 s were a single browser test — and so the whole package
// was left out, leaving the paste pin without deploy coverage.
//
// With the runner here, an expensive pin can live in a subpackage without
// duplicating code, and the gate watches ./internal/webassets without the /... —
// cheap on deploy, complete under `go test ./...` and on pre-push.
package pins

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// repoRootDir walks up the tree until it finds the go.mod.
//
// The old pins assembled the path with filepath.Join("..", "..", …), which ties
// the runner to the DEPTH of its caller — moving a test one level down broke the
// path silently, and "script not found" would read as a pin failure instead of a
// refactoring mistake. Walking up to the go.mod works from any depth.
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
		pai := filepath.Dir(dir)
		if pai == dir {
			break
		}
		dir = pai
	}
	t.Fatalf("não achei a raiz do repo (go.mod) subindo a partir do dir de teste")
	return ""
}

// RunBash is Run for pins written in shell (not everything that needs
// asserting is JavaScript — the independence of the recovery container lives in
// a Dockerfile and a startup script).
func RunBash(t *testing.T, script string) {
	t.Helper()
	caminho := filepath.Join(repoRootDir(t), "scripts", script)
	output, err := exec.Command("bash", caminho).CombinedOutput()
	verify(t, script, string(output), err)
}

// Run executes a .mjs harness with node.
//
// A missing node is a FAILURE, not a skip. `make minify` already depends on
// node/esbuild, so the machine that builds this project has node; a silent skip
// would return the pin to its orphan state, now disguised as green.
func Run(t *testing.T, script string, env ...string) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("node não encontrado no PATH: os pinos de tela não podem rodar, e pular seria fingir cobertura (%v)", err)
	}
	cmd := exec.Command(node, filepath.Join(repoRootDir(t), "scripts", script))
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	output, err := cmd.CombinedOutput()
	verify(t, script, string(output), err)
}

// confere applies the same verdict to both runners, including the vacuity
// guard: a harness that prints no PASS may have exited 0 without running a
// single assertion (broken import, empty file, early return).
func verify(t *testing.T, script, text string, err error) {
	t.Helper()
	if err != nil {
		t.Errorf("%s reprovou:\n%s", script, text)
		return
	}
	if !strings.Contains(text, "PASS") {
		t.Errorf("%s saiu com código 0 mas não imprimiu PASS — provavelmente não asseverou nada:\n%s", script, text)
	}
}
