package api

import (
	"os"
	"path/filepath"
	"testing"
)

func TestClaudeProjectDirSlug(t *testing.T) {
	cases := map[string]string{
		"/root":                                "-root",
		"/opt/panel/.claude/worktrees/vpsm-47": "-opt-panel--claude-worktrees-vpsm-47",
		"/root/projetos/acme-booking":          "-root-projetos-acme-booking",
	}
	for in, want := range cases {
		if got := claudeProjectDirSlug(in); got != want {
			t.Errorf("slug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestClaudeProjectHasMessages(t *testing.T) {
	cfg := t.TempDir()
	cwd := "/tmp/projeto-x"
	dir := filepath.Join(cfg, "projects", claudeProjectDirSlug(cwd))

	// 1. a missing folder = the session never received a message -> self-heal may act
	if claudeProjectHasMessages(cfg, cwd) {
		t.Fatal("no project folder should be false")
	}

	// 2. pasta vazia idem
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if claudeProjectHasMessages(cfg, cwd) {
		t.Fatal("empty folder should be false")
	}

	// 3. a transcript with no user message (metadata only) is still false —
	// that is exactly the bug: Claude came up but nothing ever reached it.
	meta := filepath.Join(dir, "a.jsonl")
	if err := os.WriteFile(meta, []byte(`{"type":"summary","x":1}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if claudeProjectHasMessages(cfg, cwd) {
		t.Fatal("transcript without user message should be false")
	}

	// 4. with a user message -> there is work, do NOT kill the session
	if err := os.WriteFile(meta, []byte(`{"type":"user","message":{"content":"oi"}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !claudeProjectHasMessages(cfg, cwd) {
		t.Fatal("transcript with user message should be true")
	}

	// 5. empty cwd -> true (when in doubt, leave the session alone)
	if !claudeProjectHasMessages(cfg, "") {
		t.Fatal("empty cwd should be true (conservative)")
	}
}
