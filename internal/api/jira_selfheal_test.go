package api

import (
	"os"
	"path/filepath"
	"testing"
)

func TestClaudeProjectDirSlug(t *testing.T) {
	cases := map[string]string{
		"/root":                                 "-root",
		"/opt/panel/.claude/worktrees/panel-47": "-opt-panel--claude-worktrees-panel-47",
		"/root/projects/acme-booking":           "-root-projects-acme-booking",
	}
	for in, want := range cases {
		if got := claudeProjectDirSlug(in); got != want {
			t.Errorf("slug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestClaudeProjectHasMessages(t *testing.T) {
	cfg := t.TempDir()
	cwd := "/tmp/project-x"
	dir := filepath.Join(cfg, "projects", claudeProjectDirSlug(cwd))

	if claudeProjectHasMessages(cfg, cwd) {
		t.Fatal("no project folder should be false")
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if claudeProjectHasMessages(cfg, cwd) {
		t.Fatal("empty folder should be false")
	}

	meta := filepath.Join(dir, "a.jsonl")
	if err := os.WriteFile(meta, []byte(`{"type":"summary","x":1}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if claudeProjectHasMessages(cfg, cwd) {
		t.Fatal("transcript without user message should be false")
	}

	if err := os.WriteFile(meta, []byte(`{"type":"user","message":{"content":"oi"}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !claudeProjectHasMessages(cfg, cwd) {
		t.Fatal("transcript with user message should be true")
	}

	if !claudeProjectHasMessages(cfg, "") {
		t.Fatal("empty cwd should be true (conservative)")
	}
}
