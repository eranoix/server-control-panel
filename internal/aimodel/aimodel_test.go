package aimodel

import "testing"

// TestForPrecedence proves the order env > config > default, per tier.
func TestForPrecedence(t *testing.T) {
	// default: Suggest downgrades to haiku; the others inherit Opus ("").
	if got := For(Suggest, ""); got != "haiku" {
		t.Fatalf("default Suggest = %q, want haiku", got)
	}
	if got := For(JiraAI, ""); got != "" {
		t.Fatalf("default JiraAI = %q, want \"\" (inherits Opus)", got)
	}

	// config overrides the default.
	if got := For(JiraAI, "sonnet"); got != "sonnet" {
		t.Fatalf("config JiraAI = %q, want sonnet", got)
	}
	// the "padrao" nickname normalizes to "" = "use THE TIER's default" (it does not
	// force Opus): for Suggest that is haiku; for JiraAI it is "" (inherits Opus).
	if got := For(Suggest, "padrao"); got != "haiku" {
		t.Fatalf("config Suggest=padrao = %q, want haiku (the tier default)", got)
	}
	if got := For(JiraAI, "inherit"); got != "" {
		t.Fatalf("config JiraAI=inherit = %q, want \"\" (inherits Opus)", got)
	}

	// env overrides config (and the default).
	t.Setenv("VPSM_AI_MODEL_JIRA", "opus")
	if got := For(JiraAI, "sonnet"); got != "opus" {
		t.Fatalf("env>config JiraAI = %q, want opus", got)
	}
	t.Setenv("VPSM_AI_MODEL_SUGGEST", "sonnet")
	if got := For(Suggest, ""); got != "sonnet" {
		t.Fatalf("env Suggest = %q, want sonnet", got)
	}
}

// TestIntakeModel — the Intake tier resolves to a COMPLETE Anthropic id (never "").
// Precedence env > configured > default; nicknames become the full id.
func TestIntakeModel(t *testing.T) {
	// Default (empty config) = full sonnet id.
	if got := IntakeModel(""); got != "claude-sonnet-4-6" {
		t.Errorf("IntakeModel(\"\") = %q, want claude-sonnet-4-6", got)
	}
	// Nicknames → full id.
	for alias, want := range map[string]string{
		"sonnet": "claude-sonnet-4-6",
		"opus":   "claude-opus-4-7",
		"haiku":  "claude-haiku-4-5-20251001",
		"OPUS":   "claude-opus-4-7", // case-insensitive
	} {
		if got := IntakeModel(alias); got != want {
			t.Errorf("IntakeModel(%q) = %q, want %q", alias, got, want)
		}
	}
	// "inherit"/"default" do NOT become "" here (the route demands a concrete model) → default.
	if got := IntakeModel("default"); got != "claude-sonnet-4-6" {
		t.Errorf("IntakeModel(\"default\") = %q, want claude-sonnet-4-6 (never empty)", got)
	}
	// An unknown full id passes straight through (future-proof).
	if got := IntakeModel("claude-sonnet-5"); got != "claude-sonnet-5" {
		t.Errorf("IntakeModel(full id) = %q, want it passed straight through", got)
	}
	// Env overrides config.
	t.Setenv("VPSM_AI_MODEL_INTAKE", "opus")
	if got := IntakeModel("sonnet"); got != "claude-opus-4-7" {
		t.Errorf("IntakeModel with env=opus = %q, want claude-opus-4-7", got)
	}
}

// TestAllowed proves the anti-injection allowlist (Layer 3): only the canonical
// ids plus "" get through; anything else (an injection attempt included) is blocked.
func TestAllowed(t *testing.T) {
	for _, ok := range []string{"", "haiku", "sonnet", "opus", "fable", "PADRAO", " opus "} {
		if !Allowed(ok) {
			t.Errorf("Allowed(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{"gpt-4", "haiku; rm -rf /", "claude-opus-4-8", "sonnet --dangerously"} {
		if Allowed(bad) {
			t.Errorf("Allowed(%q) = true, want false (it must block)", bad)
		}
	}
}
