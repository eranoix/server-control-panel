package claudeacct

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

type AttribEntry struct {
	Ts        int64  `json:"ts"`
	SessionID string `json:"session_id"`
	AccountID string `json:"account_id"`
	Source    string `json:"source,omitempty"`
	ConfigDir string `json:"config_dir,omitempty"`
}

func (s *Store) attribPath() string {
	return filepath.Join(filepath.Dir(s.path), "claude_attrib.jsonl")
}

var attribMu sync.Mutex

func (s *Store) RecordAttrib(e AttribEntry) error {
	if strings.TrimSpace(e.SessionID) == "" || strings.TrimSpace(e.AccountID) == "" {
		return nil
	}
	if !s.hasAccount(e.AccountID) {
		return nil
	}
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	attribMu.Lock()
	defer attribMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.attribPath()), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(s.attribPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return err
}

func (s *Store) AccountIDForConfigDir(dir string) string {
	dir = strings.TrimRight(strings.TrimSpace(dir), "/")
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, a := range s.accounts {
		if strings.TrimRight(a.ConfigDir, "/") == dir {
			return a.ID
		}
	}
	return ""
}

type ledger struct {
	bySession map[string][]AttribEntry
}

func (s *Store) loadLedger() *ledger {
	l := &ledger{bySession: map[string][]AttribEntry{}}
	f, err := os.Open(s.attribPath())
	if err != nil {
		return l
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		var e AttribEntry
		if json.Unmarshal(sc.Bytes(), &e) != nil || e.SessionID == "" || e.AccountID == "" {
			continue
		}
		l.bySession[e.SessionID] = append(l.bySession[e.SessionID], e)
	}
	for k := range l.bySession {
		sort.Slice(l.bySession[k], func(i, j int) bool {
			return l.bySession[k][i].Ts < l.bySession[k][j].Ts
		})
	}
	return l
}

func (l *ledger) accountAt(session string, ts int64) string {
	ents := l.bySession[session]
	if len(ents) == 0 {
		return ""
	}
	account := ents[0].AccountID
	for _, e := range ents[1:] {
		if ts < e.Ts {
			break
		}
		account = e.AccountID
	}
	return account
}

func (s *Store) inferredFromSessionEnv() map[string]string {
	out := map[string]string{}
	ambiguous := map[string]bool{}
	for _, a := range s.Accounts() {
		base := a.ConfigDir
		if base == "" {
			base = s.claudeHome
		}
		ents, err := os.ReadDir(filepath.Join(base, "session-env"))
		if err != nil {
			continue
		}
		for _, e := range ents {
			if !e.IsDir() {
				continue
			}
			id := e.Name()
			if prev, seen := out[id]; seen && prev != a.ID {
				ambiguous[id] = true
				continue
			}
			out[id] = a.ID
		}
	}
	for id := range ambiguous {
		delete(out, id)
	}
	return out
}
