package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"server-control-panel/internal/claudeacct"
	ptysvc "server-control-panel/internal/pty"
)

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func swapAccountLabel(accountID string) string {
	var b strings.Builder
	for _, r := range accountID {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "default"
	}
	return b.String()
}

func (r *Router) jobsConfigDir() string {
	if r.claudeAccts == nil {
		return ""
	}
	return r.claudeAccts.ConfigDirFor(claudeacct.ConsumerJobs)
}

func (r *Router) terminalConfigDir() string {
	if r.claudeAccts == nil {
		return ""
	}
	return r.claudeAccts.ConfigDirFor(claudeacct.ConsumerTerminal)
}

func (r *Router) forkConfigDir() string {
	if r.claudeAccts == nil {
		return ""
	}
	return r.claudeAccts.ConfigDirFor(claudeacct.ConsumerFork)
}

func (r *Router) terminalConfigDirForSession(session string) string {
	if r.claudeAccts == nil {
		return ""
	}
	return r.claudeAccts.ConfigDirForSession(session, claudeacct.ConsumerTerminal)
}

var consumerMeta = map[string][2]string{
	claudeacct.ConsumerJobs:     {"Jobs (Jira analysis)", "claude -p during ticket analysis/refinement — includes detached jobs"},
	claudeacct.ConsumerTerminal: {"Interactive terminal", "terminal panes where you run claude by hand"},
	claudeacct.ConsumerFork:     {"Fork / Work on it now", "claude sessions created by fork, restart and 'Work on it now' in Jira"},
}

func (r *Router) handleClaudeAccounts(w http.ResponseWriter, req *http.Request) {
	if _, ok := r.mustPrimary(w, req); !ok {
		return
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

func (r *Router) handleClaudeAccountsUsage(w http.ResponseWriter, req *http.Request) {
	if _, ok := r.mustPrimary(w, req); !ok {
		return
	}
	if r.claudeAccts == nil {
		writeErr(w, 503, "claude accounts unavailable")
		return
	}
	writeJSON(w, r.claudeAccts.UsageAll())
}

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

func claudeProjectSlug(path string) string {
	return strings.NewReplacer("/", "-", ".", "-").Replace(path)
}

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

		var setEnv string
		if dir == "" {
			setEnv = "unset CLAUDE_CONFIG_DIR; export PANEL_CLAUDE_ACCOUNT=" + acctLabel
		} else {
			setEnv = "export CLAUDE_CONFIG_DIR=" + dir + " PANEL_CLAUDE_ACCOUNT=" + acctLabel
		}
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
	sessionName := "panel-" + user + "-claude-login-" + acct.ID
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
