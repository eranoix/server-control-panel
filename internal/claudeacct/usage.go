package claudeacct

import (
	"bufio"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type UsageStat struct {
	InputTokens           int64   `json:"input_tokens"`
	OutputTokens          int64   `json:"output_tokens"`
	CacheCreationTokens   int64   `json:"cache_creation_tokens"`
	CacheCreation1hTokens int64   `json:"cache_creation_1h_tokens,omitempty"`
	CacheReadTokens       int64   `json:"cache_read_tokens"`
	Messages              int64   `json:"messages"`
	CostUSD               float64 `json:"cost_usd"`
}

func (s *UsageStat) add(o UsageStat) {
	s.InputTokens += o.InputTokens
	s.OutputTokens += o.OutputTokens
	s.CacheCreationTokens += o.CacheCreationTokens
	s.CacheCreation1hTokens += o.CacheCreation1hTokens
	s.CacheReadTokens += o.CacheReadTokens
	s.Messages += o.Messages
	s.CostUSD += o.CostUSD
}

func (s UsageStat) TotalTokens() int64 {
	return s.InputTokens + s.OutputTokens + s.CacheCreationTokens + s.CacheReadTokens
}

type ModelUsage struct {
	Model string `json:"model"`
	UsageStat
}

type AccountUsage struct {
	AccountID      string       `json:"account_id"`
	Label          string       `json:"label"`
	Today          UsageStat    `json:"today"`
	Last7d         UsageStat    `json:"last_7d"`
	Total          UsageStat    `json:"total"`
	ByModel        []ModelUsage `json:"by_model"`
	Sessions       int          `json:"sessions"`
	LastActivity   int64        `json:"last_activity"`
	LastSession    UsageStat    `json:"last_session"`
	Error          string       `json:"error,omitempty"`
	InferredTokens int64        `json:"inferred_tokens,omitempty"`
	Dir            string       `json:"dir,omitempty"`
}

type modelPrice struct{ in, out float64 }

func priceFor(model string) modelPrice {
	m := strings.ToLower(model)
	switch {
	case strings.Contains(m, "fable"):
		return modelPrice{10.0 / 1e6, 50.0 / 1e6}
	case strings.Contains(m, "opus"):
		return modelPrice{5.0 / 1e6, 25.0 / 1e6}
	case strings.Contains(m, "sonnet"):
		return modelPrice{3.0 / 1e6, 15.0 / 1e6}
	case strings.Contains(m, "haiku"):
		return modelPrice{1.0 / 1e6, 5.0 / 1e6}
	default:
		return modelPrice{5.0 / 1e6, 25.0 / 1e6}
	}
}

func costOf(st UsageStat, model string) float64 {
	p := priceFor(model)
	cc1h := st.CacheCreation1hTokens
	if cc1h > st.CacheCreationTokens {
		cc1h = st.CacheCreationTokens
	}
	if cc1h < 0 {
		cc1h = 0
	}
	cc5m := st.CacheCreationTokens - cc1h
	return float64(st.InputTokens)*p.in +
		float64(st.OutputTokens)*p.out +
		float64(cc5m)*p.in*1.25 +
		float64(cc1h)*p.in*2.0 +
		float64(st.CacheReadTokens)*p.in*0.1
}

type msgAgg struct {
	ts      int64
	session string
	model   string
	tokens  UsageStat
}

type fileAgg struct {
	mtime    int64
	size     int64
	msgs     []msgAgg
	lastTs   int64
	hadUsage bool
}

var (
	usageMu    sync.Mutex
	usageCache = map[string]*fileAgg{}
)

const maxLine = 64 * 1024 * 1024

type UsageReport struct {
	Accounts       []AccountUsage `json:"accounts"`
	Unattributed   AccountUsage   `json:"unattributed"`
	SharedTree     bool           `json:"shared_tree,omitempty"`
	SharedDir      string         `json:"shared_dir,omitempty"`
	LedgerEntries  int            `json:"ledger_entries"`
	LedgerSessions int            `json:"ledger_sessions"`
}

func (s *Store) Usage(acct Account) AccountUsage {
	rep := s.UsageAll()
	for _, u := range rep.Accounts {
		if u.AccountID == acct.ID {
			return u
		}
	}
	return AccountUsage{AccountID: acct.ID, Label: acct.Label}
}

func (s *Store) UsageAll() UsageReport {
	now := time.Now()
	todayKey := now.Format("2006-01-02")
	sevenKey := now.AddDate(0, 0, -6).Format("2006-01-02")

	accounts := s.Accounts()
	led := s.loadLedger()
	inferred := s.inferredFromSessionEnv()

	rep := UsageReport{LedgerSessions: len(led.bySession)}
	for _, ents := range led.bySession {
		rep.LedgerEntries += len(ents)
	}

	type acc struct {
		u           *AccountUsage
		byModel     map[string]*UsageStat
		sessions    map[string]bool
		lastTs      int64
		lastSession string
	}
	fresh := func(id, label string) *acc {
		return &acc{
			u:        &AccountUsage{AccountID: id, Label: label},
			byModel:  map[string]*UsageStat{},
			sessions: map[string]bool{},
		}
	}
	accs := map[string]*acc{}
	blocked := map[string]bool{}
	for _, a := range accounts {
		accs[a.ID] = fresh(a.ID, a.Label)
		if ls := s.LoginStatus(a.ID); ls.IdentityMismatch {
			blocked[a.ID] = true
			accs[a.ID].u.Error = "waiting for login" + asOtherAccount(ls.Email)
		}
	}
	unknown := fresh("", "Unattributed")

	dirs := map[string][]string{}
	for _, a := range accounts {
		d := s.ProjectsDir(a)
		if d == "" {
			if accs[a.ID].u.Error == "" {
				accs[a.ID].u.Error = "no transcripts in " + s.projectsDirFor(a)
			}
			continue
		}
		accs[a.ID].u.Dir = d
		dirs[d] = append(dirs[d], a.ID)
	}
	for d, ids := range dirs {
		if len(ids) > 1 {
			rep.SharedTree, rep.SharedDir = true, d
			break
		}
	}

	for dir, ids := range dirs {
		_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
				return nil
			}
			agg := aggregateFile(path)
			if agg == nil || !agg.hadUsage {
				return nil
			}
			for _, m := range agg.msgs {
				owner := led.accountAt(m.session, m.ts)
				wasInferred := false
				if owner == "" {
					if len(ids) == 1 {
						owner = ids[0]
					} else if cand, ok := inferred[m.session]; ok && contains(ids, cand) {
						owner, wasInferred = cand, true
					}
				}
				target := unknown
				if owner != "" && !blocked[owner] {
					if a, ok := accs[owner]; ok {
						target = a
					}
				}

				st := m.tokens
				st.CostUSD = costOf(st, m.model)
				day := time.Unix(m.ts, 0).Local().Format("2006-01-02")

				target.u.Total.add(st)
				if day >= sevenKey {
					target.u.Last7d.add(st)
				}
				if day == todayKey {
					target.u.Today.add(st)
				}
				if wasInferred {
					target.u.InferredTokens += st.TotalTokens()
				}
				bm := target.byModel[m.model]
				if bm == nil {
					bm = &UsageStat{}
					target.byModel[m.model] = bm
				}
				bm.add(st)
				target.sessions[m.session] = true
				if m.ts > target.lastTs {
					target.lastTs, target.lastSession = m.ts, m.session
				}
			}
			return nil
		})
	}

	closeOnce := func(a *acc) AccountUsage {
		a.u.Sessions = len(a.sessions)
		a.u.LastActivity = a.lastTs
		for model, st := range a.byModel {
			a.u.ByModel = append(a.u.ByModel, ModelUsage{Model: model, UsageStat: *st})
		}
		sort.Slice(a.u.ByModel, func(i, j int) bool {
			return a.u.ByModel[i].TotalTokens() > a.u.ByModel[j].TotalTokens()
		})
		if a.u.Sessions == 0 && a.u.Error == "" && a.u.Dir != "" {
			a.u.Error = "no usage attributed to this account"
		}
		return *a.u
	}
	for _, a := range accounts {
		rep.Accounts = append(rep.Accounts, closeOnce(accs[a.ID]))
	}
	rep.Unattributed = closeOnce(unknown)
	return rep
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

