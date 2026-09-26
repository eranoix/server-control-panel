package api

// handlers_claude_accounts.go — per-consumer Claude account selector.
//
// "Switching Claude accounts" = switching the CLAUDE_CONFIG_DIR each consumer
// uses on spawn (the account is client-side, minted by Claude Code from the
// config dir's credential; claude-router is passthrough in oauth mode and does
// not know which account it is). This file exposes:
//
//   - jobsConfigDir / terminalConfigDir / forkConfigDir — nil-safe resolvers
//     used by the 3 spawn seams (jiraai, interactive pty, fork pty).
//   - GET  /api/claude/accounts        — table of consumers + accounts + login
//   - POST /api/claude/accounts/assign — assigns consumer→account
//   - POST /api/claude/accounts/login-terminal — opens a session for OAuth login
//
// Every endpoint is gated by mustPrimary (admin) and audited.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"server-control-panel/internal/claudeacct"
	ptysvc "server-control-panel/internal/pty"
)

// uuidRe matches the UUID format Claude Code uses for conversation files.
// Validated before using the value in send-keys to prevent shell injection.
var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// swapAccountLabel returns a safe display label for use in shell printf banners.
// accountID is already validated by SetSessionAccount (allowlist), but we strip
// anything outside [a-zA-Z0-9_-] to be defensive about future changes.
func swapAccountLabel(accountID string) string {
	var b strings.Builder
	for _, r := range accountID {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "padrao"
	}
	return b.String()
}

// jobsConfigDir returns the CLAUDE_CONFIG_DIR for the jobs consumer,
// or "" to inherit the default account ($HOME/.claude). nil-safe: a degraded
// boot (claudeAccts==nil) keeps the earlier behaviour.
func (r *Router) jobsConfigDir() string {
	if r.claudeAccts == nil {
		return ""
	}
	return r.claudeAccts.ConfigDirFor(claudeacct.ConsumerJobs)
}

// terminalConfigDir resolves the CLAUDE_CONFIG_DIR for interactive Claude
// panels (the "terminal" consumer). "" → inherit default.
func (r *Router) terminalConfigDir() string {
	if r.claudeAccts == nil {
		return ""
	}
	return r.claudeAccts.ConfigDirFor(claudeacct.ConsumerTerminal)
}

// forkConfigDir resolves the CLAUDE_CONFIG_DIR for fork/restart/jira-work
// sessions (the "fork" consumer). "" → inherit default.
func (r *Router) forkConfigDir() string {
	if r.claudeAccts == nil {
		return ""
	}
	return r.claudeAccts.ConfigDirFor(claudeacct.ConsumerFork)
}

// terminalConfigDirForSession resolves the CLAUDE_CONFIG_DIR for a specific
// sessions, checking session-level overrides before the consumer-global setting.
func (r *Router) terminalConfigDirForSession(session string) string {
	if r.claudeAccts == nil {
		return ""
	}
	return r.claudeAccts.ConfigDirForSession(session, claudeacct.ConsumerTerminal)
}

// consumerMeta is the display copy for each class-(A) consumer row. Kept
// server-side so the table renders one source of truth.
var consumerMeta = map[string][2]string{
	claudeacct.ConsumerJobs:     {"Jobs (Jira analysis)", "claude -p during ticket analysis/refinement — includes detached jobs"},
	claudeacct.ConsumerTerminal: {"Interactive terminal", "terminal panes where you run claude by hand"},
	claudeacct.ConsumerFork:     {"Fork / Work on it now", "claude sessions created by fork, restart and 'Work on it now' in Jira"},
}

// handleClaudeAccounts (GET) returns the account-selector table: the registry
// of accounts with their (token-free) login status, plus the current
// consumer→account assignments. Admin-only.
func (r *Router) handleClaudeAccounts(w http.ResponseWriter, req *http.Request) {
	if _, ok := r.mustPrimary(w, req); !ok {
		return // mustPrimary already wrote 401/403
	}
	if r.claudeAccts == nil {
		writeErr(w, 503, "claude accounts unavailable")
		return
	}

	type acctView struct {
		ID    string                 `json:"id"`
		Label string                 `json:"label"`
		Email string                 `json:"email"`
		Login claudeacct.LoginStatus `json:"login"`
	}
	accounts := make([]acctView, 0)
	for _, a := range r.claudeAccts.Accounts() {
		accounts = append(accounts, acctView{
			ID: a.ID, Label: a.Label, Email: a.Email,
			Login: r.claudeAccts.LoginStatus(a.ID),
		})
	}

	type consView struct {
		ID        string `json:"id"`
		Label     string `json:"label"`
		Desc      string `json:"desc"`
		AccountID string `json:"account_id"`
	}
	assign := r.claudeAccts.Assignments()
	consumers := make([]consView, 0)
	for _, c := range r.claudeAccts.Consumers() {
		meta := consumerMeta[c]
		consumers = append(consumers, consView{
			ID: c, Label: meta[0], Desc: meta[1], AccountID: assign[c],
		})
	}

	writeJSON(w, map[string]any{
		"accounts":  accounts,
		"consumers": consumers,
	})
}

