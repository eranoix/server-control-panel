package pty

import "testing"

func TestValidatedModel(t *testing.T) {
	for _, in := range []string{"", "  ", "haiku", "sonnet", "opus"} {
		if _, err := validatedModel(in); err != nil {
			t.Errorf("validatedModel(%q) unexpected error: %v", in, err)
		}
	}
	if got, _ := validatedModel("  "); got != "" {
		t.Errorf("validatedModel(spaces) = %q, want \"\"", got)
	}
	if got, _ := validatedModel("sonnet"); got != "sonnet" {
		t.Errorf("validatedModel(sonnet) = %q, want sonnet", got)
	}
	for _, in := range []string{"opus; rm -rf /", "haiku --dangerously-skip", "$(whoami)", "gpt-4"} {
		if _, err := validatedModel(in); err == nil {
			t.Errorf("validatedModel(%q) should fail (outside the allowlist)", in)
		}
	}
}
