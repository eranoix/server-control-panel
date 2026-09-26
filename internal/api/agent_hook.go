// agent_hook.go — Claude Code hooks → agent state + notifications (VPSM #4).
//
// Endpoint: POST /api/agent/hook. It is registered on the RAW mux (not behind
// the JWT middleware) but is NOT an open mutation endpoint: it is gated on
//
//	(a) a loopback RemoteAddr (the hook curls 127.0.0.1), AND
//	(b) a shared secret the hook command includes (X-Vpsm-Agent-Secret),
//
// compared in constant time. The secret lives in <DataDir>/agent-hook.secret
// and is the same value ensureAgentHooks() embeds into the spawned sessions'
// settings.json hook commands.
//
// Payload is a Claude Code hook event: {session_id, cwd, hook_event_name,
// transcript_path, message}. We map cwd → dtach session name via the cwd
// sidecar (agent_status.go), set the session's state in session-status.json,
// and dispatch a notify Event for waiting_input / done (throttled by the notify
// spine's per-(DedupKey,rule) window).
package api

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"server-control-panel/internal/claudeacct"
	"server-control-panel/internal/notify"
)

// agentHookPayload is the subset of the CC hook JSON we consume.
type agentHookPayload struct {
	SessionID      string `json:"session_id"`
	CWD            string `json:"cwd"`
	HookEventName  string `json:"hook_event_name"`
	TranscriptPath string `json:"transcript_path"`
	Message        string `json:"message"`
	// Source distingue startup / resume / clear / compact num SessionStart.
	Source string `json:"source"`
}

// loadAgentHookSecret reads (or first-run generates) the shared secret. Returns
// "" only if it cannot generate one, in which case the endpoint fails closed
// (every request is rejected).
func (r *Router) loadAgentHookSecret() string {
	path := filepath.Join(r.cfg.DataDir, "agent-hook.secret")
	if b, err := os.ReadFile(path); err == nil {
		if s := strings.TrimSpace(string(b)); s != "" {
			return s
		}
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return ""
	}
	s := hex.EncodeToString(buf)
	_ = os.MkdirAll(r.cfg.DataDir, 0o700)
	_ = os.WriteFile(path, []byte(s), 0o600)
	return s
}

// isLoopbackRemote reports whether the request came from the loopback iface.
func isLoopbackRemote(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// hookEventToState maps a CC hook_event_name to an agent state.
func hookEventToState(name string) string {
	switch name {
	case "Notification":
		return agentStateWaiting
	case "Stop":
		return agentStateDone
	case "SessionEnd":
		return agentStateIdle
	default:
		// SessionStart / PreToolUse / PostToolUse / UserPromptSubmit / etc. — all
		// signal active work.
		return agentStateRunning
	}
}

// handleAgentHook receives a CC hook payload, sets the mapped session's state,
// and dispatches a notify event for waiting_input / done.
func (r *Router) handleAgentHook(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	// Defense in depth: loopback only.
	if !isLoopbackRemote(req.RemoteAddr) {
		writeErr(w, 403, "forbidden")
		return
	}
	// Shared secret, constant-time. Fail closed when the secret is unset.
	got := req.Header.Get("X-Vpsm-Agent-Secret")
	if r.agentHookSecret == "" || subtle.ConstantTimeCompare([]byte(got), []byte(r.agentHookSecret)) != 1 {
		writeErr(w, 401, "unauthorized")
		return
	}

	var p agentHookPayload
	if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 1<<20)).Decode(&p); err != nil {
		writeErr(w, 400, "bad json")
		return
	}

	// Attribution ledger. The transcript does NOT carry account identity, and
	// ever since the accounts started sharing the transcript tree, authorship
	// stopped being recoverable from the file. This hook is the one place in the
	// system that KNOWS the account without inferring it: it runs inside claude,
	// with CLAUDE_CONFIG_DIR in its own env, and the header below carries that
	// value all the way here.
	//
	// Each SessionStart opens an interval. That is why the live swap comes for
	// free: the respawn fires another SessionStart, which opens another interval,
	// and the messages before and after the switch land on different accounts.
	if p.HookEventName == "SessionStart" && r.claudeAccts != nil {
		dir := req.Header.Get("X-Vpsm-Claude-Dir")
		if id := r.claudeAccts.AccountIDForConfigDir(dir); id != "" {
			_ = r.claudeAccts.RecordAttrib(claudeacct.AttribEntry{
				Ts:        time.Now().Unix(),
				SessionID: p.SessionID,
				AccountID: id,
				Source:    p.Source,
				ConfigDir: dir,
			})
		}
	}

	// Resolve cwd → dtach session name. Best-effort: without a record we can't
	// attribute the event, so we ack (200) without touching state.
	session := r.agentCWD.ResolveName(p.CWD)
	if session == "" {
		writeJSON(w, map[string]any{"status": "unmapped"})
		return
	}

	state := hookEventToState(p.HookEventName)
	if r.agentStatus != nil {
		r.agentStatus.setState(session, state, p.SessionID)
	}

	// Notify on the actionable transitions. The notify spine throttles identical
	// (DedupKey, rule) within its window, so repeated waiting_input pings for the
	// same session collapse to one per window.
	if r.notify != nil && (state == agentStateWaiting || state == agentStateDone) {
		r.notify.Dispatch(r.agentHookEvent(session, state, p.Message))
	}

	r.auditEventBackground(r.sessionOwnerOf(session), "agent.hook", session+" "+p.HookEventName+" → "+state)
	writeJSON(w, map[string]any{"status": "ok", "session": session, "state": state})
}

