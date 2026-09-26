package api

// handlers_ai_models.go — editor for the per-complexity model tiering.
// Exposes/edits config.AIModels: which model each class of AI task
// (Suggest/JiraAI) uses. The real resolution is env > this config > the tier
// default (internal/aimodel); the UI edits only the config layer. Interactive
// panels are NOT tiered — they inherit settings.json — and therefore do not
// appear here.
//
// Admin-only (the same mustPrimary gate as the alerting editor). Validation uses
// aimodel.Allowed's allowlist — the UI only sets the canonical models (dropdown);
// a future/arbitrary model is still possible via the env var, the escape hatch.

import (
	"encoding/json"
	"net/http"
	"path/filepath"

	"server-control-panel/internal/aimodel"
	"server-control-panel/internal/auth"
	"server-control-panel/internal/config"
)

// handleAIModelsConfig serve GET (config + modelos efetivos + allowlist) e
// POST (valida contra a allowlist e persiste). "" em qualquer tier = herda o
// default do processo (Opus).
func (r *Router) handleAIModelsConfig(w http.ResponseWriter, req *http.Request) {
	user := auth.UserFrom(req)
	if !r.isPrimary(user) {
		writeErr(w, 403, "admin only")
		return
	}

	switch req.Method {
	case http.MethodGet:
		r.cfgMu.Lock()
		cur := r.cfg.AIModels
		r.cfgMu.Unlock()
		writeJSON(w, map[string]any{
			"config": cur,
			// EFFECTIVE models (after env>config>default) — the UI shows what is
			// really live, which may differ from the config if an env var is set.
			"effective": map[string]string{
				"suggest": aimodel.For(aimodel.Suggest, cur.Suggest),
				"jira_ai": aimodel.For(aimodel.JiraAI, cur.JiraAI),
			},
			// "" = "Padrão (Opus)" in the dropdown; inherits the process default.
			"allowed":  []string{"", "haiku", "sonnet", "opus", "fable"},
			"defaults": map[string]string{"suggest": "haiku", "jira_ai": ""},
		})
	case http.MethodPost:
		var body config.AIModels
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			writeErr(w, 400, "bad json")
			return
		}
		for _, m := range []string{body.Suggest, body.JiraAI} {
			if !aimodel.Allowed(m) {
				writeErr(w, 400, "invalid model: "+m)
				return
			}
		}
		r.cfgMu.Lock()
		r.cfg.AIModels = body
		err := config.Save(r.cfg, filepath.Join(r.cfg.DataDir, "config.json"))
		r.cfgMu.Unlock()
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		r.auditEvent(req, user, "ai_models.update", "")
		writeJSON(w, map[string]any{"status": "ok"})
	default:
		writeErr(w, 405, "method not allowed")
	}
}
