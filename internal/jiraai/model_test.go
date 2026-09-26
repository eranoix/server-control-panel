package jiraai

import (
	"reflect"
	"testing"
)

// TestClaudeArgsDefault proves that, with no model configured (nil or ""), the
// spawn stays byte-identical to the era before per-tier models: exactly
// `-p <prompt>`, with no --model.
func TestClaudeArgsDefault(t *testing.T) {
	base := []string{"-p", "PROMPT"}

	// nil resolver → inherits the default (Opus), no --model.
	r := &Runner{}
	if got := r.claudeArgs("-p", "PROMPT"); !reflect.DeepEqual(got, base) {
		t.Errorf("nil model: args = %v, want %v", got, base)
	}

	// a resolver returning "" → same thing (inherits the default).
	r = &Runner{model: func() string { return "" }}
	if got := r.claudeArgs("-p", "PROMPT"); !reflect.DeepEqual(got, base) {
		t.Errorf("empty model: args = %v, want %v", got, base)
	}
}

// TestClaudeArgsWithModel proves a configured model prefixes --model <m>.
func TestClaudeArgsWithModel(t *testing.T) {
	r := &Runner{model: func() string { return "sonnet" }}
	want := []string{"--model", "sonnet", "-p", "PROMPT"}
	if got := r.claudeArgs("-p", "PROMPT"); !reflect.DeepEqual(got, want) {
		t.Errorf("model=sonnet: args = %v, want %v", got, want)
	}
}
