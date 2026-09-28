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

type agentHookPayload struct {
	SessionID      string `json:"session_id"`
	CWD            string `json:"cwd"`
	HookEventName  string `json:"hook_event_name"`
	TranscriptPath string `json:"transcript_path"`
	Message        string `json:"message"`
	Source         string `json:"source"`
}

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

func isLoopbackRemote(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func hookEventToState(name string) string {
	switch name {
	case "Notification":
		return agentStateWaiting
	case "Stop":
		return agentStateDone
	case "SessionEnd":
		return agentStateIdle
	default:
		return agentStateRunning
	}
}

func (r *Router) handleAgentHook(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	if !isLoopbackRemote(req.RemoteAddr) {
		writeErr(w, 403, "forbidden")
		return
	}
	got := req.Header.Get("X-Panel-Agent-Secret")
	if r.agentHookSecret == "" || subtle.ConstantTimeCompare([]byte(got), []byte(r.agentHookSecret)) != 1 {
		writeErr(w, 401, "unauthorized")
		return
	}

	var p agentHookPayload
	if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 1<<20)).Decode(&p); err != nil {
		writeErr(w, 400, "bad json")
		return
	}

	if p.HookEventName == "SessionStart" && r.claudeAccts != nil {
		dir := req.Header.Get("X-Panel-Claude-Dir")
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

	session := r.agentCWD.ResolveName(p.CWD)
	if session == "" {
		writeJSON(w, map[string]any{"status": "unmapped"})
		return
	}

	state := hookEventToState(p.HookEventName)
	if r.agentStatus != nil {
		r.agentStatus.setState(session, state, p.SessionID)
	}

	if r.notify != nil && (state == agentStateWaiting || state == agentStateDone) {
		r.notify.Dispatch(r.agentHookEvent(session, state, p.Message))
	}

	r.auditEventBackground(r.sessionOwnerOf(session), "agent.hook", session+" "+p.HookEventName+" → "+state)
	writeJSON(w, map[string]any{"status": "ok", "session": session, "state": state})
}

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

func (r *Router) sessionOwnerOf(session string) string {
	if r.sessionOwn != nil {
		if o := r.sessionOwn.Owner(session); o != "" {
			return o
		}
	}
	if rest, ok := strings.CutPrefix(session, "panel-"); ok {
		if i := strings.IndexByte(rest, '-'); i > 0 {
			return rest[:i]
		}
	}
	return ""
}

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

func (r *Router) ensureAgentHooks() {
	if r.agentHookSecret == "" {
		return
	}
	cmd := "curl -sf -m 5 -X POST" +
		" -H 'Content-Type: application/json'" +
		" -H 'X-Panel-Agent-Secret: " + r.agentHookSecret + "'" +
		" -H \"X-Panel-Claude-Dir: ${CLAUDE_CONFIG_DIR:-}\"" +
		" --data-binary @- http://127.0.0.1:" + strconv.Itoa(r.selfLoopbackPort()) + "/api/agent/hook"

	dirs := map[string]bool{"/root/.claude": true}
	if fork := r.forkConfigDir(); fork != "" {
		dirs[fork] = true
	}
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

const agentHookMarker = "/api/agent/hook"

func mergeAgentHooksFile(path, cmd string) error {
	root := map[string]any{}
	if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
		if err := json.Unmarshal(data, &root); err != nil {
			return err
		}
	}

	hooks, _ := root["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
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

func groupIsOnlyHooks(grp map[string]any) bool {
	for k := range grp {
		if k != "hooks" {
			return false
		}
	}
	return true
}