var (
	internMu sync.Mutex
	internTb = map[string]string{}
)

func intern(v string) string {
	internMu.Lock()
	defer internMu.Unlock()
	if got, ok := internTb[v]; ok {
		return got
	}
	internTb[v] = v
	return v
}

func sessionFromFile(path string) string {
	return strings.TrimSuffix(filepath.Base(path), ".jsonl")
}

func (s *Store) ProjectsDir(a Account) string {
	dir, err := s.resolvedProjectsDir(a)
	if err != nil {
		return ""
	}
	return dir
}

func (s *Store) resolvedProjectsDir(a Account) (string, error) {
	dir := s.projectsDirFor(a)
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	return real, nil
}

func (s *Store) projectsDirFor(a Account) string {
	if a.ConfigDir == "" {
		return filepath.Join(s.claudeHome, "projects")
	}
	return filepath.Join(a.ConfigDir, "projects")
}

func aggregateFile(path string) *fileAgg {
	fi, err := os.Stat(path)
	if err != nil {
		return nil
	}
	mtime, size := fi.ModTime().Unix(), fi.Size()

	usageMu.Lock()
	if cached, ok := usageCache[path]; ok && cached.mtime == mtime && cached.size == size {
		usageMu.Unlock()
		return cached
	}
	usageMu.Unlock()

	agg := &fileAgg{mtime: mtime, size: size}

	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1024*1024), maxLine)
	for sc.Scan() {
		line := sc.Bytes()
		if !strings.Contains(string(line), `"usage"`) {
			continue
		}
		var rec struct {
			Type      string `json:"type"`
			Timestamp string `json:"timestamp"`
			SessionID string `json:"sessionId"`
			Message   struct {
				Model string `json:"model"`
				Usage *struct {
					InputTokens              int64 `json:"input_tokens"`
					OutputTokens             int64 `json:"output_tokens"`
					CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
					CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
					CacheCreation            *struct {
						Ephemeral1h int64 `json:"ephemeral_1h_input_tokens"`
					} `json:"cache_creation"`
				} `json:"usage"`
			} `json:"message"`
		}
		if err := json.Unmarshal(line, &rec); err != nil || rec.Type != "assistant" || rec.Message.Usage == nil {
			continue
		}
		ts, err := time.Parse(time.RFC3339, rec.Timestamp)
		if err != nil {
			continue
		}
		if u := ts.Unix(); u > agg.lastTs {
			agg.lastTs = u
		}
		model := rec.Message.Model
		if model == "" {
			model = "unknown"
		}
		var st UsageStat
		st.InputTokens = rec.Message.Usage.InputTokens
		st.OutputTokens = rec.Message.Usage.OutputTokens
		st.CacheCreationTokens = rec.Message.Usage.CacheCreationInputTokens
		if cc := rec.Message.Usage.CacheCreation; cc != nil {
			n := cc.Ephemeral1h
			if n < 0 {
				n = 0
			}
			if n > rec.Message.Usage.CacheCreationInputTokens {
				n = rec.Message.Usage.CacheCreationInputTokens
			}
			st.CacheCreation1hTokens = n
		}
		st.CacheReadTokens = rec.Message.Usage.CacheReadInputTokens
		st.Messages = 1
		session := rec.SessionID
		if session == "" {
			session = sessionFromFile(path)
		}
		agg.msgs = append(agg.msgs, msgAgg{
			ts: ts.Unix(), session: intern(session), model: intern(model), tokens: st,
		})
		agg.hadUsage = true
	}

	usageMu.Lock()
	usageCache[path] = agg
	usageMu.Unlock()
	return agg
}
