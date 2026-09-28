package api

import (
	"encoding/json"
	"net/http"
	"path/filepath"

	"server-control-panel/internal/aimodel"
	"server-control-panel/internal/auth"
	"server-control-panel/internal/config"
)

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
			"effective": map[string]string{
				"suggest": aimodel.For(aimodel.Suggest, cur.Suggest),
				"jira_ai": aimodel.For(aimodel.JiraAI, cur.JiraAI),
			},
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
