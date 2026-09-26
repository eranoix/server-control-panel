// Package aimodel centralizes model tiering: routing each class of AI task to a
// model matching its complexity — a trivial task goes to a cheap/fast model
// (Haiku/Sonnet), deep reasoning stays on Opus. The goal is to take the light
// work off the Opus quota (pricier and scarcer: ratelimits.go SevenDayOpus is a
// window of its own) without regressing quality where it matters.
//
// Resolution precedence, the same for every tier: env > config > built-in default.
// A model resolved to "" means "inherit the process default" (Opus) — in that
// case the caller must NOT pass --model, keeping the spawn byte-identical to the
// behavior from before tiering existed. Only the Suggest tier downgrades by
// default; the others are born as "" (unchanged) and move only by an explicit
// config/env setting.
//
// This package is the single point for Layers 1 (ai_suggest) and 2 (jira_ai). The
// interactive panels (Layer 3) do NOT come through here any more: they inherit the
// model the operator pre-set in Claude Code's settings.json (Opus / full window),
// decoupled from tiering. It does not import config (it receives the configured
// value as a string), so there is no import cycle.
package aimodel

import (
	"os"
	"strings"
)

// Tier identifies a class of AI workload for model routing.
type Tier string

const (
	// Suggest — trivial one-shot generation (an alert's name/description). Downgrades
	// to haiku by default (proven under the Max/OAuth router in the smoke test); the
	// ai_suggest call site still passes --fallback-model sonnet.
	Suggest Tier = "suggest"
	// JiraAI — ticket audit/refinement in the repo. Default "" (inherits Opus) so
	// that deep ticket analysis stays unchanged, barring config/env.
	JiraAI Tier = "jira_ai"
	// Intake — extraction/classification for the intake pipeline, which talks to
	// private-ai through the NATIVE /v1/messages passthrough. Unlike the tiers above
	// (which become the CLI's --model and resolve the alias upstream), this path does
	// NOT resolve nicknames: it demands a COMPLETE Anthropic id. Hence its own
	// resolver (IntakeModel), which never returns "". Default sonnet.
	Intake Tier = "intake"
)

// intakeDefaultModel is the Intake tier's default full Anthropic id. NEVER "".
const intakeDefaultModel = "claude-sonnet-4-6"

// intakeAliasToFullID maps the CLI nicknames to the full Anthropic id that
// /v1/messages demands (the alias is only resolved in the backend on the
// OpenAI-compat route, not this one). Kept in sync with private-ai's resolveUpstreamModel.
var intakeAliasToFullID = map[string]string{
	"haiku":  "claude-haiku-4-5-20251001",
	"sonnet": "claude-sonnet-4-6",
	"opus":   "claude-opus-4-7",
}

// defaults is the built-in model per tier. "" = inherit the process default (Opus).
var defaults = map[Tier]string{
	Suggest: "haiku",
	JiraAI:  "",
}

// envKeys maps each tier to the env var that overrides it (highest precedence).
// The env is the path that crosses processes: detached jobs inherit the parent's
// env, so an env override holds for the detached path too.
var envKeys = map[Tier]string{
	Suggest: "VPSM_AI_MODEL_SUGGEST",
	JiraAI:  "VPSM_AI_MODEL_JIRA",
}

// For resolves a tier's model id: env > configured > built-in default.
// `configured` is the value coming from config (config.AIModels.<tier>), possibly
// "". A return of "" means "inherit the process default" — the caller must NOT
// pass --model.
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

// IntakeModel resolves the COMPLETE Anthropic id for the intake path
// (/v1/messages, which does not resolve nicknames). Precedence env > configured >
// default; nicknames (haiku/sonnet/opus) become the full id; NEVER returns ""
// (that route needs a concrete upstream model). `configured` comes from
// config.AIModels.Intake.
func IntakeModel(configured string) string {
	pick := ""
	if v := strings.TrimSpace(os.Getenv("VPSM_AI_MODEL_INTAKE")); v != "" {
		pick = v
	} else if c := strings.TrimSpace(configured); c != "" {
		pick = c
	}
	switch strings.ToLower(pick) {
	case "", "inherit", "default", "padrao", "padrão", "opus-default":
		return intakeDefaultModel
	}
	if full, ok := intakeAliasToFullID[strings.ToLower(pick)]; ok {
		return full
	}
	return pick // assume a full id (future-proof)
}

// normalize lowercases and maps a few friendly nicknames (the ones used in the
// UI dropdown) to "" = inherit the default. Unknown values pass through intact so
// that a future model id keeps working.
func normalize(m string) string {
	m = strings.ToLower(strings.TrimSpace(m))
	switch m {
	case "inherit", "default", "padrao", "padrão", "opus-default":
		return ""
	}
	return m
}

// Allowed reports whether model is on the interactive-selection allowlist. It is
// the anti-injection guard (Layer 3): a model id that may arrive from an HTTP
// request is only concatenated into a command (spawn.go builds `cmd` as a string)
// after passing through here. "" is allowed and means "no --model" (inherit the default).
func Allowed(model string) bool {
	switch normalize(model) {
	case "", "haiku", "sonnet", "opus", "fable":
		return true
	}
	return false
}
