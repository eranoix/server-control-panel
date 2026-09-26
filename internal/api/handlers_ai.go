package api

// handlers_ai.go — Claude (OAuth-only).
//
// Covers:
//   - handleClaude (overview) / handleClaudeMode (GET health) / handleClaudePanic
//   - handleClaudeSessionFork + handleClaudeSessionRestart
//
// The app was consolidated to Claude-only: the private-ai-api bridge and Venice
// proxies, the AI Toolkit preflight and the mode toggle were all removed.
// The private-ai-api service is still on the host (used by other projects), it is
// simply no longer exposed by the UI.
//
// Extracted from api.go. Still *Router because it uses audit/auth/cfg.

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
	"server-control-panel/internal/clauderouter"
	"server-control-panel/internal/privateaiapi"
	ptysvc "server-control-panel/internal/pty"
)

// ---------- Claude/Config/Exec ----------

func (r *Router) handleClaude(w http.ResponseWriter, req *http.Request) {
	o, err := claude.Collect(r.cfg.ClaudeHome)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, o)
}

// handleClaudeMode (GET-only) reports the claude-router health. An earlier
// change consolidated the app to Claude-only (OAuth), so there is no mode —
// the router is always an OAuth passthrough. The endpoint stays so the UI can
// surface router reachability/latency.
func (r *Router) handleClaudeMode(w http.ResponseWriter, req *http.Request) {
	if auth.UserFrom(req) == "" {
		writeErr(w, 401, "unauthorized")
		return
	}
	if req.Method != http.MethodGet {
		writeErr(w, 405, "method not allowed")
		return
	}
	h := clauderouter.New().Healthz()
	writeJSON(w, map[string]any{
		"mode":      "oauth",
		"health":    h,
		"router_ok": h.Reachable,
	})
}

// handleClaudePanic runs the full panic-reset flow: backup settings.json,
// strip ANTHROPIC_API_KEY/AUTH_TOKEN, force state=oauth, restart router,
// verify health. Returns a structured result with every step.
func (r *Router) handleClaudePanic(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	user := auth.UserFrom(req)
	if user == "" {
		writeErr(w, 401, "unauthorized")
		return
	}
	res := clauderouter.New().Panic()
	r.auditEvent(req, user, "claude.panic", fmt.Sprintf("ok=%v steps=%d errors=%d", res.OK, len(res.Steps), len(res.Errors)))
	writeJSON(w, res)
}

// interactiveModel resolves the model of an interactive claude panel. Interactive
// panels do NOT go through tiering: an explicit per-session choice (reqModel —
// e.g. the fork's "modelo" selector) wins; otherwise it returns "" = NO --model,
// inheriting the model the operator predefined in Claude Code's settings.json
// (Opus / full window). The tier applies only to parallel agents and to the other
// AI functions (suggest, jira_ai). The spawner revalidates reqModel against the
// allowlist (anti-injection).
func (r *Router) interactiveModel(reqModel string) string {
	return strings.TrimSpace(reqModel)
}

// handleClaudeSessionFork spawns a new detached session running
// `claude --resume <uuid>` so the user can continue an existing conversation
// in a fresh process.
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
		Model       string `json:"model"` // tier opcional; "" = default configurado
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	if body.SessionName == "" {
		body.SessionName = "vpsm-" + user + "-fork-" + strconv.FormatInt(time.Now().Unix(), 10)
	}
	created, err := ptysvc.SpawnClaudeSession(body.SessionName, body.ResumeUUID, r.forkConfigDir(), r.interactiveModel(body.Model))
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	r.auditEvent(req, user, "claude.fork", created+" resume="+body.ResumeUUID)
	writeJSON(w, map[string]any{"ok": true, "session": created})
}

// handleClaudeSessionRestart kills a session and recreates it running
// `claude --continue`. The conversation history is restored from the JSONL.
// Any websocket attached to the old session will drop and must reattach.
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
		Model       string `json:"model"` // tier opcional; "" = default configurado
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

// ---------- private-ai-api · token management (Claude AI → Tokens) ----------
//
// The /api/private-ai/ proxy was removed from the UI; these handlers reintroduce
// it only for token management (admin API). The service is still on the host
// (127.0.0.1:8787). VPSM proxies server-side with the ADMIN_TOKEN — the token
// never reaches the browser. Access: any logged-in user (auth.UserFrom).
// Mutations are audited.
//
// Source of the admin token (in order): (1) an optional override in the global
// secrets vault under the key private_ai_admin_token; (2) ADMIN_TOKEN read from
// private-ai-api's own EnvironmentFile (cfg.PrivateAIAdminTokenFile,
// default /etc/private-ai-api/env) — the source of truth, without duplicating the secret.

const privateAIAdminTokenSecret = "private_ai_admin_token"

// privateAIClient builds an admin API client using the URL from the config and
// the resolved admin token. Returns ok=false when no token is found —
// the caller answers 503 with a clear warning.
func (r *Router) privateAIClient() (*privateaiapi.Client, bool) {
	token := r.privateAIAdminToken()
	if strings.TrimSpace(token) == "" {
		return nil, false
	}
	return privateaiapi.New(r.cfg.PrivateAIURL, token), true
}

// privateAIAdminToken resolves the admin token: the global vault override first,
// otherwise it reads ADMIN_TOKEN from private-ai-api's EnvironmentFile.
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

// readEnvFileVar extracts the value of KEY=value from a file in dotenv/systemd
// EnvironmentFile format. Ignores comments and whitespace; strips single or
// double quotes around the value. Returns "" if absent or unreadable.
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

// writePrivateAIResult repassa a resposta crua da admin API (status + body)
// para o cliente, preservando o status upstream.
func writePrivateAIResult(w http.ResponseWriter, res *privateaiapi.Result) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(res.Status)
	_, _ = w.Write(res.Body)
}

// handlePrivateAITokens — GET lists the keys, POST creates a new one.
// POST forwards the body (name + optional limits) and returns the upstream's
// plaintext (shown exactly once in the UI).
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

// handlePrivateAITokenAction — per-id actions under /api/private-ai/tokens/.
//
//	POST  /api/private-ai/tokens/<id>/revoke  → revokes
//	PATCH /api/private-ai/tokens/<id>         → edits limits
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

// handlePrivateAIStatus — painel de status (OAuth + sistema + uso 24h).
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

// decodeJSONObject reads a (size-limited) JSON object body into a map.
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