// agentHookEvent builds the notify Event for a waiting_input / done transition.
func (r *Router) agentHookEvent(session, state, msg string) notify.Event {
	typ := notify.TypeAgentDone
	title := "Agent finished: " + session
	if state == agentStateWaiting {
		typ = notify.TypeAgentWaiting
		title = "Agent waiting for input: " + session
	}
	return notify.Event{
		Type:     typ,
		Severity: notify.SeverityInfo,
		Source:   "agent:" + session,
		Owner:    r.sessionOwnerOf(session),
		Title:    title,
		Body:     strings.TrimSpace(msg),
		Labels:   map[string]string{"session": session, "state": state},
		DedupKey: "agent:" + session + ":" + state,
	}
}

// sessionOwnerOf best-effort resolves the owning user of a session (registry
// first, then the "vpsm-<user>-…" naming convention). "" when unknown.
func (r *Router) sessionOwnerOf(session string) string {
	if r.sessionOwn != nil {
		if o := r.sessionOwn.Owner(session); o != "" {
			return o
		}
	}
	if rest, ok := strings.CutPrefix(session, "vpsm-"); ok {
		if i := strings.IndexByte(rest, '-'); i > 0 {
			return rest[:i]
		}
	}
	return ""
}

// ─── settings.json hook wiring ──────────────────────────────────────────────

// selfLoopbackPort extracts the listen port (default 8766) for the hook curl.
func (r *Router) selfLoopbackPort() int {
	port := 8766
	if listen := strings.TrimSpace(r.cfg.Listen); listen != "" {
		if i := strings.LastIndex(listen, ":"); i >= 0 {
			if p, err := strconv.Atoi(listen[i+1:]); err == nil && p > 0 {
				port = p
			}
		}
	}
	return port
}

