package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

const (
	agentStateRunning = "running"
	agentStateWaiting = "waiting_input"
	agentStateDone    = "done"
	agentStateError   = "error"
	agentStateIdle    = "idle"
)

const agentStatusStaleAfter = 24 * time.Hour

type AgentTokens struct {
	In              int64 `json:"in"`
	Out             int64 `json:"out"`
	CacheRead       int64 `json:"cache_read"`
	CacheCreation   int64 `json:"cache_creation"`
	CacheCreation1h int64 `json:"cache_creation_1h,omitempty"`
}

type AgentStatus struct {
	State     string       `json:"state"`
	Updated   int64        `json:"updated"`
	CCSession string       `json:"cc_session,omitempty"`
	Tokens    *AgentTokens `json:"tokens,omitempty"`
	CostUSD   float64      `json:"cost_usd,omitempty"`
}

type agentStatusStore struct {
	mu   sync.Mutex
	path string
	m    map[string]AgentStatus
}

func newAgentStatusStore(path string) *agentStatusStore {
	s := &agentStatusStore{path: path, m: map[string]AgentStatus{}}
	if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
		_ = json.Unmarshal(data, &s.m)
		if s.m == nil {
			s.m = map[string]AgentStatus{}
		}
	}
	return s
}

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

var projectMangleRe = regexp.MustCompile(`[^a-zA-Z0-9]`)

func mangleProjectDir(cwd string) string { return projectMangleRe.ReplaceAllString(cwd, "-") }

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

func (s *agentCWDStore) Get(name string) string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.m[name]
}

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
