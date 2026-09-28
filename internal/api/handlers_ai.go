package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/claude"
	"server-control-panel/internal/privateaiapi"
	ptysvc "server-control-panel/internal/pty"
)

func (r *Router) handleClaude(w http.ResponseWriter, req *http.Request) {
	o, err := claude.Collect(r.cfg.ClaudeHome)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, o)
}

func (r *Router) interactiveModel(reqModel string) string {
	return strings.TrimSpace(reqModel)
}

func (r *Router) handleClaudeSessionFork(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	user := auth.UserFrom(req)
	if user == "" {
		writeErr(w, 401, "unauthorized")
		return
	}
	var body struct {
		SessionName string `json:"session_name"`
		ResumeUUID  string `json:"resume_uuid"`
		Model       string `json:"model"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	if body.SessionName == "" {
		body.SessionName = "panel-" + user + "-fork-" + strconv.FormatInt(time.Now().Unix(), 10)
	}
	created, err := ptysvc.SpawnClaudeSession(body.SessionName, body.ResumeUUID, r.forkConfigDir(), r.interactiveModel(body.Model))
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	r.auditEvent(req, user, "claude.fork", created+" resume="+body.ResumeUUID)
	writeJSON(w, map[string]any{"ok": true, "session": created})
}

func (r *Router) handleClaudeSessionRestart(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	user := auth.UserFrom(req)
	if user == "" {
		writeErr(w, 401, "unauthorized")
		return
	}
	var body struct {
		SessionName string `json:"session_name"`
		Model       string `json:"model"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body.SessionName == "" {
		writeErr(w, 400, "session_name required")
		return
	}
	if err := ptysvc.RestartClaudeSession(body.SessionName, r.terminalConfigDirForSession(body.SessionName), r.interactiveModel(body.Model)); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	r.auditEvent(req, user, "claude.restart", body.SessionName)
	writeJSON(w, map[string]any{"ok": true, "session": body.SessionName})
}

const privateAIAdminTokenSecret = "private_ai_admin_token"

func (r *Router) privateAIClient() (*privateaiapi.Client, bool) {
	token := r.privateAIAdminToken()
	if strings.TrimSpace(token) == "" {
		return nil, false
	}
	return privateaiapi.New(r.cfg.PrivateAIURL, token), true
}

func (r *Router) privateAIAdminToken() string {
	if r.secrets != nil {
		if v, ok := r.secrets.Get(privateAIAdminTokenSecret); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	if path := r.cfg.PrivateAIAdminTokenFile; path != "" {
		if tok := readEnvFileVar(path, "ADMIN_TOKEN"); tok != "" {
			return tok
		}
	}
	return ""
}

func readEnvFileVar(path, key string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	prefix := key + "="
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		val := strings.TrimSpace(strings.TrimPrefix(line, prefix))
		if len(val) >= 2 {
			if (val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'') {
				val = val[1 : len(val)-1]
			}
		}
		return val
	}
	return ""
}

func writePrivateAIResult(w http.ResponseWriter, res *privateaiapi.Result) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(res.Status)
	_, _ = w.Write(res.Body)
}

func (r *Router) handlePrivateAITokens(w http.ResponseWriter, req *http.Request) {
	user := auth.UserFrom(req)
	if user == "" {
		writeErr(w, 401, "unauthorized")
		return
	}
	cli, ok := r.privateAIClient()
	if !ok {
		writeErr(w, 503, "private-ai-api admin token not found (set ADMIN_TOKEN in /etc/private-ai-api/env or the secret "+privateAIAdminTokenSecret+")")
		return
	}
	ctx := req.Context()

	switch req.Method {
	case http.MethodGet:
		res, err := cli.ListKeys(ctx)
		if err != nil {
			writeErr(w, 502, "private-ai-api unreachable: "+err.Error())
			return
		}
		writePrivateAIResult(w, res)

	case http.MethodPost:
		payload, err := decodeJSONObject(req)
		if err != nil {
			writeErr(w, 400, "bad json")
			return
		}
		name, _ := payload["name"].(string)
		if strings.TrimSpace(name) == "" {
			writeErr(w, 400, "name is required")
			return
		}
		res, err := cli.CreateKey(ctx, payload)
		if err != nil {
			writeErr(w, 502, "private-ai-api unreachable: "+err.Error())
			return
		}
		r.auditEvent(req, user, "private_ai.token.create", "name="+name+" status="+strconv.Itoa(res.Status))
		writePrivateAIResult(w, res)

	default:
		writeErr(w, 405, "method not allowed")
	}
}

func (r *Router) handlePrivateAITokenAction(w http.ResponseWriter, req *http.Request) {
	user := auth.UserFrom(req)
	if user == "" {
		writeErr(w, 401, "unauthorized")
		return
	}
	cli, ok := r.privateAIClient()
	if !ok {
		writeErr(w, 503, "private-ai-api admin token not found (set ADMIN_TOKEN in /etc/private-ai-api/env or the secret "+privateAIAdminTokenSecret+")")
		return
	}
	ctx := req.Context()

	rest := strings.Trim(strings.TrimPrefix(req.URL.Path, "/api/private-ai/tokens/"), "/")
	parts := strings.Split(rest, "/")
	if len(parts) == 0 || parts[0] == "" {
		writeErr(w, 400, "id is required")
		return
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, 400, "invalid id")
		return
	}

	switch {
	case len(parts) == 2 && parts[1] == "revoke" && req.Method == http.MethodPost:
		res, err := cli.RevokeKey(ctx, id)
		if err != nil {
			writeErr(w, 502, "private-ai-api unreachable: "+err.Error())
			return
		}
		r.auditEvent(req, user, "private_ai.token.revoke", fmt.Sprintf("id=%d status=%d", id, res.Status))
		writePrivateAIResult(w, res)

	case len(parts) == 1 && req.Method == http.MethodPatch:
		payload, err := decodeJSONObject(req)
		if err != nil {
			writeErr(w, 400, "bad json")
			return
		}
		res, err := cli.UpdateKey(ctx, id, payload)
		if err != nil {
			writeErr(w, 502, "private-ai-api unreachable: "+err.Error())
			return
		}
		r.auditEvent(req, user, "private_ai.token.update", fmt.Sprintf("id=%d status=%d", id, res.Status))
		writePrivateAIResult(w, res)

	default:
		writeErr(w, 404, "route not found")
	}
}

func (r *Router) handlePrivateAIStatus(w http.ResponseWriter, req *http.Request) {
	if auth.UserFrom(req) == "" {
		writeErr(w, 401, "unauthorized")
		return
	}
	if req.Method != http.MethodGet {
		writeErr(w, 405, "method not allowed")
		return
	}
	cli, ok := r.privateAIClient()
	if !ok {
		writeErr(w, 503, "private-ai-api admin token not found (set ADMIN_TOKEN in /etc/private-ai-api/env or the secret "+privateAIAdminTokenSecret+")")
		return
	}
	ctx, cancel := context.WithTimeout(req.Context(), 12*time.Second)
	defer cancel()
	writeJSON(w, cli.Status(ctx))
}

func decodeJSONObject(req *http.Request) (map[string]any, error) {
	var payload map[string]any
	dec := json.NewDecoder(io.LimitReader(req.Body, 1<<20))
	if err := dec.Decode(&payload); err != nil {
		return nil, err
	}
	if payload == nil {
		payload = map[string]any{}
	}
	return payload, nil
}