// handleClaudeAccountsUsage (GET) returns per-account usage metrics (tokens by
// today/7d/total + by-model + estimated cost), parsed from each account's
// Claude transcripts. Admin-only. Separate from /accounts because the parse is
// heavier — the UI loads it lazily.
func (r *Router) handleClaudeAccountsUsage(w http.ResponseWriter, req *http.Request) {
	if _, ok := r.mustPrimary(w, req); !ok {
		return
	}
	if r.claudeAccts == nil {
		writeErr(w, 503, "claude accounts unavailable")
		return
	}
	// One single sweep, all accounts. Sweeping per account would read the shared
	// tree twice and count every token twice.
	writeJSON(w, r.claudeAccts.UsageAll())
}

// handleClaudeAccountsRateLimits (GET) returns each account's live Claude Max
// rate-limit status (utilization/remaining per window + monthly credits),
// fetched from Anthropic's OAuth usage endpoint. Admin-only. Cached ≤60s.
func (r *Router) handleClaudeAccountsRateLimits(w http.ResponseWriter, req *http.Request) {
	if _, ok := r.mustPrimary(w, req); !ok {
		return
	}
	if r.claudeAccts == nil {
		writeErr(w, 503, "claude accounts unavailable")
		return
	}
	rates := make([]claudeacct.RateLimitStatus, 0)
	for _, a := range r.claudeAccts.Accounts() {
		rates = append(rates, r.claudeAccts.RateLimits(a))
	}
	writeJSON(w, map[string]any{"accounts": rates})
}

