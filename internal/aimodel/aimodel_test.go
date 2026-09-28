package aimodel

import "testing"

func TestForPrecedence(t *testing.T) {
	if got := For(Suggest, ""); got != "haiku" {
		t.Fatalf("default Suggest = %q, want haiku", got)
	}
	if got := For(JiraAI, ""); got != "" {
		t.Fatalf("default JiraAI = %q, want \"\" (inherits Opus)", got)
	}

	if got := For(JiraAI, "sonnet"); got != "sonnet" {
		t.Fatalf("config JiraAI = %q, want sonnet", got)
	}
	if got := For(Suggest, "default"); got != "haiku" {
		t.Fatalf("config Suggest=default = %q, want haiku (the tier default)", got)
	}
	if got := For(JiraAI, "inherit"); got != "" {
		t.Fatalf("config JiraAI=inherit = %q, want \"\" (inherits Opus)", got)
	}

	t.Setenv("PANEL_AI_MODEL_JIRA", "opus")
	if got := For(JiraAI, "sonnet"); got != "opus" {
		t.Fatalf("env>config JiraAI = %q, want opus", got)
	}
	t.Setenv("PANEL_AI_MODEL_SUGGEST", "sonnet")
	if got := For(Suggest, ""); got != "sonnet" {
		t.Fatalf("env Suggest = %q, want sonnet", got)
	}
}

func TestIntakeModel(t *testing.T) {
	if got := IntakeModel(""); got != "claude-sonnet-4-6" {
		t.Errorf("IntakeModel(\"\") = %q, want claude-sonnet-4-6", got)
	}
	for alias, want := range map[string]string{
		"sonnet": "claude-sonnet-4-6",
		"opus":   "claude-opus-4-7",
		"haiku":  "claude-haiku-4-5-20251001",
		"OPUS":   "claude-opus-4-7",
	} {
		if got := IntakeModel(alias); got != want {
			t.Errorf("IntakeModel(%q) = %q, want %q", alias, got, want)
		}
	}
	if got := IntakeModel("default"); got != "claude-sonnet-4-6" {
		t.Errorf("IntakeModel(\"default\") = %q, want claude-sonnet-4-6 (never empty)", got)
	}
	if got := IntakeModel("claude-sonnet-5"); got != "claude-sonnet-5" {
		t.Errorf("IntakeModel(full id) = %q, want it passed straight through", got)
	}
	t.Setenv("PANEL_AI_MODEL_INTAKE", "opus")
	if got := IntakeModel("sonnet"); got != "claude-opus-4-7" {
		t.Errorf("IntakeModel with env=opus = %q, want claude-opus-4-7", got)
	}
}

func TestAllowed(t *testing.T) {
	for _, ok := range []string{"", "haiku", "sonnet", "opus", "fable", "DEFAULT", " opus "} {
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
