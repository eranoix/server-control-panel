package claudeacct

// usage.go — per-account Claude usage metrics.
//
// Models exactly how Claude Code itself tracks usage (the source /cost and
// ccusage read): every assistant message in a session transcript carries
// message.usage with input/output/cache token counts and the model. We walk
// <configdir>/projects/**/*.jsonl, sum tokens by model/day, and bucket into
// today / last-7d / total, plus an estimated equivalent API cost.
//
// ⚠️ THE ORIGINAL PREMISE IS DEAD — read this before trusting the numbers.
//
// This file used to claim that "usage is already physically separated by
// account", because CLAUDE_CONFIG_DIR is per account and each one had its own
// transcript tree. The live swap (a89bfc2) pointed both projects/ at the SAME
// /root/.claude-shared/projects, on purpose, so that `claude --continue` would
// survive an account switch. The physical separation stopped existing and
// nobody updated this comment.
//
// Two consequences, and the second is the dangerous one:
//
//  1. filepath.WalkDir does NOT follow a symlink, not even the root one — so
//     the sweep started visiting ZERO files and the panel showed 0 tokens for
//     a week, in silence. Fixed here by resolving the link before sweeping,
//     with an explicit error when the resolution fails.
//
//  2. With the shared tree, this file can NO longer say whose each token is:
//     the transcript carries no account identity (no email, no accountUuid).
//     Real attribution happens through a ledger of intervals fed by a hook.
//     Until then, Usage() reports the volume of the tree the account sees, and
//     the UI only shows that for the slot whose credential actually matches
//     the declared identity.
//
// Cost note: for Max (OAuth subscription) accounts the dollar figure is an
// "equivalent API cost" reference — billing is the subscription, not per token.
// The figure that actually matters for the quota motivation is token VOLUME per
// account over recent windows; cost is the convenience overlay.
//
// Performance: per-file aggregates are cached by (mtime,size). The first scan
// of a large tree (the jordan account holds all interactive dev sessions) is
// O(bytes); subsequent requests only re-parse files that changed.

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

// UsageStat is a token + cost tally over some window.
type UsageStat struct {
	InputTokens         int64 `json:"input_tokens"`
	OutputTokens        int64 `json:"output_tokens"`
	CacheCreationTokens int64 `json:"cache_creation_tokens"`
	// CacheCreation1hTokens is the slice of CacheCreationTokens written with the
	// 1h TTL (bills at 2x input, not the 5m rate of 1.25x).
	// CacheCreationTokens remains the TOTAL, so existing readers are unaffected.
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

// TotalTokens is the all-in token count (handy for sorting/display).
func (s UsageStat) TotalTokens() int64 {
	return s.InputTokens + s.OutputTokens + s.CacheCreationTokens + s.CacheReadTokens
}

// ModelUsage is one model's contribution to an account's total.
type ModelUsage struct {
	Model string `json:"model"`
	UsageStat
}

// AccountUsage is the per-account rollup the UI renders.
type AccountUsage struct {
	AccountID    string       `json:"account_id"`
	Label        string       `json:"label"`
	Today        UsageStat    `json:"today"`
	Last7d       UsageStat    `json:"last_7d"`
	Total        UsageStat    `json:"total"`
	ByModel      []ModelUsage `json:"by_model"`
	Sessions     int          `json:"sessions"`
	LastActivity int64        `json:"last_activity"` // unix seconds, 0 if none
	LastSession  UsageStat    `json:"last_session"`  // the most-recently-active session file
	// Error explains an EMPTY rollup instead of letting it read as "zero
	// usage". A silent 0 is indistinguishable from a broken scan,
	// which is exactly how the symlink regression survived a week unnoticed.
	Error string `json:"error,omitempty"`
	// InferredTokens is the slice of Total that came from the session-env
	// backfill instead of the ledger — deduced, not observed. Reported apart so
	// nobody mistakes one for the other.
	InferredTokens int64 `json:"inferred_tokens,omitempty"`
	// Dir is the transcript tree actually scanned, AFTER symlink resolution.
	// Two accounts reporting the same Dir is the tell that the tree is shared
	// and the split is not real yet.
	Dir string `json:"dir,omitempty"`
}

// ─── Pricing (per the claude-api skill, per token USD) ────────────────────

type modelPrice struct{ in, out float64 }

// priceFor maps a model id to per-token USD rates. Cache write ≈ 1.25×input,
// cache read ≈ 0.1×input (Anthropic prompt-caching economics).
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
		return modelPrice{5.0 / 1e6, 25.0 / 1e6} // unknown → assume opus tier
	}
}

// costOf prices one tally. Cache writes bill by TTL: 1h at 2x input, 5m at
// 1.25x. CacheCreation1hTokens is the 1h slice of the total, so the
// remainder is the 5m slice; tallies from transcripts without the breakdown
// carry 0 there and price entirely at 1.25x, as before. The subtraction is
// clamped so a malformed record cannot yield a negative 5m charge.
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

