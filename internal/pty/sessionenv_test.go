package pty

import (
	"strings"
	"testing"
)

// The session env has to declare TERM. The spawners exec the CLI DIRECTLY (no
// shell), so /root/.bashrc — which is what fixes the empty TERM inherited from
// systemd in the ordinary terminals — never runs. Without TERM, Claude Code comes
// up MONOCHROME: that was the black-and-white pane of the Jira "Work on it now"
// button, while the ordinary terminal (born from `bash -l`) came up in colour.
func TestClaudeConfigEnvDeclaraTERM(t *testing.T) {
	for _, dir := range []string{"", "/srv/agent-accounts/sam"} {
		env := claudeConfigEnv(dir)
		if !hasEnv(env, "TERM=xterm-256color") {
			t.Fatalf("claudeConfigEnv(%q) without TERM: %v", dir, env)
		}
	}
}

// The session's TERM has to be the SAME one the ordinary terminal's client
// injects (pty.go), or the two paths diverge all over again.
func TestSessionTermCasaComOClienteDoTerminal(t *testing.T) {
	if sessionTerm != "xterm-256color" {
		t.Fatalf("sessionTerm diverged from the client's TERM (pty.go): %q", sessionTerm)
	}
}

// CLAUDE_CONFIG_DIR and PATH (~/.local/bin) are still standing.
func TestClaudeConfigEnvMantemContaEPath(t *testing.T) {
	env := claudeConfigEnv("/srv/agent-accounts/sam")
	if !hasEnv(env, "CLAUDE_CONFIG_DIR=/srv/agent-accounts/sam") {
		t.Fatalf("CLAUDE_CONFIG_DIR sumiu: %v", env)
	}
	if hasEnv(claudeConfigEnv(""), "CLAUDE_CONFIG_DIR=") {
		t.Fatalf("an empty dir must not pin an account")
	}
}

func hasEnv(env []string, prefix string) bool {
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			return true
		}
	}
	return false
}
