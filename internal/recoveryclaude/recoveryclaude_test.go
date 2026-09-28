package recoveryclaude

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

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

	for _, name := range []string{"Dockerfile", "entrypoint.sh", "welcome.sh", "manage.sh"} {
		if _, err := os.Stat(filepath.Join(dir, "recovery-claude", name)); err != nil {
			t.Errorf("%s was not materialized: %v", name, err)
		}
	}
}

func TestEmbeddedPayloadDoesNotSetBaseURL(t *testing.T) {
	dir := t.TempDir()
	if _, err := Materialize(dir); err != nil {
		t.Fatalf("materialize: %v", err)
	}
	injected := []string{
		`ENV ANTHROPIC_BASE_URL`,
		`export ANTHROPIC_BASE_URL`,
		`-e ANTHROPIC_BASE_URL`,
		`"ANTHROPIC_BASE_URL"`,
		`ANTHROPIC_BASE_URL=http`,
	}
	for _, name := range []string{"Dockerfile", "entrypoint.sh", "manage.sh"} {
		b, err := os.ReadFile(filepath.Join(dir, "recovery-claude", name))
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		for _, line := range strings.Split(string(b), "\n") {
			cut := strings.TrimSpace(line)
			if strings.HasPrefix(cut, "#") {
				continue
			}
			for _, fallback := range injected {
				if strings.Contains(cut, fallback) {
					t.Errorf("embedded %s points Claude at a custom base URL (%s): %q", name, fallback, cut)
				}
			}
		}
	}
}

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
