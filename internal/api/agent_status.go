// agent_status.go — shared agent status file (VPSM agent-ops #3/#4).
//
// This produces <DataDir>/session-status.json, the CONTRACT the code-server
// code-server session extension reads. It is a JSON object keyed by dtach
// session name:
//
//	{ "<dtach-session-name>": {
//	    "state": "running"|"waiting_input"|"done"|"error"|"idle",
//	    "updated": <unix seconds>,
//	    "cc_session": "<claude session uuid, optional>",
//	    "tokens": { "in": N, "out": N, "cache_read": N, "cache_creation": N },
//	    "cost_usd": <float>
//	} }
//
// Two producers feed it: the CC hook endpoint (agent_hook.go → state) and the
// periodic cost aggregator (agent_cost.go → tokens+cost). Both go through the
// atomic temp+rename writer here. The cwd sidecar (session-cwd.json) maps a
// dtach session name → its cwd so the hook can resolve payload.cwd → name and
// the aggregator can resolve name → CC project dir.
package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

// Agent states (the closed set the extension renders).
const (
	agentStateRunning = "running"
	agentStateWaiting = "waiting_input"
	agentStateDone    = "done"
	agentStateError   = "error"
	agentStateIdle    = "idle"
)

// agentStatusStaleAfter prunes entries not touched within this window, in
// addition to dropping entries whose session is no longer known.
const agentStatusStaleAfter = 24 * time.Hour

// AgentTokens mirrors the CC transcript `usage` block, summed across a
// session's newest JSONL. Field names ARE the extension contract.
type AgentTokens struct {
	In            int64 `json:"in"`
	Out           int64 `json:"out"`
	CacheRead     int64 `json:"cache_read"`
	CacheCreation int64 `json:"cache_creation"`
	// CacheCreation1h is the slice of CacheCreation written with the 1h TTL,
	// which bills at 2× input instead of the 5m rate of 1.25×.
	// CacheCreation stays the TOTAL of both TTLs so existing consumers of
	// `cache_creation` keep reading the same number; omitempty keeps the field
	// out of the JSON for transcripts predating the breakdown.
	CacheCreation1h int64 `json:"cache_creation_1h,omitempty"`
}

// AgentStatus is one dtach session's telemetry entry. Missing fields allowed.
type AgentStatus struct {
	State     string       `json:"state"`
	Updated   int64        `json:"updated"`
	CCSession string       `json:"cc_session,omitempty"`
	Tokens    *AgentTokens `json:"tokens,omitempty"`
	CostUSD   float64      `json:"cost_usd,omitempty"`
}

// agentStatusStore owns <DataDir>/session-status.json. All access is under mu;
// the file is tiny so persisting under the lock is fine. Atomic temp+rename,
// same pattern as pty.Registry / ownership.
type agentStatusStore struct {
	mu   sync.Mutex
	path string
	m    map[string]AgentStatus
}

// newAgentStatusStore loads (or initialises) the status file. A missing or
// empty file yields an empty map; malformed JSON is tolerated (starts fresh)
// so a corrupt sidecar never blocks boot.
func newAgentStatusStore(path string) *agentStatusStore {
	s := &agentStatusStore{path: path, m: map[string]AgentStatus{}}
	if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
		_ = json.Unmarshal(data, &s.m) // tolerate corruption: keep empty map
		if s.m == nil {
			s.m = map[string]AgentStatus{}
		}
	}
	return s
}

// Snapshot retorna uma copia do mapa de status (leitura pro kanban/dashboard
// de custo). Copia rasa basta: AgentStatus e valor; Tokens e ponteiro que os
// handlers de leitura nao mutam.
func (s *agentStatusStore) Snapshot() map[string]AgentStatus {
	if s == nil {
		return map[string]AgentStatus{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]AgentStatus, len(s.m))
	for k, v := range s.m {
		out[k] = v
	}
	return out
}

// setState updates a session's state (and cc_session when known), preserving
// any tokens/cost already computed. Bumps `updated`. No-op on nil receiver /
// empty session.
func (s *agentStatusStore) setState(session, state, ccSession string) {
	if s == nil || session == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.m[session]
	e.State = state
	e.Updated = time.Now().Unix()
	if ccSession != "" {
		e.CCSession = ccSession
	}
	s.m[session] = e
	_ = s.persistLocked()
}

