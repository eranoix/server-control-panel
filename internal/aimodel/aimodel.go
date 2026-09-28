package aimodel

import (
	"os"
	"strings"
)

type Tier string

const (
	Suggest Tier = "suggest"
	JiraAI  Tier = "jira_ai"
	Intake  Tier = "intake"
)

const intakeDefaultModel = "claude-sonnet-4-6"

var intakeAliasToFullID = map[string]string{
	"haiku":  "claude-haiku-4-5-20251001",
	"sonnet": "claude-sonnet-4-6",
	"opus":   "claude-opus-4-7",
}

var defaults = map[Tier]string{
	Suggest: "haiku",
	JiraAI:  "",
}

var envKeys = map[Tier]string{
	Suggest: "PANEL_AI_MODEL_SUGGEST",
	JiraAI:  "PANEL_AI_MODEL_JIRA",
}

func For(t Tier, configured string) string {
	if k := envKeys[t]; k != "" {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return normalize(v)
		}
	}
	if c := normalize(configured); c != "" {
		return c
	}
	return defaults[t]
}

func IntakeModel(configured string) string {
	pick := ""
	if v := strings.TrimSpace(os.Getenv("PANEL_AI_MODEL_INTAKE")); v != "" {
		pick = v
	} else if c := strings.TrimSpace(configured); c != "" {
		pick = c
	}
	switch strings.ToLower(pick) {
	case "", "inherit", "default", "opus-default":
		return intakeDefaultModel
	}
	if full, ok := intakeAliasToFullID[strings.ToLower(pick)]; ok {
		return full
	}
	return pick
}

func normalize(m string) string {
	m = strings.ToLower(strings.TrimSpace(m))
	switch m {
	case "inherit", "default", "opus-default":
		return ""
	}
	return m
}

func Allowed(model string) bool {
	switch normalize(model) {
	case "", "haiku", "sonnet", "opus", "fable":
		return true
	}
	return false
}
