package api

// handlers_ai_suggest.go — AI-suggested alert name+description.
//
// The alert builder fills name/description instantly (a front-end heuristic). The
// "✨ refinar com IA" button calls HERE, which runs `claude -p` (same pattern as
// jira_ai: jiraai/runner.go:213) with the jobs account's CLAUDE_CONFIG_DIR, and
// returns a more natural text. Best-effort: if the AI fails or times out, the
// front-end keeps the instant suggestion.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"server-control-panel/internal/aimodel"
	"server-control-panel/internal/auth"
	"server-control-panel/internal/claudebin"
)

type suggestAlertReq struct {
	Kind       string  `json:"kind"` // "metric" | "event"
	Metric     string  `json:"metric"`
	Label      string  `json:"label"`
	Unit       string  `json:"unit"`
	Category   string  `json:"category"`
	Op         string  `json:"op"`
	Threshold  float64 `json:"threshold"`
	Duration   int     `json:"duration"`
	Severity   string  `json:"severity"`
	EventType  string  `json:"event_type"`
	EventLabel string  `json:"event_label"`
}

// handleAISuggestAlert gera {name, description} para um alerta, via `claude -p`.
func (r *Router) handleAISuggestAlert(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	user := auth.UserFrom(req)
	if !r.isPrimary(user) {
		writeErr(w, 403, "admin only")
		return
	}
	var body suggestAlertReq
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}

	// This is the MOST trivial AI task in the system (naming an alert), so it
	// routes to the cheap tier (haiku by default, proven under the Max/OAuth
	// router). The deadline goes 25s→35s to fit the retry below without being
	// swallowed — haiku is fast, two spawns fit comfortably.
	ctx, cancel := context.WithTimeout(req.Context(), 35*time.Second)
	defer cancel()

	model := aimodel.For(aimodel.Suggest, r.cfg.AIModels.Suggest)
	prompt := buildAlertSuggestPrompt(body)
	env := os.Environ()
	if r.claudeAccts != nil {
		if dir := r.claudeAccts.ConfigDirFor("jobs"); dir != "" {
			env = append(env, "CLAUDE_CONFIG_DIR="+dir)
		}
	}
	// run spawns a NEW claude process on every call: exec.Cmd is single-use
	// (after Output() the same cmd cannot be re-executed), so the retry below
	// needs a fresh Cmd. When model=="" (config/env asked to "inherit"), it
	// falls back to the plain `claude -p` spawn, byte-identical to the
	// pre-tiering behaviour. --fallback-model sonnet covers a transient haiku
	// outage without promoting to Opus.
	run := func() ([]byte, error) {
		args := []string{"-p", prompt}
		if model != "" {
			args = append([]string{"--model", model, "--fallback-model", "sonnet"}, args...)
		}
		c := exec.CommandContext(ctx, claudebin.Path(), args...)
		c.Env = env
		return c.Output()
	}

	out, err := run()
	if err != nil {
		writeErr(w, 502, "AI unavailable: "+err.Error())
		return
	}
	name, desc := parseSuggestJSON(string(out))
	if name == "" && desc == "" {
		// An empty parse ≠ unavailability — the model answered, but in prose/outside
		// the format (--fallback-model does NOT cover this case, it only covers the
		// primary going down). A 2nd attempt with a fresh Cmd usually fixes it before the 502.
		if out2, err2 := run(); err2 == nil {
			name, desc = parseSuggestJSON(string(out2))
		}
	}
	if name == "" && desc == "" {
		writeErr(w, 502, "AI did not return a valid suggestion")
		return
	}
	writeJSON(w, map[string]string{"name": name, "description": desc})
}

func buildAlertSuggestPrompt(b suggestAlertReq) string {
	var subject string
	if b.Kind == "event" {
		subject = fmt.Sprintf("an alert about event %q (%s), severity %q", b.EventLabel, b.EventType, sevOrDefault(b.Severity))
	} else {
		subject = fmt.Sprintf("an alert about metric %q (key %s, category %s) that fires when the value is %s %.4g %s, sustained for %ds, severity %q",
			b.Label, b.Metric, b.Category, b.Op, b.Threshold, b.Unit, b.Duration, sevOrDefault(b.Severity))
	}
	return "You name monitoring alerts for a server control panel, in English. " +
		"For " + subject + ", produce: a short, clear NAME (40 characters max, plain human language, no code jargon) and a one-sentence DESCRIPTION explaining in simple words what the alert warns about and why it matters. " +
		"Reply with RAW JSON ONLY (no code fences, no extra text), in exactly this format: " +
		`{"name":"...","description":"..."}`
}

func sevOrDefault(s string) string {
	if s == "" {
		return "aviso"
	}
	return s
}

// parseSuggestJSON extracts the first {...} object from the output (tolerant of
// surrounding text or code fences) and returns name/description.
func parseSuggestJSON(s string) (name, desc string) {
	i := strings.Index(s, "{")
	j := strings.LastIndex(s, "}")
	if i < 0 || j <= i {
		return "", ""
	}
	var out struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal([]byte(s[i:j+1]), &out); err != nil {
		return "", ""
	}
	return strings.TrimSpace(out.Name), strings.TrimSpace(out.Description)
}
