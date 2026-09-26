package api

// handlers_recovery.go — the /recovery emergency flow
//
// A UI independent of the SPA. If index.html breaks (JS, Alpine, Tailwind),
// /recovery still gives you a terminal + basic actions (rollback, restart).
// Dedicated auth (separate cookie, separate secret, without touching the
// primary lockout). Recovery vault codes live in the config (vpsmctl
// reset-totp/recovery-totp restores them when the user loses their apps).
//
// Covers: handleRecoveryPage / Auth / Term / PTY / Action / Logout +
// the issueRecoverySession and recoveryUserFromCookie helpers.

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

// ============================================================================
// /recovery — emergency UI independent from the SPA
//
// Threat model: the primary index.html SPA crashed (JS error, Alpine bug,
// bad template, missing Tailwind class). Backend Go is still up. We need to
// expose a terminal + key actions (rollback, restart) without depending on
// any of the SPA code, classes, or assets that could be broken.
//
// Auth is INTENTIONALLY separate from the primary login:
//   - Primary 2FA secret cannot unlock recovery (and vice-versa)
//   - Session cookie name + kind claim are different
//   - Failed login here doesn't increment primary lockout counters
//
// Recovery codes vault: vpsmctl reset-totp + vpsmctl reset-recovery-totp are
// the last line of defense if you lose both authenticator apps.
// ============================================================================

const recoveryCookieName = "vpsm_recovery_token"
const recoveryCookieUser = "vpsm_recovery_user" // non-HttpOnly, JS reads to show username in UI

func (r *Router) handleRecoveryPage(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		writeErr(w, 405, "method not allowed")
		return
	}
	// If already authenticated for recovery, jump straight to the terminal.
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

// handleRecoveryAuth: the /recovery flow's login — user + local password +
// recovery TOTP, returning a session cookie (path=/recovery, 30min TTL).
//
// Inline first-time enrolment was REMOVED for security: the bootstrap let
// anyone who knew the local bcrypt password (with no Supabase MFA check)
// generate a recovery TOTP out of thin air and escalate to a root
// shell. A recovery TOTP now has to be pre-enrolled from the console
// (`vpsmctl reset-recovery-totp <user>`).
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
	// App-only gate: /recovery is an entrance to the DASHBOARD (an emergency UI
	// with a root PTY), independent of /api/auth/login. Without this check the
	// gate would have a back door — whoever was refused at login would come in
	// through here. Same generic invalid-credential message the handler already
	// uses for a wrong password; the real reason goes only to the audit log.
	if r.userIsAppOnly(body.Username) {
		r.auditEvent(req, body.Username, "recovery.login.denied.app_only", "recovery blocked for an app-only account")
		writeErr(w, 401, "invalid credentials")
		return
	}
	// Same lockout pool — credential stuffing attempts on recovery shouldn't
	// bypass primary rate-limiting.
	if ok, until := r.lockout.Allowed(body.Username); !ok {
		retry := int(time.Until(until).Seconds())
		if retry < 1 {
			retry = 1
		}
		w.Header().Set("Retry-After", strconv.Itoa(retry))
		r.auditEvent(req, body.Username, "recovery.login.locked", "")
		writeErr(w, 423, "conta temporariamente bloqueada; tente em "+strconv.Itoa(retry)+"s")
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

	// SECURITY: on-the-fly enrolment through /recovery has been removed.
	// The original bootstrap let anyone holding the local bcrypt password
	// (which does NOT check Supabase MFA) register a recovery TOTP out of
	// thin air and open an escalation path. A recovery TOTP now has to be
	// enrolled beforehand, from the console:
	//
	//   vpsmctl reset-recovery-totp <user>
	//
	// Anyone who can run vpsmctl is already root on the host — the physical
	// access gate replaces the Supabase MFA gate that /recovery lacks.
	if !hasStored {
		r.auditEvent(req, body.Username, "recovery.login.fail", "no_recovery_totp_enrolled")
		writeErr(w, 403, "recovery TOTP not enrolled — run 'vpsmctl reset-recovery-totp "+body.Username+"' on the console")
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

// issueRecoverySession centralises issuing the cookie + resetting the
// lockout/ratelimit. Used both by the normal login and by the enrolment confirm.
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
		HttpOnly: false, // UI reads to display username
		Secure:   secureCookie,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   30 * 60,
	})
	r.auditEvent(req, username, auditAction, "")
	writeJSON(w, map[string]any{"ok": true})
}

