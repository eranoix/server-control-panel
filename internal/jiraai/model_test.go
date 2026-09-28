package jiraai

import (
	"reflect"
	"testing"
)

func TestClaudeArgsDefault(t *testing.T) {
	base := []string{"-p", "PROMPT"}

	r := &Runner{}
	if got := r.claudeArgs("-p", "PROMPT"); !reflect.DeepEqual(got, base) {
		t.Errorf("nil model: args = %v, want %v", got, base)
	}

	r = &Runner{model: func() string { return "" }}
	if got := r.claudeArgs("-p", "PROMPT"); !reflect.DeepEqual(got, base) {
		t.Errorf("empty model: args = %v, want %v", got, base)
	}
}

func TestClaudeArgsWithModel(t *testing.T) {
	r := &Runner{model: func() string { return "sonnet" }}
	want := []string{"--model", "sonnet", "-p", "PROMPT"}
	if got := r.claudeArgs("-p", "PROMPT"); !reflect.DeepEqual(got, want) {
		t.Errorf("model=sonnet: args = %v, want %v", got, want)
	}
}
