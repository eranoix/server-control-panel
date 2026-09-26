package api

// handlers_users.go — user management + terminal sessions
// + handleExec (RCE, primary-only) + handleHostShell.
//
// handleExec, handleHostShell and touching users are all primary-only:
// grouped here as the "admin actions" only the host's owner may perform.
//
// Covers:
//   - handleExec (POST /api/exec — direct shell, vps-manager root)
//   - handleHostShell (GET /api/host-shell — PTY wrapper for the host shell)
//   - handleUsersList / Create / Delete / ResetPassword / Disable2FA
//     / RevokeSessions (admin UI)
//   - handleTerminalSessions / handleTerminalKillSession (kill by sid)
//
// Extracted from api.go.

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

// ---------------- User management ----------------

// userInfo is the shape exposed to the UI (never includes the password hash).
type userInfo struct {
	Username  string `json:"username"`
	IsPrimary bool   `json:"is_primary"`
	IsAdmin   bool   `json:"is_admin"` // primary OR the Admin flag — privilege parity
	HasTOTP   bool   `json:"has_totp"`
	Sessions  int    `json:"sessions"` // count of active JWT sessions
}

func (r *Router) handleUsersList(w http.ResponseWriter, req *http.Request) {
	if auth.UserFrom(req) == "" {
		writeErr(w, 401, "unauthorized")
		return
	}
	r.cfgMu.Lock()
	all := r.cfg.AllUsers()
	// FIX (v2): this used to be r.cfg.Username, which is empty after the
	// migration → is_primary always came out false in the UI. The canonical
	// primary in v2 is r.cfg.Primary.
	primaryName := r.cfg.Primary
	adminSet := map[string]bool{}
	for _, name := range r.cfg.Admins() {
		adminSet[name] = true
	}
	r.cfgMu.Unlock()
	// Count active sessions per user (when sessions are enabled)
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
			// The real MFA now lives in Supabase. SupabaseMFAEnabled is the local
			// mirror (updated on enroll-verify/disable/login). TOTPSecret is kept
			// as a fallback when it exists (legacy, pre-migration).
			HasTOTP:  u.SupabaseMFAEnabled || u.TOTPSecret != "",
			Sessions: sessionsPerUser[u.Username],
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
	// Sanitisation: only A-Z a-z 0-9 _ -
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
	// WhatsApp provisioning (v2): creates dirs, allocates a port, generates the
	// vault keys, renders compose/env, systemctl enable. Idempotent. An error is
	// logged but does not take the user create down (the config has already been
	// saved) — the operator re-runs Provision at boot or with a manual tool.
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
	// Guard: you cannot delete yourself
	if body.Username == caller {
		writeErr(w, 403, "cannot delete your own user (logged in right now)")
		return
	}
	// Protecting the primary: now that several admins can exist, an additional
	// admin may NOT delete the primary (it would break the legacy state
	// binding). RemoveUser rejects it too, but we give a clear 403 here.
	r.cfgMu.Lock()
	isPrimaryTarget := r.cfg.Primary != "" && r.cfg.Primary == body.Username
	r.cfgMu.Unlock()
	if isPrimaryTarget {
		writeErr(w, 403, "cannot delete the primary user")
		return
	}
	// Decommission WhatsApp BEFORE deleting from the config — that way the
	// Manager can still resolve the user in order to stop the container and clean
	// the vault. On failure the user stays in the config and the operator can
	// retry; it prevents the "user gone from the config but container orphaned"
	// state.
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
	// Revoke every local session of the deleted user.
	if r.auth.Sessions() != nil {
		r.auth.Sessions().RevokeAllExcept(body.Username, "")
	}
	// Close the Supabase login path: with no entry in uuid_map, verifySupabase
	// returns ErrSupabaseInvalidCredentials before ever reaching GoTrue. Without
	// this, a deleted user would go on logging in whenever the Supabase backend
	// is "supabase" or "both" (found in an audit).
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

// handleUserSetAdmin grants or revokes a user's administrator privileges (full
// parity with the primary). Gated by mustPrimary: only an admin can create or
// remove other admins. The primary is always an admin — trying to revoke it
// returns 422 (SetAdmin refuses). Persists to the config and updates the
// in-memory state atomically under cfgMu.
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
	// BUG fix: this used to be `if !ok` (ok = mustPrimary) — always false here,
	// so SetPassword returning false (the user does not exist) silently
	// answered 200 without changing anything. Now the correct feedback gets out.
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

// handleTerminalSessions lists only the authenticated user's sessions. Tenant
// boundary: each profile sees only its own sessions (the vpsm-<user>- prefix).
// Other profiles' sessions are invisible in the UI — not "listed but not
// attachable", genuinely invisible.
func (r *Router) handleTerminalSessions(w http.ResponseWriter, req *http.Request) {
	user := auth.UserFrom(req)
	if user == "" {
		writeErr(w, 401, "unauthorized")
		return
	}
	// ?all=1 + admin → an annotated master view (every session plus its owner in
	// "assigned"), used by the session manager. sanitizeList preserves the
	// "assigned" field (it only drops items with no "name", and dedups). For a
	// non-admin the ?all=1 is ignored: it falls into the normal filtered path
	// (the "who it shows up for").
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
		writeJSON(w, []any{}) // graceful: no engine or no sessions -> empty list
		return
	}
	writeJSON(w, sanitizeList(r.enrichSessionsWithAgent(sessions), "name"))
}