// recoveryUserFromCookie extracts and verifies the recovery session cookie.
// Returns (username, true) on valid + non-expired session.
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
	// HEAD is the "is my recovery session still valid?" probe. The WS upgrade is
	// refused BEFORE it becomes a WebSocket when the cookie has expired, and a
	// refused upgrade reaches the browser as close 1006 — the same code as a
	// network drop. With no way to tell the two apart, the client would reconnect
	// in a loop forever, saying "no connection" over a perfect network. With
	// HEAD it can ask: 200 = the session is alive (it was the network),
	// 302 to /recovery = it has expired (authenticate again). net/http discards
	// the body on HEAD, so the probe costs only the headers.
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
	// Reuses the same HostShell that the primary terminal uses — a recovery
	// session is the same OS-level capability, just gated by a stricter
	// (independent) auth path.
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
	// Body is ignored — kept for forward compat with the previous TOTP-per-action
	// flow. The recovery session cookie (30min, HttpOnly, kind="recovery", set
	// only after password + recovery TOTP validation in handleRecoveryAuth) is
	// the credential boundary. Re-prompting TOTP per click was friction without
	// real security gain: the session is short-lived AND already 2FA-gated.
	_, _ = io.Copy(io.Discard, io.LimitReader(req.Body, 1<<10))

	var output string
	var execErr error
	switch action {
	case "rollback":
		out, err := exec.CommandContext(req.Context(), "/usr/local/bin/vpsmctl", "rollback").CombinedOutput()
		output = string(out)
		execErr = err
	case "restart":
		out, err := exec.CommandContext(req.Context(), "/usr/bin/systemctl", "restart", "vps-manager").CombinedOutput()
		output = string(out)
		execErr = err
	case "health":
		out, err := exec.CommandContext(req.Context(), "/usr/local/bin/vpsmctl", "health").CombinedOutput()
		output = string(out)
		execErr = err
	case "claude-up":
		// Starts the standalone Claude container. It is idempotent:
		// if it already exists, it just starts it. There is a button on the tab
		// because in an emergency the container may have been stopped by the very
		// problem you are trying to fix.
		// The manager is EMBEDDED in the binary and is materialised now, so it
		// cannot go missing because of a branch or a working tree — which is
		// exactly what broke this button on the first real attempt.
		cmd, err := recoveryclaude.Comando(r.cfg.DataDir, "up")
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

// ============================================================================
// Recovery Claude — an independent connection
//
// On the host, every `claude` goes through claude-router (ANTHROPIC_BASE_URL
// points at 127.0.0.1:8788). The router is one more service in the path, and
// one more service in the path is one more thing that can be broken exactly
// when you fall back to this screen. This Claude runs in its OWN container,
// alongside vps-manager, with no ANTHROPIC_BASE_URL and with a login of its
// own: neither the router, nor the host's Claude installation, nor the
// vps-manager process is part of the equation.
//
// The container is brought up by Docker (restart=always), not by us — so it is
// already on its feet before anything of ours runs.
// ============================================================================

// The container's name comes from the package that carries the assets — one truth only.
const recoveryClaudeContainer = recoveryclaude.Container

// handleRecoveryClaudeStatus answers what the tab needs to know BEFORE trying
// to open a terminal: does the container exist? is it running? does it already
// have its own login? Without this the tab would open a WebSocket that dies
// with no explanation — exactly the kind of silence an emergency screen cannot
// afford.
func (r *Router) handleRecoveryClaudeStatus(w http.ResponseWriter, req *http.Request) {
	if _, ok := r.recoveryUserFromCookie(req); !ok {
		writeErr(w, 401, "unauthorized")
		return
	}
	// `container inspect`, not `inspect`: the second one matches IMAGES too, and
	// the image has exactly the same name as the container. Docker happens to
	// resolve the container first today, but that is luck, not a contract — the
	// same ambiguity has already made the startup script try `docker start` on a
	// container that did not exist.
	estado, _ := exec.CommandContext(req.Context(), "/usr/bin/docker", "container", "inspect",
		"-f", "{{.State.Status}}", recoveryClaudeContainer).Output()
	rodando := strings.TrimSpace(string(estado)) == "running"
	resp := map[string]any{
		"ok":        true,
		"existe":    len(strings.TrimSpace(string(estado))) > 0,
		"rodando":   rodando,
		"container": recoveryClaudeContainer,
	}
	if rodando {
		// A login of its own: that is what separates "independent" from "borrows
		// the host's credential". Without it `claude` opens by asking for
		// authentication, and the tab has to say so beforehand, not afterwards.
		autenticado := exec.CommandContext(req.Context(), "/usr/bin/docker", "exec",
			recoveryClaudeContainer, "test", "-f", "/config/.credentials.json").Run() == nil
		resp["autenticado"] = autenticado
		if v, err := exec.CommandContext(req.Context(), "/usr/bin/docker", "exec",
			recoveryClaudeContainer, "claude", "--version").Output(); err == nil {
			resp["versao"] = strings.TrimSpace(string(v))
		}
	}
	writeJSON(w, resp)
}

// handleRecoveryClaudePTY attaches the tab's terminal to a NAMED `dtach`
// session inside the recovery container.
//
// It became dtach — the same engine as the main terminal — so the product does
// not carry TWO multiplexers. A multiplexer is vocabulary: two of them mean two
// ways for a session to be born, to die, to redraw and to fail, and whoever
// debugs it has to know which one they are in.
//
// A multiplexer (rather than a loose shell) because on a bad network
// reconnection is the rule: the conversation with Claude has to carry on from
// where it stopped.
func (r *Router) handleRecoveryClaudePTY(w http.ResponseWriter, req *http.Request) {
	user, ok := r.recoveryUserFromCookie(req)
	if !ok {
		writeErr(w, 401, "unauthorized")
		return
	}
	if !r.dockerReady(w) {
		return
	}
	// The check comes BEFORE the upgrade: a WebSocket that opens and closes has
	// no way to explain why, and here the reason is actionable ("start the container").
	// `container inspect`, not `inspect`: the second one matches IMAGES too, and
	// the image has exactly the same name as the container. Docker happens to
	// resolve the container first today, but that is luck, not a contract — the
	// same ambiguity has already made the startup script try `docker start` on a
	// container that did not exist.
	estado, _ := exec.CommandContext(req.Context(), "/usr/bin/docker", "container", "inspect",
		"-f", "{{.State.Status}}", recoveryClaudeContainer).Output()
	if strings.TrimSpace(string(estado)) != "running" {
		writeErr(w, 409, "container "+recoveryClaudeContainer+" is not running")
		return
	}
	r.auditEvent(req, user, "recovery.claude.open", "")
	// `dtach -A` attaches if it already exists and creates it if not — exactly
	// the same contract (and the same flag) as the main terminal. The welcome
	// message runs on each creation (not on each attach), so it guides without
	// turning into repeated noise.
	//
	// `-E` disables dtach's detach key (^\), which would be a trap here: somebody
	// on a RECOVERY screen must not lose the session to an accidental keystroke.
	// `-z` disables suspend (^Z) for the same reason.
	ptysvc.ContainerExec(w, req, r.docker.Raw(), recoveryClaudeContainer, []string{
		"/bin/bash", "-lc",
		"dtach -A /tmp/recovery.sock -E -z bash -lc '/usr/local/bin/bemvindo.sh; exec bash -l'",
	}, []string{"CLAUDE_CONFIG_DIR=/config", "VPSM_RECOVERY=1"})
}

// recoveryTetoAbsoluto caps how long a recovery session can go on being
// renewed, counted from the LOGIN — not from the last renewal.
const recoveryTetoAbsoluto = 8 * time.Hour

// handleRecoveryRenew extends the recovery session while work is actually
// happening.
//
// The session lasts 30 minutes because it is a privileged path with its own
// authentication, and keeping it short was a conscious decision. But expiring
// in the MIDDLE of a repair — which is what happened in the first real session,
// with the operator looking at the screen and the terminal alive — is the worst
// possible moment to demand password + TOTP again.
//
// The balance: it renews while the tab is in use, but the absolute cap since
// login still holds (the `ini` claim is not restarted). Somebody who is working
// is not interrupted; a tab forgotten on a screen still dies.
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
	inicio, err := r.auth.RecoveryTokenStart(c.Value)
	if err != nil {
		writeErr(w, 401, "unauthorized")
		return
	}
	if restante := recoveryTetoAbsoluto - time.Since(inicio); restante <= 0 {
		// Deliberately does NOT renew: the cap exists so that a privileged
		// session does not become permanent just because the tab was left open.
		r.auditEvent(req, user, "recovery.renew.teto", "")
		writeErr(w, 403, "recovery session hit the limit of "+recoveryTetoAbsoluto.String()+" — authenticate again")
		return
	}
	tok, err := r.auth.IssueRecoveryTokenFrom(user, 30*time.Minute, inicio)
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
		"ok": true,
		// How much of the cap is left — the screen warns before it runs out,
		// instead of letting the operator find out by being disconnected.
		"restante_seg": int((recoveryTetoAbsoluto - time.Since(inicio)).Seconds()),
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