// ─── Per-file aggregate cache ─────────────────────────────────────────────

// msgAgg is ONE fact: an assistant message, its instant, its model and its
// tokens. The cache stores facts, not interpretation.
//
// That separation is what makes attribution viable. If the cache stored the
// total ALREADY attributed to an account, every new session — which appends a
// line to the ledger and therefore changes the interpretation — would invalidate
// the 402 MB of sweeping. Storing the raw fact, the ledger can change at will:
// only the query redoes the arithmetic, and that costs microseconds.
type msgAgg struct {
	ts     int64
	sessao string
	modelo string
	tokens UsageStat
}

type fileAgg struct {
	mtime    int64
	size     int64
	msgs     []msgAgg
	lastTs   int64 // unix of newest assistant msg
	hadUsage bool  // file contained ≥1 usage line
}

var (
	usageMu    sync.Mutex
	usageCache = map[string]*fileAgg{}
)

// maxLine caps the scanner buffer. Transcript lines embed full message content
// (tool results, large pastes) and routinely exceed bufio's 64KB default —
// without this the scan errors out mid-file and silently undercounts.
const maxLine = 64 * 1024 * 1024

// UsageReport is the whole picture in one pass: every account plus the volume
// that nothing can honestly claim.
type UsageReport struct {
	Accounts []AccountUsage `json:"accounts"`
	// Unattributed is the volume whose owner is genuinely unknown — transcripts
	// older than the ledger, or sessions it never saw. It is SHOWN, not hidden
	// and not folded into an account: a total that silently absorbs unknown
	// usage is exactly the kind of plausible-and-wrong number this whole ticket
	// exists to stop producing.
	Unattributed AccountUsage `json:"unattributed"`
	// SharedTree flags that two accounts resolve to the same transcript tree,
	// which is the normal state here (the swap needs it for --continue).
	SharedTree bool   `json:"shared_tree,omitempty"`
	SharedDir  string `json:"shared_dir,omitempty"`
	// LedgerEntries/LedgerSessions size the evidence behind the split, so the
	// panel can say how much of it is measured rather than assumed.
	LedgerEntries  int `json:"ledger_entries"`
	LedgerSessions int `json:"ledger_sessions"`
}

// Usage computes one account's rollup. Kept for callers that want a single
// account; it runs the full pass and picks one result out, so prefer UsageAll
// when you need more than one.
func (s *Store) Usage(acct Account) AccountUsage {
	rep := s.UsageAll()
	for _, u := range rep.Accounts {
		if u.AccountID == acct.ID {
			return u
		}
	}
	return AccountUsage{AccountID: acct.ID, Label: acct.Label}
}

