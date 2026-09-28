package pty

import (
	"strings"
	"testing"
)

func TestClaudeConfigEnvDeclaresTERM(t *testing.T) {
	for _, dir := range []string{"", "/srv/agent-accounts/sam"} {
		env := claudeConfigEnv(dir)
		if !hasEnv(env, "TERM=xterm-256color") {
			t.Fatalf("claudeConfigEnv(%q) without TERM: %v", dir, env)
		}
	}
}

func TestSessionTermMatchesTerminalClient(t *testing.T) {
	if sessionTerm != "xterm-256color" {
		t.Fatalf("sessionTerm diverged from the client's TERM (pty.go): %q", sessionTerm)
	}
}

func TestClaudeConfigEnvKeepsAccountAndPath(t *testing.T) {
	env := claudeConfigEnv("/srv/agent-accounts/sam")
	if !hasEnv(env, "CLAUDE_CONFIG_DIR=/srv/agent-accounts/sam") {
		t.Fatalf("CLAUDE_CONFIG_DIR is gone: %v", env)
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