// enrichSessionsWithAgent merges the agent's state (state ●/○, cwd, cost,
// tokens, last activity) — data already kept in session-status.json +
// session-cwd.json, which the agents tab uses — into each row of the session
// list, so the session manager becomes a cockpit without duplicating telemetry.
// Mutates in place; the extra keys survive sanitizeList (which only filters out
// entries with no "name").
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

// handleTerminalCreate creates a DETACHED dtach session with the chosen
// cwd/command/account (POST /api/terminal/create) — the structured "New
// session" form. Creating one through the WS (/ws/shell?name=) always starts in
// $HOME; this endpoint lets you pick the directory and the Claude account
// BEFORE the shell comes up. The frontend then opens a pane attaching to the
// already-created session (and injects the command via startupCmd).
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
	// Already alive? Idempotent: answer ok (the frontend just attaches).
	if alive, _ := ptysvc.SessionHas(name); alive {
		writeJSON(w, map[string]any{"ok": true, "name": name, "existed": true})
		return
	}
	// Per-user quota (same rule as the WS): only live sessions count.
	if n := ptysvc.OwnedSessionCountLive(r.sessionOwn, user); n >= ptysvc.MaxSessionsPerUser {
		writeErr(w, 429, "session limit reached — close one first")
		return
	}
	// cwd: check it exists and is a directory (best effort; empty = the shell's default).
	cwd := strings.TrimSpace(body.CWD)
	if cwd != "" {
		if fi, err := os.Stat(cwd); err != nil || !fi.IsDir() {
			writeErr(w, 400, "invalid cwd")
			return
		}
	}
	// Optional Claude account → set the assignment and inject CLAUDE_CONFIG_DIR.
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
	// Ownership to the creator (same semantics as the WS: a new session belongs to the user).
	if r.sessionOwn != nil {
		_ = r.sessionOwn.Claim(name, user)
	}
	writeJSON(w, map[string]any{"ok": true, "name": name})
}

// handleCodeRestorePing writes the trigger file the code-server extension
// watches (fs.watch) in order to reopen the code-server sessions on every
// (re)load of the iframe. Needed because code-server's native restore fails on
// reload and the extension's activate() does NOT fire again (the extension host
// is persistent). The frontend calls this from onVscodeFrameLoad. Contents = a
// timestamp (it changes on every ping → fs.watch fires and the extension runs
// restoreOpenSessions). Best effort.
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

// handleTerminalAssignSession reattributes a session's audience:
// who sees it in their day-to-day picker. Admin-only (mustPrimary). Target is a
// valid username (the session becomes private to them) or "*" (AudienceAll —
// visible to everyone). The session's name is sanitised the same way the
// backend keys ownership, so the assign lands on the right entry. We do not
// require the session to currently exist: assigning ahead of (re)create
// is harmless and the entry is inert until a session by that name lives.
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