// ensureAgentHooks idempotently installs Notification + Stop hooks into the CC
// settings.json of every config dir spawned sessions use (the fork config dir,
// when set, AND /root/.claude). The hook command curls the local endpoint with
// the shared secret. Existing settings are preserved; only our own hook entries
// (identified by the /api/agent/hook marker) are replaced on re-run. Best-
// effort: failures are logged by the caller pattern, never fatal.
func (r *Router) ensureAgentHooks() {
	if r.agentHookSecret == "" {
		return
	}
	// X-Vpsm-Claude-Dir carries the CLAUDE_CONFIG_DIR of the process that fired
	// the hook, expanded by the shell AT THAT MOMENT. It is what lets the
	// attribution ledger know the account without inferring it.
	//
	// It comes from the env, and is not recorded per directory, on purpose:
	// settings.json can be shared between accounts (the claudeacct package comment
	// says it is a symlink; today they are separate files, but the env is
	// per-process and is right in both worlds). DOUBLE quotes because the expansion
	// has to happen; the ':-' keeps the header present and empty for the default
	// account, which runs without CLAUDE_CONFIG_DIR — and empty is precisely its id.
	cmd := "curl -sf -m 5 -X POST" +
		" -H 'Content-Type: application/json'" +
		" -H 'X-Vpsm-Agent-Secret: " + r.agentHookSecret + "'" +
		" -H \"X-Vpsm-Claude-Dir: ${CLAUDE_CONFIG_DIR:-}\"" +
		" --data-binary @- http://127.0.0.1:" + strconv.Itoa(r.selfLoopbackPort()) + "/api/agent/hook"

	dirs := map[string]bool{"/root/.claude": true}
	if fork := r.forkConfigDir(); fork != "" {
		dirs[fork] = true
	}
	// ALL the accounts in the registry, not just the default one and the fork's: an
	// account whose settings.json lacks the hook never enters the ledger, and all of
	// its usage falls into the "unattributed" bucket without anything looking broken.
	if r.claudeAccts != nil {
		for _, a := range r.claudeAccts.Accounts() {
			if a.ConfigDir != "" {
				dirs[a.ConfigDir] = true
			}
		}
	}
	for dir := range dirs {
		_ = mergeAgentHooksFile(filepath.Join(dir, "settings.json"), cmd)
	}
}

// agentHookMarker identifies hook commands this code installed, so a re-run
// updates them (port/secret) rather than duplicating.
const agentHookMarker = "/api/agent/hook"

// mergeAgentHooksFile reads settings.json (tolerating absence), ensures the
// Notification + Stop events carry exactly one command hook pointing at our
// endpoint, and writes it back atomically without clobbering unrelated keys.
func mergeAgentHooksFile(path, cmd string) error {
	root := map[string]any{}
	if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
		if err := json.Unmarshal(data, &root); err != nil {
			return err // malformed existing settings: don't risk clobbering
		}
	}

	hooks, _ := root["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	// SessionStart feeds the attribution ledger: every fire opens an interval of
	// "this session is on this account". It is also what makes the live swap work by
	// itself — the respawn fires another SessionStart.
	for _, event := range []string{"Notification", "Stop", "SessionStart"} {
		hooks[event] = withAgentHook(hooks[event], cmd)
	}
	root["hooks"] = hooks

	data, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// withAgentHook returns the hook-group slice for one event: existing groups with
// any of OUR command entries stripped, plus one fresh group carrying our command.
func withAgentHook(existing any, cmd string) []any {
	out := []any{}
	if arr, ok := existing.([]any); ok {
		for _, g := range arr {
			grp, ok := g.(map[string]any)
			if !ok {
				out = append(out, g)
				continue
			}
			kept := filterOurHooks(grp["hooks"])
			// Drop groups that only existed to hold our (now-removed) command.
			if len(kept) == 0 && groupIsOnlyHooks(grp) {
				continue
			}
			grp["hooks"] = kept
			out = append(out, grp)
		}
	}
	out = append(out, map[string]any{
		"hooks": []any{map[string]any{"type": "command", "command": cmd}},
	})
	return out
}

// filterOurHooks removes hook entries whose command contains our marker.
func filterOurHooks(hooksField any) []any {
	kept := []any{}
	arr, ok := hooksField.([]any)
	if !ok {
		return kept
	}
	for _, h := range arr {
		if hm, ok := h.(map[string]any); ok {
			if c, _ := hm["command"].(string); strings.Contains(c, agentHookMarker) {
				continue
			}
		}
		kept = append(kept, h)
	}
	return kept
}

// groupIsOnlyHooks reports whether a matcher group has no keys other than
// "hooks" (so it can be dropped once emptied).
func groupIsOnlyHooks(grp map[string]any) bool {
	for k := range grp {
		if k != "hooks" {
			return false
		}
	}
	return true
}
