package pty

import "testing"

// TestValidatedModel proves the layer-3 anti-injection guard: "" passes
// through as "" (inherits the default), a model from the allowlist passes, and anything outside
// it (including an attempted shell/flag injection) returns an error instead of being
// concatenated into the command the shell word-splits.
func TestValidatedModel(t *testing.T) {
	// aceitos
	for _, in := range []string{"", "  ", "haiku", "sonnet", "opus"} {
		if _, err := validatedModel(in); err != nil {
			t.Errorf("validatedModel(%q) unexpected error: %v", in, err)
		}
	}
	// "" (and whitespace) resolves to "" = no --model
	if got, _ := validatedModel("  "); got != "" {
		t.Errorf("validatedModel(spaces) = %q, want \"\"", got)
	}
	if got, _ := validatedModel("sonnet"); got != "sonnet" {
		t.Errorf("validatedModel(sonnet) = %q, want sonnet", got)
	}
	// rejected (injection / outside the allowlist)
	for _, in := range []string{"opus; rm -rf /", "haiku --dangerously-skip", "$(whoami)", "gpt-4"} {
		if _, err := validatedModel(in); err == nil {
			t.Errorf("validatedModel(%q) should fail (outside the allowlist)", in)
		}
	}
}