// handleTerminalKillSession kills a single session by full name.
// Accepts {"name": "..."} (preferred) or {"tab": "..."} (legacy alias).
// handleTerminalScrollback returns the pane history (scrollback + visible
// screen, with colors) for a session the caller owns. The mobile hterm fetches
// this on first attach to prime its scrollback — `dtach -A` only
// redraws the visible screen, so without priming the client can only scroll the
// rows it received since connecting ("it only loads part of it"). Ownership is
// enforced the same way as kill/rename: 404 (not 403) so existence never leaks.
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
	// plain=1 → text with no escapes (for "copy everything" to the clipboard).
	// The default keeps the colours (-e) to prime hterm's display on attach.
	escapes := req.URL.Query().Get("plain") != "1"
	// The engine is dtach: reading means tailing the pty log (session.go).
	writeJSON(w, map[string]any{"data": ptysvc.SessionScrollback(user, name, lines, escapes)})
}

// handleTerminalRawLog returns the RAW BYTES of the session's pty log — escapes
// and all — so the client can prime its own emulator when attaching.
//
// ## Why raw, and why the dashboard needs it
//
// `/api/terminal/scrollback` cuts by lines and knows how to strip escapes. Both
// of those destroy the output of a program that redraws: the same log yields
// 511 shredded lines as plain text and 5,058 readable ones when the bytes reach
// an emulator intact. What knows how to assemble that is a terminal, and the
// dashboard has one (xterm.js) — the same reasoning that led the app to ask for
// this.
//
// The dashboard used to depend on the replay the SERVER decides to send on
// attach, and that replay is skipped on exactly the sessions that matter:
// measured across the 28 logs on this machine, every working session has a
// repainted stream and receives nothing. The result was opening the terminal on
// another computer and seeing a single blank page.
//
// Ownership is checked as in kill/rename: 404, never 403 — the existence of
// someone else's session must not leak. It weighs more here, because the raw
// log is the literal transcript of everything that went through the terminal.
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
	bytesPedidos := 0
	if b := req.URL.Query().Get("bytes"); b != "" {
		if n, err := strconv.Atoi(b); err == nil {
			bytesPedidos = n
		}
	}
	data, total := ptysvc.SessionRawLogTail(user, name, bytesPedidos)
	// It goes out as octet-stream and not as JSON/base64: the dashboard writes
	// these bytes straight into xterm, and putting them through base64 would only
	// cost a third more bandwidth plus a decode on the side that is already busy
	// painting.
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Vpsm-Log-Total", strconv.Itoa(total))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// handleTerminalHistorico returns the session's RENDERED history: the lines
// that have already scrolled off the screen, as append-only text.
//
// It is what the dashboard writes into xterm when opening the session. The
// difference from /log-bruto is not one of format, but of nature:
//
//	raw log  = everything that went over the wire, the drawing in progress
//	           included
//	history  = what the person SAW, once each
//
// Replaying the raw log onto a fresh grid duplicates content — the `ESC[nA` of
// a program that repaints saturates at the top of the SCREEN and never reaches
// the scrollback, so the earlier copy stays put. The history does not have that
// problem because it is not a replay: the server already rendered it, live, on
// a screen the size of the session.
//
// Empty is a legitimate answer: a new session, or one that has not yet scrolled
// a single line off. The dashboard falls back to /log-bruto in that case.
func (r *Router) handleTerminalHistorico(w http.ResponseWriter, req *http.Request) {
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
	bytesPedidos := 0
	if b := req.URL.Query().Get("bytes"); b != "" {
		if n, err := strconv.Atoi(b); err == nil {
			bytesPedidos = n
		}
	}
	data, total := ptysvc.HistoricoDaSessao(user, name, bytesPedidos)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Vpsm-Hist-Total", strconv.Itoa(total))
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
	// 404 (not 403) when the target does not belong to the user: existence does not leak.
	if !ptysvc.OwnsSession(user, target, r.isPrimary(user), r.sessionOwn) {
		writeErr(w, 404, "session not found")
		return
	}
	if err := ptysvc.SessionKill(target); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	// Release ownership: a future create with the same name belongs to
	// whoever attaches first (primary on legacy adoption, the explicit
	// claimer otherwise). A stale entry would just sit there harmlessly,
	// but cleaning up keeps the registry honest.
	if err := r.sessionOwn.Release(target); err != nil {
		log.Printf("session ownership: release %s: %v", target, err)
	}
	r.auditEvent(req, user, "terminal.kill", target)
	writeJSON(w, map[string]string{"status": "ok"})
}