// UsageAll walks every transcript ONCE and splits it across accounts using the
// attribution ledger.
//
// The walk is per DIRECTORY, not per account: with the shared tree both
// accounts resolve to the same files, and walking per account would read the
// whole tree twice and count every token twice — which is precisely the
// plausible-looking double count this ticket exists to prevent.
func (s *Store) UsageAll() UsageReport {
	now := time.Now()
	todayKey := now.Format("2006-01-02")
	sevenKey := now.AddDate(0, 0, -6).Format("2006-01-02")

	contas := s.Accounts()
	led := s.carregaLedger()
	inferido := s.inferidoPorSessionEnv()

	rep := UsageReport{LedgerSessions: len(led.porSessao)}
	for _, ents := range led.porSessao {
		rep.LedgerEntries += len(ents)
	}

	// One accumulator per account, plus the bucket for the unknown.
	type acc struct {
		u        *AccountUsage
		byModel  map[string]*UsageStat
		sessoes  map[string]bool
		ultimoTs int64
		ultimaSe string
	}
	novo := func(id, label string) *acc {
		return &acc{
			u:       &AccountUsage{AccountID: id, Label: label},
			byModel: map[string]*UsageStat{},
			sessoes: map[string]bool{},
		}
	}
	accs := map[string]*acc{}
	bloqueada := map[string]bool{}
	for _, a := range contas {
		accs[a.ID] = novo(a.ID, a.Label)
		// Identity gate: a slot whose credential belongs to another identity
		// receives no tokens at all.
		if ls := s.LoginStatus(a.ID); ls.IdentityMismatch {
			bloqueada[a.ID] = true
			accs[a.ID].u.Error = "waiting for login" + comoOutraConta(ls.Email)
		}
	}
	desconhecido := novo("", "Unattributed")

	// UNIQUE dirs: that is what avoids reading the shared tree twice.
	dirs := map[string][]string{} // resolved dir → accounts that see it
	for _, a := range contas {
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

	// Per unique dir: for each message, the ledger says whose it is.
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
				dono := led.contaEm(m.sessao, m.ts)
				inferida := false
				if dono == "" {
					// With no ledger, in order of strength of evidence:
					//
					//  1. A tree EXCLUSIVE to one account. Not a guess: if only
					//     one account sees these files, only it can have written
					//     them. It is the structural guarantee that held before
					//     the symlink, and it still holds wherever the dir is
					//     not shared.
					//  2. A SHARED tree: authorship is genuinely ambiguous. It
					//     falls to the session-env signal (which is per account)
					//     and, if not even that decides, to the unknown bucket.
					if len(ids) == 1 {
						dono = ids[0]
					} else if cand, ok := inferido[m.sessao]; ok && contem(ids, cand) {
						dono, inferida = cand, true
					}
				}
				// An account blocked by the identity gate falls into the unknown
				// bucket, NOT on the floor. The token existed; what is missing is
				// knowing which slot to credit it to. Dropping it silently would
				// break the one property that makes this panel auditable — that
				// sum(accounts) + unattributed == the tree's gross total.
				alvo := desconhecido
				if dono != "" && !bloqueada[dono] {
					if a, ok := accs[dono]; ok {
						alvo = a
					}
				}

				st := m.tokens
				st.CostUSD = costOf(st, m.modelo)
				dia := time.Unix(m.ts, 0).Local().Format("2006-01-02")

				alvo.u.Total.add(st)
				if dia >= sevenKey {
					alvo.u.Last7d.add(st)
				}
				if dia == todayKey {
					alvo.u.Today.add(st)
				}
				if inferida {
					alvo.u.InferredTokens += st.TotalTokens()
				}
				bm := alvo.byModel[m.modelo]
				if bm == nil {
					bm = &UsageStat{}
					alvo.byModel[m.modelo] = bm
				}
				bm.add(st)
				alvo.sessoes[m.sessao] = true
				if m.ts > alvo.ultimoTs {
					alvo.ultimoTs, alvo.ultimaSe = m.ts, m.sessao
				}
			}
			return nil
		})
	}

	fecha := func(a *acc) AccountUsage {
		a.u.Sessions = len(a.sessoes)
		a.u.LastActivity = a.ultimoTs
		for modelo, st := range a.byModel {
			a.u.ByModel = append(a.u.ByModel, ModelUsage{Model: modelo, UsageStat: *st})
		}
		sort.Slice(a.u.ByModel, func(i, j int) bool {
			return a.u.ByModel[i].TotalTokens() > a.u.ByModel[j].TotalTokens()
		})
		if a.u.Sessions == 0 && a.u.Error == "" && a.u.Dir != "" {
			a.u.Error = "no usage attributed to this account"
		}
		return *a.u
	}
	for _, a := range contas {
		rep.Accounts = append(rep.Accounts, fecha(accs[a.ID]))
	}
	rep.Unattributed = fecha(desconhecido)
	return rep
}

func contem(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// intern reuses the repeated strings (session and model repeat on every message
// in the file). Without it the fact cache would hold tens of thousands of copies
// of the same few dozen strings.
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

// sessaoDoArquivo is the fallback when the line carries no sessionId: the
// transcript's name IS the session id.
func sessaoDoArquivo(path string) string {
	return strings.TrimSuffix(filepath.Base(path), ".jsonl")
}

// ProjectsDir is the transcript tree an account WOULD scan, with symlinks
// resolved, independent of whether the account passes the identity gate. The
// caller needs it precisely for the gated case: two slots pointing at the same
// real tree means no per-account number is a slice yet, and the panel has to
// say so even while one of the slots is showing nothing.
func (s *Store) ProjectsDir(a Account) string {
	dir, err := s.resolvedProjectsDir(a)
	if err != nil {
		return ""
	}
	return dir
}

// resolvedProjectsDir resolves the account's transcript tree, FOLLOWING
// symlinks. This is load-bearing, not hygiene: filepath.WalkDir stats with
// Lstat and refuses to descend a symlinked root, so when the account dirs
// became symlinks into /root/.claude-shared/projects the walk started visiting
// exactly one entry — the link itself — and every metric collapsed to zero
// without a single error surfacing.
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

// aggregateFile parses one transcript, cached by (mtime,size). Each .jsonl is
// one session, so a file with any usage line counts as one session.
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
		// Cheap pre-filter: only assistant lines carry usage.
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
					// Per-TTL split of CacheCreationInputTokens; pointer so an old
					// transcript (absent) is distinguishable from a present zero.
					CacheCreation *struct {
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
		// The session comes from the LINE, not from the file name: a `--continue`
		// can append to an existing transcript, and the name would lie.
		sessao := rec.SessionID
		if sessao == "" {
			sessao = sessaoDoArquivo(path)
		}
		agg.msgs = append(agg.msgs, msgAgg{
			ts: ts.Unix(), sessao: intern(sessao), modelo: intern(model), tokens: st,
		})
		agg.hadUsage = true
	}

	usageMu.Lock()
	usageCache[path] = agg
	usageMu.Unlock()
	return agg
}