// setTokens updates a session's tokens+cost (and cc_session), preserving the
// current state. `updated` is only bumped when the token totals changed, so a
// truly idle session's timestamp ages toward the stale-prune cutoff instead of
// being kept alive by the aggregator's own ticks.
func (s *agentStatusStore) setTokens(session string, tok AgentTokens, cost float64, ccSession string) {
	if s == nil || session == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.m[session]
	changed := e.Tokens == nil || *e.Tokens != tok
	t := tok
	e.Tokens = &t
	e.CostUSD = cost
	if ccSession != "" {
		e.CCSession = ccSession
	}
	if e.State == "" {
		e.State = agentStateIdle
	}
	if changed || e.Updated == 0 {
		e.Updated = time.Now().Unix()
	}
	s.m[session] = e
	_ = s.persistLocked()
}

// prune drops entries whose session is not in `known` OR whose `updated` is
// older than agentStatusStaleAfter. Called by the aggregator each tick.
func (s *agentStatusStore) prune(known map[string]bool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := time.Now().Add(-agentStatusStaleAfter).Unix()
	changed := false
	for name, e := range s.m {
		if !known[name] || (e.Updated != 0 && e.Updated < cutoff) {
			delete(s.m, name)
			changed = true
		}
	}
	if changed {
		_ = s.persistLocked()
	}
}

// persistLocked writes the map atomically (temp+rename). Caller holds mu.
func (s *agentStatusStore) persistLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.m, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// ─── cwd sidecar ────────────────────────────────────────────────────────────

// projectMangleRe mirrors Claude Code's cwd→project-dir mapping: every
// non-alphanumeric character becomes '-'. (Same rule as cmd/vpsmctl's
// projectMangle; duplicated here to keep the api package self-contained.)
var projectMangleRe = regexp.MustCompile(`[^a-zA-Z0-9]`)

func mangleProjectDir(cwd string) string { return projectMangleRe.ReplaceAllString(cwd, "-") }

// agentCWDStore owns <DataDir>/session-cwd.json: dtach session name → cwd. This
// is the mapping both the hook (cwd → name) and the aggregator (name → cwd →
// CC project dir) resolve through. Best-effort for sessions without a record.
type agentCWDStore struct {
	mu   sync.Mutex
	path string
	m    map[string]string
}

func newAgentCWDStore(path string) *agentCWDStore {
	s := &agentCWDStore{path: path, m: map[string]string{}}
	if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
		_ = json.Unmarshal(data, &s.m)
		if s.m == nil {
			s.m = map[string]string{}
		}
	}
	return s
}

// Put records name→cwd (idempotent; no-op when unchanged). nil-safe.
func (s *agentCWDStore) Put(name, cwd string) {
	if s == nil || name == "" || cwd == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m[name] == cwd {
		return
	}
	s.m[name] = cwd
	_ = s.persistLocked()
}

// Get returns the recorded cwd for name ("" when absent). nil-safe.
func (s *agentCWDStore) Get(name string) string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.m[name]
}

// All returns a snapshot copy of the name→cwd map. nil-safe.
func (s *agentCWDStore) All() map[string]string {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]string, len(s.m))
	for k, v := range s.m {
		out[k] = v
	}
	return out
}

// ResolveName maps a cwd (from a hook payload) back to a session name. It tries
// an exact cwd match first, then a project-mangle match (the hook's cwd and the
// recorded cwd can differ only by trailing-slash / symlink normalisation, but
// both mangle to the same CC project dir). Returns "" when unresolved.
func (s *agentCWDStore) ResolveName(cwd string) string {
	if s == nil || cwd == "" {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for name, c := range s.m {
		if c == cwd {
			return name
		}
	}
	target := mangleProjectDir(cwd)
	for name, c := range s.m {
		if mangleProjectDir(c) == target {
			return name
		}
	}
	return ""
}

func (s *agentCWDStore) persistLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.m, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
