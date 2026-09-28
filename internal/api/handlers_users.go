package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/claudeacct"
	"server-control-panel/internal/config"
	ptysvc "server-control-panel/internal/pty"
	"server-control-panel/internal/scope"
)

func (r *Router) handleExec(w http.ResponseWriter, req *http.Request) {
	caller, ok := r.mustPrimary(w, req)
	if !ok {
		return
	}
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	var body struct {
		Cmd string `json:"cmd"`
	}
	if err := json.NewDecoder(io.LimitReader(req.Body, 64*1024)).Decode(&body); err != nil || body.Cmd == "" {
		writeErr(w, 400, "cmd required")
		return
	}
	ctx, cancel := context.WithTimeout(req.Context(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/bash", "-lc", body.Cmd)
	outBytes, err := cmd.CombinedOutput()
	resp := map[string]any{"output": string(outBytes), "ok": err == nil}
	if err != nil {
		resp["error"] = err.Error()
	}
	r.auditEvent(req, caller, "exec", body.Cmd)
	writeJSON(w, resp)
}

func (r *Router) handleHostShell(w http.ResponseWriter, req *http.Request) {
	user := auth.UserFrom(req)
	sessionName := ptysvc.SafeSessionName(req.URL.Query().Get("name"))
	if sessionName == "" {
		sessionName = ptysvc.SafeSessionName(req.URL.Query().Get("tab"))
	}
	if sessionName == "" {
		sessionName = "main"
	}
	ptysvc.HostShell(w, req, user, r.isPrimary(user), r.sessionOwn,
		r.terminalConfigDirForSession(sessionName), r.cfg.DataDir, r.sessReg)
}

type userInfo struct {
	Username  string `json:"username"`
	IsPrimary bool   `json:"is_primary"`
	IsAdmin   bool   `json:"is_admin"`
	HasTOTP   bool   `json:"has_totp"`
	Sessions  int    `json:"sessions"`
}

func (r *Router) handleUsersList(w http.ResponseWriter, req *http.Request) {
	if auth.UserFrom(req) == "" {
		writeErr(w, 401, "unauthorized")
		return
	}
	r.cfgMu.Lock()
	all := r.cfg.AllUsers()
	primaryName := r.cfg.Primary
	adminSet := map[string]bool{}
	for _, name := range r.cfg.Admins() {
		adminSet[name] = true
	}
	r.cfgMu.Unlock()
	sessionsPerUser := map[string]int{}
	if r.auth.Sessions() != nil {
		now := time.Now().Unix()
		for _, u := range all {
			for _, s := range r.auth.Sessions().ListForUser(u.Username) {
				if !s.Revoked && s.ExpiresAt > now {
					sessionsPerUser[s.User]++
				}
			}
		}
	}
	out := make([]userInfo, 0, len(all))
	for _, u := range all {
		out = append(out, userInfo{
			Username:  u.Username,
			IsPrimary: u.Username == primaryName,
			IsAdmin:   adminSet[u.Username],
			HasTOTP:   u.SupabaseMFAEnabled || u.TOTPSecret != "",
			Sessions:  sessionsPerUser[u.Username],
		})
	}
	writeJSON(w, map[string]any{"users": sanitizeList(out, "Username")})
}

func (r *Router) handleUserCreate(w http.ResponseWriter, req *http.Request) {
	caller, ok := r.mustPrimary(w, req)
	if !ok {
		return
	}
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	body.Username = strings.TrimSpace(body.Username)
	if body.Username == "" || len(body.Username) > 40 {
		writeErr(w, 400, "invalid username (1-40 chars)")
		return
	}
	for _, c := range body.Username {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-') {
			writeErr(w, 400, "username may contain only letters, digits, _ and -")
			return
		}
	}
	if len(body.Password) < 8 {
		writeErr(w, 400, "password must be at least 8 characters")
		return
	}
	hash, err := auth.HashPassword(body.Password)
	if err != nil {
		writeErr(w, 500, "bcrypt: "+err.Error())
		return
	}
	r.cfgMu.Lock()
	if err := r.cfg.AddUser(body.Username, hash); err != nil {
		r.cfgMu.Unlock()
		writeErr(w, 409, err.Error())
		return
	}
	saveErr := config.Save(r.cfg, filepath.Join(r.cfg.DataDir, "config.json"))
	r.auth.ReloadUsers(credsFromConfig(r.cfg))
	r.cfgMu.Unlock()
	if saveErr != nil {
		writeErr(w, 500, "save config: "+saveErr.Error())
		return
	}
	if r.whatsappMgr != nil {
		if su, err := scope.New(body.Username); err == nil {
			if err := r.whatsappMgr.Provision(su); err != nil {
				log.Printf("user.create whatsapp provision %s: %v", su, err)
			}
		}
	}
	r.auditEvent(req, caller, "user.create", body.Username)
	writeJSON(w, map[string]string{"status": "ok", "username": body.Username})
}

func (r *Router) handleUserDelete(w http.ResponseWriter, req *http.Request) {
	caller, ok := r.mustPrimary(w, req)
	if !ok {
		return
	}
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	var body struct {
		Username string `json:"username"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	if body.Username == "" {
		writeErr(w, 400, "empty username")
		return
	}
	if body.Username == caller {
		writeErr(w, 403, "cannot delete your own user (logged in right now)")
		return
	}
	r.cfgMu.Lock()
	isPrimaryTarget := r.cfg.Primary != "" && r.cfg.Primary == body.Username
	r.cfgMu.Unlock()
	if isPrimaryTarget {
		writeErr(w, 403, "cannot delete the primary user")
		return
	}
	if r.whatsappMgr != nil {
		if su, err := scope.New(body.Username); err == nil {
			if err := r.whatsappMgr.Decommission(su); err != nil {
				log.Printf("user.delete whatsapp decommission %s: %v", su, err)
			}
		}
	}
	r.cfgMu.Lock()
	if err := r.cfg.RemoveUser(body.Username); err != nil {
		r.cfgMu.Unlock()
		writeErr(w, 404, err.Error())
		return
	}
	saveErr := config.Save(r.cfg, filepath.Join(r.cfg.DataDir, "config.json"))
	r.auth.ReloadUsers(credsFromConfig(r.cfg))
	r.cfgMu.Unlock()
	if saveErr != nil {
		writeErr(w, 500, "save config: "+saveErr.Error())
		return
	}
	if r.auth.Sessions() != nil {
		r.auth.Sessions().RevokeAllExcept(body.Username, "")
	}
	if um := r.auth.UUIDMap(); um != nil {
		if um.Remove(body.Username) {
			if err := auth.SaveUUIDMap(um, filepath.Join(r.cfg.DataDir, "migration-uuid-map.json")); err != nil {
				log.Printf("user.delete: uuid_map save failed: %v", err)
			}
		}
	}
	r.auditEvent(req, caller, "user.delete", body.Username)
	writeJSON(w, map[string]string{"status": "ok"})
}

func (r *Router) handleUserSetAdmin(w http.ResponseWriter, req *http.Request) {
	caller, ok := r.mustPrimary(w, req)
	if !ok {
		return
	}
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	var body struct {
		Username string `json:"username"`
		Admin    bool   `json:"admin"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	body.Username = strings.TrimSpace(body.Username)
	if body.Username == "" {
		writeErr(w, 400, "empty username")
		return
	}
	r.cfgMu.Lock()
	if !r.cfg.HasUser(body.Username) {
		r.cfgMu.Unlock()
		writeErr(w, 404, "user not found")
		return
	}
	if err := r.cfg.SetAdmin(body.Username, body.Admin); err != nil {
		r.cfgMu.Unlock()
		writeErr(w, 422, err.Error())
		return
	}
	saveErr := config.Save(r.cfg, filepath.Join(r.cfg.DataDir, "config.json"))
	r.cfgMu.Unlock()
	if saveErr != nil {
		writeErr(w, 500, "save config: "+saveErr.Error())
		return
	}
	verb := "grant"
	if !body.Admin {
		verb = "revoke"
	}
	r.auditEvent(req, caller, "user.admin."+verb, body.Username)
	writeJSON(w, map[string]any{"status": "ok", "username": body.Username, "admin": body.Admin})
}

func (r *Router) handleUserResetPassword(w http.ResponseWriter, req *http.Request) {
	caller, ok := r.mustPrimary(w, req)
	if !ok {
		return
	}
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	if body.Username == "" || len(body.Password) < 8 {
		writeErr(w, 400, "empty username or password < 8 chars")
		return
	}
	hash, err := auth.HashPassword(body.Password)
	if err != nil {
		writeErr(w, 500, "bcrypt: "+err.Error())
		return
	}
	r.cfgMu.Lock()
	updated := r.cfg.SetPassword(body.Username, hash)
	var saveErr error
	if updated {
		saveErr = config.Save(r.cfg, filepath.Join(r.cfg.DataDir, "config.json"))
		r.auth.ReloadUsers(credsFromConfig(r.cfg))
	}
	r.cfgMu.Unlock()
	if !updated {
		writeErr(w, 404, "user not found")
		return
	}
	if saveErr != nil {
		writeErr(w, 500, "save config: "+saveErr.Error())
		return
	}
	r.auditEvent(req, caller, "user.reset-password", body.Username)
	writeJSON(w, map[string]string{"status": "ok"})
}

func (r *Router) handleUserDisable2FA(w http.ResponseWriter, req *http.Request) {
	caller, ok := r.mustPrimary(w, req)
	if !ok {
		return
	}
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	var body struct {
		Username string `json:"username"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	r.cfgMu.Lock()
	updated := r.cfg.SetTOTPSecret(body.Username, "")
	var saveErr error
	if updated {
		saveErr = config.Save(r.cfg, filepath.Join(r.cfg.DataDir, "config.json"))
	}
	r.cfgMu.Unlock()
	if !updated {
		writeErr(w, 404, "user not found")
		return
	}
	if saveErr != nil {
		writeErr(w, 500, "save config: "+saveErr.Error())
		return
	}
	r.auditEvent(req, caller, "user.disable-2fa", body.Username)
	writeJSON(w, map[string]string{"status": "ok"})
}

func (r *Router) handleUserRevokeSessions(w http.ResponseWriter, req *http.Request) {
	caller, ok := r.mustPrimary(w, req)
	if !ok {
		return
	}
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	var body struct {
		Username string `json:"username"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	if r.auth.Sessions() == nil {
		writeErr(w, 503, "sessions are not configured")
		return
	}
	count := r.auth.Sessions().RevokeAllExcept(body.Username, "")
	r.auditEvent(req, caller, "user.revoke-sessions", fmt.Sprintf("%s:%d", body.Username, count))
	writeJSON(w, map[string]any{"status": "ok", "revoked": count})
}

func (r *Router) handleTerminalSessions(w http.ResponseWriter, req *http.Request) {
	user := auth.UserFrom(req)
	if user == "" {
		writeErr(w, 401, "unauthorized")
		return
	}
	if req.URL.Query().Get("all") == "1" && r.isPrimary(user) {
		sessions, err := ptysvc.SessionListAnnotated(r.sessionOwn, r.claudeAccts, claudeacct.ConsumerTerminal)
		if err != nil {
			writeJSON(w, []any{})
			return
		}
		writeJSON(w, sanitizeList(r.enrichSessionsWithAgent(sessions), "name"))
		return
	}
	sessions, err := ptysvc.SessionListForUser(user, r.isPrimary(user), r.sessionOwn)
	if err != nil {
		writeJSON(w, []any{})
		return
	}
	writeJSON(w, sanitizeList(r.enrichSessionsWithAgent(sessions), "name"))
}

func (r *Router) enrichSessionsWithAgent(sessions []map[string]any) []map[string]any {
	var status map[string]AgentStatus
	if r.agentStatus != nil {
		status = r.agentStatus.Snapshot()
	}
	var cwds map[string]string
	if r.agentCWD != nil {
		cwds = r.agentCWD.All()
	}
	for _, s := range sessions {
		name, _ := s["name"].(string)
		if name == "" {
			continue
		}
		if st, ok := status[name]; ok {
			if st.State != "" {
				s["state"] = st.State
			}
			if st.Updated != 0 {
				s["updated"] = st.Updated
			}
			if st.CostUSD > 0 {
				s["cost_usd"] = st.CostUSD
			}
			if st.Tokens != nil {
				s["tokens"] = st.Tokens
			}
		}
		if cw := cwds[name]; cw != "" {
			s["cwd"] = cw
		}
	}
	return sessions
}

func (r *Router) handleTerminalCreate(w http.ResponseWriter, req *http.Request) {
	user := auth.UserFrom(req)
	if user == "" {
		writeErr(w, 401, "unauthorized")
		return
	}
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	var body struct {
		Name    string `json:"name"`
		CWD     string `json:"cwd"`
		Account string `json:"account"`
	}
	raw, _ := readRawJSONBody(req.Body)
	if err := json.Unmarshal(raw, &body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	name := ptysvc.SafeSessionName(body.Name)
	if name == "" {
		writeErr(w, 400, "name required")
		return
	}
	if alive, _ := ptysvc.SessionHas(name); alive {
		writeJSON(w, map[string]any{"ok": true, "name": name, "existed": true})
		return
	}
	if n := ptysvc.OwnedSessionCountLive(r.sessionOwn, user); n >= ptysvc.MaxSessionsPerUser {
		writeErr(w, 429, "session limit reached — close one first")
		return
	}
	cwd := strings.TrimSpace(body.CWD)
	if cwd != "" {
		if fi, err := os.Stat(cwd); err != nil || !fi.IsDir() {
			writeErr(w, 400, "invalid cwd")
			return
		}
	}
	var env []string
	if acc := strings.TrimSpace(body.Account); acc != "" && r.claudeAccts != nil {
		_ = r.claudeAccts.SetSessionAccount(name, acc)
	}
	if r.claudeAccts != nil {
		if cd := r.claudeAccts.ConfigDirForSession(name, claudeacct.ConsumerTerminal); cd != "" {
			env = append(env, "CLAUDE_CONFIG_DIR="+cd)
		}
	}
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash"
	}
	if err := ptysvc.SessionCreateDetached(name, []string{shell, "-l"}, env, cwd); err != nil {
		writeErr(w, 500, "failed to create session: "+err.Error())
		return
	}
	if r.sessionOwn != nil {
		_ = r.sessionOwn.Claim(name, user)
	}
	writeJSON(w, map[string]any{"ok": true, "name": name})
}

func (r *Router) handleCodeRestorePing(w http.ResponseWriter, req *http.Request) {
	user := auth.UserFrom(req)
	if user == "" {
		writeErr(w, 401, "unauthorized")
		return
	}
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	if strings.Contains(user, "..") || strings.ContainsAny(user, "/\\") {
		writeErr(w, 400, "bad user")
		return
	}
	dir := filepath.Join(r.cfg.DataDir, "users", user)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	_ = os.WriteFile(filepath.Join(dir, "code-restore.trigger"),
		[]byte(fmt.Sprintf("%d", time.Now().UnixNano())), 0o600)
	writeJSON(w, map[string]any{"ok": true})
}

func (r *Router) handleTerminalAssignSession(w http.ResponseWriter, req *http.Request) {
	caller, ok := r.mustPrimary(w, req)
	if !ok {
		return
	}
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	var body struct {
		Name   string `json:"name"`
		Target string `json:"target"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	name := ptysvc.SafeSessionName(body.Name)
	if name == "" {
		writeErr(w, 400, "name required")
		return
	}
	target := strings.TrimSpace(body.Target)
	if target != ptysvc.AudienceAll {
		r.cfgMu.Lock()
		valid := r.cfg.HasUser(target)
		r.cfgMu.Unlock()
		if !valid {
			writeErr(w, 400, "invalid target")
			return
		}
	}
	if err := r.sessionOwn.Assign(name, target); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	r.auditEvent(req, caller, "terminal.assign", name+" -> "+target)
	writeJSON(w, map[string]any{"status": "ok", "name": name, "assigned": target})
}

func (r *Router) handleTerminalScrollback(w http.ResponseWriter, req *http.Request) {
	user := auth.UserFrom(req)
	if user == "" {
		writeErr(w, 401, "unauthorized")
		return
	}
	name := req.URL.Query().Get("name")
	if name == "" {
		name = req.URL.Query().Get("tab")
	}
	if name == "" {
		writeErr(w, 400, "name required")
		return
	}
	if !ptysvc.OwnsSession(user, name, r.isPrimary(user), r.sessionOwn) {
		writeErr(w, 404, "session not found")
		return
	}
	lines := 5000
	if l := req.URL.Query().Get("lines"); l != "" {
		var n int
		if _, err := fmt.Sscanf(l, "%d", &n); err == nil && n > 0 {
			lines = n
		}
	}
	escapes := req.URL.Query().Get("plain") != "1"
	writeJSON(w, map[string]any{"data": ptysvc.SessionScrollback(user, name, lines, escapes)})
}

func (r *Router) handleTerminalRawLog(w http.ResponseWriter, req *http.Request) {
	user := auth.UserFrom(req)
	if user == "" {
		writeErr(w, 401, "unauthorized")
		return
	}
	name := strings.TrimSpace(req.URL.Query().Get("name"))
	if name == "" {
		writeErr(w, 400, "name required")
		return
	}
	if !ptysvc.OwnsSession(user, name, r.isPrimary(user), r.sessionOwn) {
		writeErr(w, 404, "session not found")
		return
	}
	requestedBytes := 0
	if b := req.URL.Query().Get("bytes"); b != "" {
		if n, err := strconv.Atoi(b); err == nil {
			requestedBytes = n
		}
	}
	data, total := ptysvc.SessionRawLogTail(user, name, requestedBytes)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Panel-Log-Total", strconv.Itoa(total))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (r *Router) handleTerminalHistory(w http.ResponseWriter, req *http.Request) {
	user := auth.UserFrom(req)
	if user == "" {
		writeErr(w, 401, "unauthorized")
		return
	}
	name := strings.TrimSpace(req.URL.Query().Get("name"))
	if name == "" {
		writeErr(w, 400, "name required")
		return
	}
	if !ptysvc.OwnsSession(user, name, r.isPrimary(user), r.sessionOwn) {
		writeErr(w, 404, "session not found")
		return
	}
	requestedBytes := 0
	if b := req.URL.Query().Get("bytes"); b != "" {
		if n, err := strconv.Atoi(b); err == nil {
			requestedBytes = n
		}
	}
	data, total := ptysvc.SessionHistory(user, name, requestedBytes)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Panel-Hist-Total", strconv.Itoa(total))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (r *Router) handleTerminalKillSession(w http.ResponseWriter, req *http.Request) {
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
		Name string `json:"name"`
		Tab  string `json:"tab"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	target := body.Name
	if target == "" {
		target = body.Tab
	}
	if !ptysvc.OwnsSession(user, target, r.isPrimary(user), r.sessionOwn) {
		writeErr(w, 404, "session not found")
		return
	}
	if err := ptysvc.SessionKill(target); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if err := r.sessionOwn.Release(target); err != nil {
		log.Printf("session ownership: release %s: %v", target, err)
	}
	r.auditEvent(req, user, "terminal.kill", target)
	writeJSON(w, map[string]string{"status": "ok"})
}
