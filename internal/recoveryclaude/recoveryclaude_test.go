package recoveryclaude

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The path the "Start the container" button walks: materialize the embedded
// assets and run the manager from there, exercising the real path.
func TestMaterializeDeliversExecutableManager(t *testing.T) {
	dir := t.TempDir()
	script, err := Materialize(dir)
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}

	info, err := os.Stat(script)
	if err != nil {
		t.Fatalf("the manager was not written: %v", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("the manager has no execute bit (%v) — the exec would fail", info.Mode().Perm())
	}

	// Docker's build context is the materialized directory, so the Dockerfile
	// and the scripts it copies have to come out TOGETHER. With one missing, the
	// `build` would break only when it mattered.
	for _, name := range []string{"Dockerfile", "entrypoint.sh", "welcome.sh", "manage.sh"} {
		if _, err := os.Stat(filepath.Join(dir, "recovery-claude", name)); err != nil {
			t.Errorf("%s was not materialized: %v", name, err)
		}
	}
}

// The container's independence is an ABSENCE (there is no ANTHROPIC_BASE_URL),
// and absences disappear without anyone noticing. Here it is asserted over the
// content the BINARY carries — not over the repository file, which may diverge
// from what was actually embedded.
func TestEmbeddedPayloadDoesNotSetBaseURL(t *testing.T) {
	dir := t.TempDir()
	if _, err := Materialize(dir); err != nil {
		t.Fatalf("materialize: %v", err)
	}
	// Only DEFINING/INJECTING the variable breaks the independence. Mentioning
	// it is fine: the manager's own `doctor` cites it to verify it is absent.
	injected := []string{
		`ENV ANTHROPIC_BASE_URL`,    // Dockerfile
		`export ANTHROPIC_BASE_URL`, // shell
		`-e ANTHROPIC_BASE_URL`,     // docker run
		`"ANTHROPIC_BASE_URL"`,      // generated settings.json
		`ANTHROPIC_BASE_URL=http`,   // direct assignment
	}
	for _, name := range []string{"Dockerfile", "entrypoint.sh", "manage.sh"} {
		b, err := os.ReadFile(filepath.Join(dir, "recovery-claude", name))
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		for _, line := range strings.Split(string(b), "\n") {
			cut := strings.TrimSpace(line)
			if strings.HasPrefix(cut, "#") {
				continue // a comment explaining the absence is welcome
			}
			for _, fallback := range injected {
				if strings.Contains(cut, fallback) {
					t.Errorf("embedded %s points Claude at a custom base URL (%s): %q", name, fallback, cut)
				}
			}
		}
	}
}

// Always rewriting (instead of skipping when it already exists) is what stops
// an old deploy's version from surviving on disk after a fix.
func TestMaterializeOverwritesOldVersion(t *testing.T) {
	dir := t.TempDir()
	if _, err := Materialize(dir); err != nil {
		t.Fatalf("materialize: %v", err)
	}
	target := filepath.Join(dir, "recovery-claude", "manage.sh")
	if err := os.WriteFile(target, []byte("#!/bin/sh\necho old version\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Materialize(dir); err != nil {
		t.Fatalf("materialize again: %v", err)
	}
	b, _ := os.ReadFile(target)
	if strings.Contains(string(b), "old version") {
		t.Error("the old manager survived — a corrected deploy would not reach the disk")
	}
}

// A real execution: the materialized manager has to RUN and talk to Docker.
// Without Docker (CI) it skips — faking coverage would be worse than none.
func TestMaterializedManagerActuallyRuns(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker missing in this environment")
	}
	cmd, err := Command(t.TempDir(), "status")
	if err != nil {
		t.Fatalf("command: %v", err)
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the materialized manager did not run: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "state:") {
		t.Errorf("unexpected output from the manager:\n%s", output)
	}
}
