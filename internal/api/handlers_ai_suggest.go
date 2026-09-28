package api

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
	Kind       string  `json:"kind"`
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
		return "warning"
	}
	return s
}

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