// handleClaudeAccountAssign (POST) points a consumer at an account. Admin-only.
// Body: {consumer, account_id}. The switch takes effect on the NEXT spawn of
// that consumer (jobs: next job; terminal/fork: next freshly-created session).
func (r *Router) handleClaudeAccountAssign(w http.ResponseWriter, req *http.Request) {
	user, ok := r.mustPrimary(w, req)
	if !ok {
		return
	}
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	if r.claudeAccts == nil {
		writeErr(w, 503, "claude accounts unavailable")
		return
	}
	var body struct {
		Consumer  string `json:"consumer"`
		AccountID string `json:"account_id"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	if err := r.claudeAccts.Assign(body.Consumer, body.AccountID); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	r.auditEvent(req, user, "claude.account.assign", body.Consumer+"="+body.AccountID)
	writeJSON(w, map[string]any{"ok": true, "consumer": body.Consumer, "account_id": body.AccountID})
}

// claudeProjectSlug converts an absolute path into the directory slug that
// Claude Code uses in its project store (swaps '/' and '.' for '-').
func claudeProjectSlug(path string) string {
	return strings.NewReplacer("/", "-", ".", "-").Replace(path)
}

// handleClaudeAccountSessionSwap (POST) hotswaps the Claude account for one or
// all sessions without killing them. Body: {"session":"name"|"*","account_id":"id"|""}.
// For shell sessions (bash/zsh/sh/fish) the new CLAUDE_CONFIG_DIR is injected
// live via send-keys; for sessions running claude in foreground the override is
// persisted and needs_restart:true is returned. Admin-only.
func (r *Router) handleClaudeAccountSessionSwap(w http.ResponseWriter, req *http.Request) {
	user, ok := r.mustPrimary(w, req)
	if !ok {
		return
	}
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	if r.claudeAccts == nil {
		writeErr(w, 503, "claude accounts unavailable")
		return
	}
	var body struct {
		Session   string `json:"session"`
		AccountID string `json:"account_id"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	applyToSession := func(sessionName string) map[string]any {
		if err := r.claudeAccts.SetSessionAccount(sessionName, body.AccountID); err != nil {
			return map[string]any{"session": sessionName, "ok": false, "error": err.Error()}
		}
		acctLabel := swapAccountLabel(body.AccountID)
		dir := r.claudeAccts.ConfigDirForSession(sessionName, claudeacct.ConsumerTerminal)

		// dtach: injects the env vars into the session's shell via `dtach -p` (replacing
		// the old key-sending). \x15 = Ctrl-U clears the current line before the command.
		// The next `claude` launched in the shell uses the new account; if a claude is
		// already running in the foreground, the user exits and relaunches (the account
		// is already in the registry + in the exported CLAUDE_CONFIG_DIR). dtach has no
		// respawn-pane, so there is no automatic relaunch — hence needs_restart for claude panes.
		// setEnv applies the account to the shell (CLAUDE_CONFIG_DIR + label).
		var setEnv string
		if dir == "" {
			setEnv = "unset CLAUDE_CONFIG_DIR; export VPSM_CLAUDE_ACCOUNT=" + acctLabel
		} else {
			setEnv = "export CLAUDE_CONFIG_DIR=" + dir + " VPSM_CLAUDE_ACCOUNT=" + acctLabel
		}
		// If a claude IS RUNNING in the session, switch it LIVE (respawn: kill + relaunch
		// `claude --continue` on the new account — the conversation is preserved because
		// projects/ is shared between the accounts). That is what makes the dropdown change
		// FOR REAL. With no claude running, inject setEnv into bash (it applies next time).
		if respawned, resumed := ptysvc.RespawnClaudeInSession(sessionName, setEnv); respawned {
			r.auditEvent(req, user, "claude.account.session_swap.respawn", sessionName+"="+body.AccountID)
			return map[string]any{
				"session": sessionName, "ok": true, "swapped": true,
				"method": "respawn", "resumed": resumed, "needs_restart": false,
			}
		}
		_ = ptysvc.SessionSendText(sessionName, "\x15"+setEnv+"\n")
		banner := fmt.Sprintf("printf '\\033[1;32m[VPS-MGR] Claude account: %s (active on the next claude)\\033[0m\\n'\n", acctLabel)
		_ = ptysvc.SessionSendText(sessionName, banner)
		r.auditEvent(req, user, "claude.account.session_swap.live", sessionName+"="+body.AccountID)
		return map[string]any{
			"session": sessionName, "ok": true,
			"swapped": true, "needs_restart": false,
		}
	}

	if body.Session == "*" {
		all, err := ptysvc.SessionListAll()
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		results := make([]map[string]any, 0, len(all))
		for _, s := range all {
			name, _ := s["name"].(string)
			if name != "" {
				results = append(results, applyToSession(name))
			}
		}
		writeJSON(w, map[string]any{"ok": true, "results": results})
		return
	}

	writeJSON(w, applyToSession(body.Session))
}

// handleClaudeAccountLoginTerminal (POST) opens a detached login shell
// for an account so the operator can run `claude` → /login (or
// `claude setup-token`) for that account. Admin-only. Works for every account
// including the default (ConfigDir==""): SpawnLoginShell opens it in HOME mode
// (/root/.claude) so a default account whose token expired can be re-logged.
func (r *Router) handleClaudeAccountLoginTerminal(w http.ResponseWriter, req *http.Request) {
	user, ok := r.mustPrimary(w, req)
	if !ok {
		return
	}
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	if r.claudeAccts == nil {
		writeErr(w, 503, "claude accounts unavailable")
		return
	}
	var body struct {
		AccountID string `json:"account_id"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	acct, found := r.claudeAccts.AccountByID(body.AccountID)
	if !found {
		writeErr(w, 400, "unknown account")
		return
	}
	// acct.ConfigDir == "" is the default account (jordan): SpawnLoginShell
	// handles it as a HOME-mode login shell (/root/.claude), so no special
	// casing here — the button must work for the default account too.
	sessionName := "vpsm-" + user + "-claude-login-" + acct.ID
	created, err := ptysvc.SpawnLoginShell(sessionName, acct.ConfigDir)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	r.auditEvent(req, user, "claude.account.login_terminal", created+" dir="+acct.ConfigDir)
	writeJSON(w, map[string]any{
		"ok":         true,
		"session":    created,
		"config_dir": acct.ConfigDir,
		"hint":       "In the terminal that just opened run `claude` and type /login (sign in as " + acct.Email + "), or run `claude setup-token`.",
	})
}
