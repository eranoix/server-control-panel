package claudeacct

// attrib.go — who spent each token.
//
// # Why this has to exist
//
// Until recently the answer was structural and free: each account had its own
// <configdir>/projects tree, so the file already said whose the token was. The
// live swap (a89bfc2) pointed both trees at the same
// /root/.claude-shared/projects — on purpose, because that is what makes
// `claude --continue` survive an account switch.
//
// And the transcript does NOT carry account identity. The keys of an assistant
// line are cwd, gitBranch, sessionId, timestamp, message, uuid, version and a
// few more — no email, no accountUuid. In other words: once the tree became
// shared, attribution stopped being recoverable from the transcript. Any
// heuristic that tries to infer the account by reading the file is guessing.
//
// The architectural conclusion is that attribution has to be OWNED by the
// control plane, which is what decides the CLAUDE_CONFIG_DIR on each spawn — not
// by the transcript, which is only the witness.
//
// # The ledger
//
// Append-only, one line per SessionStart, coming from the hook that already
// exists (/api/agent/hook). The hook runs INSIDE claude, with CLAUDE_CONFIG_DIR
// in the env: it KNOWS the account, it does not infer it.
//
//	{"ts":1787602647,"session_id":"b3524a31-…","account_id":"sam","source":"startup"}
//
// Reading is by INTERVAL, not by session: each entry opens a window that holds
// until the next entry of the SAME session. That is what makes the live swap
// work for free — the respawn fires a new SessionStart, which opens a new
// interval, and the messages before and after the switch land on different
// accounts, exactly as it happened. Nothing in account_switch.go had to change.
//
// # What the ledger does not reach
//
// The transcripts that predate it have no entry and are NOT attributable. They
// go into an "unattributed" bucket visible in the panel, with an opportunistic
// inference from session-env/ (see inferidoPorSessionEnv). Inventing an owner
// for them would be worse than admitting we do not know: a plausible number
// under the wrong name has no way of being spotted by whoever reads it.

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// AttribEntry is one line of the ledger: from Ts onward, session SessionID was
// running on account AccountID.
type AttribEntry struct {
	Ts        int64  `json:"ts"`
	SessionID string `json:"session_id"`
	AccountID string `json:"account_id"`
	Source    string `json:"source,omitempty"` // startup | resume | clear | compact
	ConfigDir string `json:"config_dir,omitempty"`
}

// attribPath is the ledger on disk, next to the rest of the account state.
func (s *Store) attribPath() string {
	return filepath.Join(filepath.Dir(s.path), "claude_attrib.jsonl")
}

var attribMu sync.Mutex

// RecordAttrib appends an entry to the ledger. Append-only and under lock: the
// hook can fire from several sessions at once, and one JSONL line truncated
// mid-way poisons the reading of all the others.
//
// Entries with no session or no account are dropped silently: they would
// attribute nothing and would only serve to inflate the file.
func (s *Store) RecordAttrib(e AttribEntry) error {
	if strings.TrimSpace(e.SessionID) == "" || strings.TrimSpace(e.AccountID) == "" {
		return nil
	}
	if !s.hasAccount(e.AccountID) {
		return nil
	}
	linha, err := json.Marshal(e)
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
	_, err = f.Write(append(linha, '\n'))
	return err
}

// AccountIDForConfigDir maps a CLAUDE_CONFIG_DIR back to the account id. An empty
// Dir is the default account — the same semantics as Account.ConfigDir=="", which
// means "inherit the process default", and not "unknown account".
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

// ledger is the query index: per session, the intervals in time order.
type ledger struct {
	porSessao map[string][]AttribEntry
}

// carregaLedger reads the whole ledger and sorts each session by time. Corrupted
// lines are skipped rather than taking the read down: the file is written by an
// external hook, and a partial append must not cost ALL the attribution.
func (s *Store) carregaLedger() *ledger {
	l := &ledger{porSessao: map[string][]AttribEntry{}}
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
		l.porSessao[e.SessionID] = append(l.porSessao[e.SessionID], e)
	}
	for k := range l.porSessao {
		sort.Slice(l.porSessao[k], func(i, j int) bool {
			return l.porSessao[k][i].Ts < l.porSessao[k][j].Ts
		})
	}
	return l
}

// contaEm returns the account that held for (session, instant), or "" when it is
// not known.
//
// A session's first entry applies RETROACTIVELY to it: the SessionStart hook
// fires a few milliseconds AFTER claude opens the transcript, so requiring
// ts >= entry.Ts would discard the first messages of every session — which is
// precisely the common case for a short one. Back-dating only the FIRST entry is
// safe: the ones that follow mark real account switches, and those cannot be
// back-dated without stealing tokens from the previous account.
func (l *ledger) contaEm(sessao string, ts int64) string {
	ents := l.porSessao[sessao]
	if len(ents) == 0 {
		return ""
	}
	conta := ents[0].AccountID
	for _, e := range ents[1:] {
		if ts < e.Ts {
			break
		}
		conta = e.AccountID
	}
	return conta
}

// inferidoPorSessionEnv is the opportunistic backfill for the history that
// predates the ledger. Claude Code creates <configdir>/session-env/<sessionId>/
// and that directory is NOT shared between the accounts (measured: 203 under
// /root/.claude against 380 under /srv/agent-accounts/sam), so its presence is
// a real signal of which account ran the session.
//
// It is INFERENCE, not measurement, and is treated as such: it holds only when a
// SINGLE account claims the session, and the volume that came in this way is
// reported separately so nobody confuses what was observed with what was
// deduced. It is a Claude Code internal, with no contract — if it disappears the
// effect is the "unattributed" bucket growing, never a wrong number appearing.
func (s *Store) inferidoPorSessionEnv() map[string]string {
	out := map[string]string{}
	ambiguo := map[string]bool{}
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
			if prev, visto := out[id]; visto && prev != a.ID {
				ambiguo[id] = true // two accounts claim it: no way to decide
				continue
			}
			out[id] = a.ID
		}
	}
	for id := range ambiguo {
		delete(out, id)
	}
	return out
}
