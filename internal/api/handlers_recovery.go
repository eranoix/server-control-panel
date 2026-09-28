package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/pquerna/otp/totp"

	"server-control-panel/internal/auth"
	ptysvc "server-control-panel/internal/pty"
	"server-control-panel/internal/recoveryclaude"
)

const recoveryCookieName = "panel_recovery_token"
const recoveryCookieUser = "panel_recovery_user"

func (r *Router) handleRecoveryPage(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		writeErr(w, 405, "method not allowed")
		return
	}
	if _, ok := r.recoveryUserFromCookie(req); ok {
		http.Redirect(w, req, "/recovery/term", http.StatusFound)
		return
	}
	html, err := webFS.ReadFile("web/recovery.html")
	if err != nil {
		writeErr(w, 500, "recovery.html missing from embed: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Frame-Options", "DENY")
	_, _ = w.Write(html)
}

func (r *Router) handleRecoveryAuth(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	ip := auth.ClientIP(req)
	if !r.limiter.Allow(ip) {
		writeErr(w, 429, "too many attempts, try again in a few seconds")
		return
	}
	var body struct {
		Username        string `json:"username"`
		Password        string `json:"password"`
		TOTP            string `json:"totp"`
		EnrollingSecret string `json:"enrolling_secret"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	body.Username = strings.TrimSpace(body.Username)
	body.TOTP = strings.TrimSpace(body.TOTP)
	body.EnrollingSecret = strings.TrimSpace(body.EnrollingSecret)
	if body.Username == "" || body.Password == "" {
		writeErr(w, 400, "username and password are required")
		return
	}
	if r.userIsAppOnly(body.Username) {
		r.auditEvent(req, body.Username, "recovery.login.denied.app_only", "recovery blocked for an app-only account")
		writeErr(w, 401, "invalid credentials")
		return
	}
	if ok, until := r.lockout.Allowed(body.Username); !ok {
		retry := int(time.Until(until).Seconds())
		if retry < 1 {
			retry = 1
		}
		w.Header().Set("Retry-After", strconv.Itoa(retry))
		r.auditEvent(req, body.Username, "recovery.login.locked", "")
		writeErr(w, 423, "account temporarily locked; try again in "+strconv.Itoa(retry)+"s")
		return
	}
	if !r.auth.Verify(body.Username, body.Password) {
		r.lockout.RecordFailure(body.Username)
		r.auditEvent(req, body.Username, "recovery.login.fail", "bad_password")
		writeErr(w, 401, "invalid credentials")
		return
	}
	storedSecret, hasStored := r.cfg.RecoveryTOTPSecretFor(body.Username)
	hasStored = hasStored && storedSecret != ""

	if !hasStored {
		r.auditEvent(req, body.Username, "recovery.login.fail", "no_recovery_totp_enrolled")
		writeErr(w, 403, "recovery TOTP not enrolled — run 'panelctl reset-recovery-totp "+body.Username+"' on the console")
		return
	}

	if body.TOTP == "" {
		writeErr(w, 400, "TOTP code is required")
		return
	}
	if !totp.Validate(body.TOTP, storedSecret) {
		r.lockout.RecordFailure(body.Username)
		r.auditEvent(req, body.Username, "recovery.login.fail", "bad_totp")
		writeErr(w, 401, "invalid recovery TOTP code")
		return
	}
	r.issueRecoverySession(w, req, body.Username, ip, "recovery.login.ok")
}

func (r *Router) issueRecoverySession(w http.ResponseWriter, req *http.Request, username, ip, auditAction string) {
	r.limiter.Reset(ip)
	r.lockout.RecordSuccess(username)
	tok, err := r.auth.IssueRecoveryToken(username, 30*time.Minute)
	if err != nil {
		writeErr(w, 500, "issue: "+err.Error())
		return
	}
	secureCookie := req.TLS != nil
	http.SetCookie(w, &http.Cookie{
		Name:     recoveryCookieName,
		Value:    tok,
		Path:     "/recovery",
		HttpOnly: true,
		Secure:   secureCookie,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   30 * 60,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     recoveryCookieUser,
		Value:    username,
		Path:     "/recovery",
		HttpOnly: false,
		Secure:   secureCookie,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   30 * 60,
	})
	r.auditEvent(req, username, auditAction, "")
	writeJSON(w, map[string]any{"ok": true})
}

func (r *Router) recoveryUserFromCookie(req *http.Request) (string, bool) {
	c, err := req.Cookie(recoveryCookieName)
	if err != nil || c.Value == "" {
		return "", false
	}
	user, err := r.auth.VerifyRecoveryToken(c.Value)
	if err != nil || user == "" {
		return "", false
	}
	return user, true
}

func (r *Router) handleRecoveryTerm(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		writeErr(w, 405, "method not allowed")
		return
	}
	if _, ok := r.recoveryUserFromCookie(req); !ok {
		http.Redirect(w, req, "/recovery", http.StatusFound)
		return
	}
	html, err := webFS.ReadFile("web/recovery-term.html")
	if err != nil {
		writeErr(w, 500, "recovery-term.html missing: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Frame-Options", "DENY")
	_, _ = w.Write(html)
}

func (r *Router) handleRecoveryPTY(w http.ResponseWriter, req *http.Request) {
	user, ok := r.recoveryUserFromCookie(req)
	if !ok {
		writeErr(w, 401, "unauthorized")
		return
	}
	r.auditEvent(req, user, "recovery.pty.open", "")
	ptysvc.HostShell(w, req, user, r.isPrimary(user), r.sessionOwn, r.terminalConfigDir(), r.cfg.DataDir, r.sessReg)
}

func (r *Router) handleRecoveryAction(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	user, ok := r.recoveryUserFromCookie(req)
	if !ok {
		writeErr(w, 401, "unauthorized")
		return
	}
	const prefix = "/recovery/action/"
	if !strings.HasPrefix(req.URL.Path, prefix) {
		writeErr(w, 404, "not found")
		return
	}
	action := strings.TrimPrefix(req.URL.Path, prefix)
	_, _ = io.Copy(io.Discard, io.LimitReader(req.Body, 1<<10))

	var output string
	var execErr error
	switch action {
	case "rollback":
		out, err := exec.CommandContext(req.Context(), "/usr/local/bin/panelctl", "rollback").CombinedOutput()
		output = string(out)
		execErr = err
	case "restart":
		out, err := exec.CommandContext(req.Context(), "/usr/bin/systemctl", "restart", "server-control-panel").CombinedOutput()
		output = string(out)
		execErr = err
	case "health":
		out, err := exec.CommandContext(req.Context(), "/usr/local/bin/panelctl", "health").CombinedOutput()
		output = string(out)
		execErr = err
	case "claude-up":
		cmd, err := recoveryclaude.Command(r.cfg.DataDir, "up")
		if err != nil {
			output = "could not prepare the container manager: " + err.Error()
			execErr = err
			break
		}
		out, err := cmd.CombinedOutput()
		output = string(out)
		execErr = err
	default:
		writeErr(w, 404, "unknown action")
		return
	}
	r.auditEvent(req, user, "recovery.action."+action, fmt.Sprintf("err=%v", execErr))
	if execErr != nil {
		writeJSON(w, map[string]any{"ok": false, "error": execErr.Error(), "output": output})
		return
	}
	writeJSON(w, map[string]any{"ok": true, "message": action + " ok", "output": output})
}

const recoveryClaudeContainer = recoveryclaude.Container

func (r *Router) handleRecoveryClaudeStatus(w http.ResponseWriter, req *http.Request) {
	if _, ok := r.recoveryUserFromCookie(req); !ok {
		writeErr(w, 401, "unauthorized")
		return
	}
	state, _ := exec.CommandContext(req.Context(), "/usr/bin/docker", "container", "inspect",
		"-f", "{{.State.Status}}", recoveryClaudeContainer).Output()
	running := strings.TrimSpace(string(state)) == "running"
	resp := map[string]any{
		"ok":        true,
		"exists":    len(strings.TrimSpace(string(state))) > 0,
		"running":   running,
		"container": recoveryClaudeContainer,
	}
	if running {
		authed := exec.CommandContext(req.Context(), "/usr/bin/docker", "exec",
			recoveryClaudeContainer, "test", "-f", "/config/.credentials.json").Run() == nil
		resp["authenticated"] = authed
		if v, err := exec.CommandContext(req.Context(), "/usr/bin/docker", "exec",
			recoveryClaudeContainer, "claude", "--version").Output(); err == nil {
			resp["version"] = strings.TrimSpace(string(v))
		}
	}
	writeJSON(w, resp)
}

func (r *Router) handleRecoveryClaudePTY(w http.ResponseWriter, req *http.Request) {
	user, ok := r.recoveryUserFromCookie(req)
	if !ok {
		writeErr(w, 401, "unauthorized")
		return
	}
	if !r.dockerReady(w) {
		return
	}
	state, _ := exec.CommandContext(req.Context(), "/usr/bin/docker", "container", "inspect",
		"-f", "{{.State.Status}}", recoveryClaudeContainer).Output()
	if strings.TrimSpace(string(state)) != "running" {
		writeErr(w, 409, "container "+recoveryClaudeContainer+" is not running")
		return
	}
	r.auditEvent(req, user, "recovery.claude.open", "")
	ptysvc.ContainerExec(w, req, r.docker.Raw(), recoveryClaudeContainer, []string{
		"/bin/bash", "-lc",
		"dtach -A /tmp/recovery.sock -E -z bash -lc '/usr/local/bin/welcome.sh; exec bash -l'",
	}, []string{"CLAUDE_CONFIG_DIR=/config", "PANEL_RECOVERY=1"})
}

const recoveryHardCap = 8 * time.Hour

func (r *Router) handleRecoveryRenew(w http.ResponseWriter, req *http.Request) {
	user, ok := r.recoveryUserFromCookie(req)
	if !ok {
		writeErr(w, 401, "unauthorized")
		return
	}
	c, err := req.Cookie(recoveryCookieName)
	if err != nil {
		writeErr(w, 401, "unauthorized")
		return
	}
	start, err := r.auth.RecoveryTokenStart(c.Value)
	if err != nil {
		writeErr(w, 401, "unauthorized")
		return
	}
	if rest := recoveryHardCap - time.Since(start); rest <= 0 {
		r.auditEvent(req, user, "recovery.renew.ceiling", "")
		writeErr(w, 403, "recovery session hit the limit of "+recoveryHardCap.String()+" — authenticate again")
		return
	}
	tok, err := r.auth.IssueRecoveryTokenFrom(user, 30*time.Minute, start)
	if err != nil {
		writeErr(w, 500, "issue: "+err.Error())
		return
	}
	secureCookie := req.TLS != nil
	http.SetCookie(w, &http.Cookie{
		Name: recoveryCookieName, Value: tok, Path: "/recovery",
		HttpOnly: true, Secure: secureCookie, SameSite: http.SameSiteLaxMode, MaxAge: 30 * 60,
	})
	http.SetCookie(w, &http.Cookie{
		Name: recoveryCookieUser, Value: user, Path: "/recovery",
		HttpOnly: false, Secure: secureCookie, SameSite: http.SameSiteLaxMode, MaxAge: 30 * 60,
	})
	writeJSON(w, map[string]any{
		"ok":            true,
		"remaining_sec": int((recoveryHardCap - time.Since(start)).Seconds()),
	})
}

func (r *Router) handleRecoveryLogout(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost && req.Method != http.MethodGet {
		writeErr(w, 405, "method not allowed")
		return
	}
	if user, ok := r.recoveryUserFromCookie(req); ok {
		r.auditEvent(req, user, "recovery.logout", "")
	}
	http.SetCookie(w, &http.Cookie{Name: recoveryCookieName, Value: "", Path: "/recovery", MaxAge: -1})
	http.SetCookie(w, &http.Cookie{Name: recoveryCookieUser, Value: "", Path: "/recovery", MaxAge: -1})
	writeJSON(w, map[string]any{"ok": true})
}
