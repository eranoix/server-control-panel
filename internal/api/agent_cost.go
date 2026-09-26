// agent_cost.go — cost/token aggregation (VPSM agent-ops #3).
//
// A cancelable ticker (~60s) walks every mapped dtach session (agentCWD), finds
// its newest Claude Code JSONL under <ClaudeHome>/projects/<mangled-cwd>/, sums
// the `usage` tokens, prices them via the hardcoded table below, and writes
// tokens+cost into <DataDir>/session-status.json. Parsing is cached by JSONL
// mtime so unchanged transcripts are not re-read.
package api

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ─── price table ────────────────────────────────────────────────────────────

// modelPrice is USD per MILLION tokens for one model family.
//   - in / out:      base input & output list prices.
//   - cacheWrite:    5-minute-TTL cache write = 1.25× input.
//   - cacheWrite1h:  1-hour-TTL cache write   = 2× input.
//   - cacheRead:     cache read = 0.1× input.
//
// The two write rates are NOT interchangeable and the split is not cosmetic:
// sessions on this host run the 1h TTL almost exclusively, so pricing every
// write at the 5m rate under-reported cache cost by 1.6×. Which TTL
// a write used comes from usage.cache_creation.ephemeral_{1h,5m}_input_tokens.
type modelPrice struct {
	in, out, cacheWrite, cacheWrite1h, cacheRead float64
}

// modelPriceTable — current public Anthropic list prices (per MTok), matched by
// substring against the CC transcript's `message.model` string, longest/most-
// specific first (first hit wins). Current-generation Opus (4.5–5) bills at
// standard rates with no 1M-context premium; legacy Opus (4.0/4.1/3) at the old
// $15/$75. Sonnet (5/4.6/4.5) $3/$15; Haiku (4.5) $1/$5.
//
// Sources: Anthropic pricing (Jan 2026). Cache multipliers are the documented
// 1.25× (write, 5m TTL) and 0.1× (read) of the input rate.
//
// Opus 5 ($5/$25, same as 4.8 — no 1M premium) was confirmed empirically, not
// assumed: a one-shot `claude -p --model 'claude-opus-5[1m]'` reported
// total_cost_usd 0.78603 for in=2, out=4, cache_creation=78592 (1h TTL), and
// 2/1e6*5 + 4/1e6*25 + 78592/1e6*10 = 0.78603 exactly — which is also the direct
// confirmation that a 1h write bills at 2× input, not 1.25×.
//
// Opus 5.5 ($4/$20) is CHEAPER than Opus 5 and MUST precede the "opus-5" row:
// the match is by substring, so "claude-opus-5-5" also contains "opus-5" and
// would otherwise bill at the older, higher rates. Its cache read is 5% of
// input (0.20), not the usual 10%. Measured the same way as above: in=2, out=4,
// cache_creation=58850 (1h TTL) reported costUSD 0.470888, and
// 2/1e6*4 + 4/1e6*20 + 58850/1e6*8 = 0.470888 exactly — the 1h write is 2×
// input here too.
var modelPriceTable = []struct {
	match string
	price modelPrice
}{
	{"opus-5-5", modelPrice{4, 20, 5, 8, 0.20}},
	{"opus-5", modelPrice{5, 25, 6.25, 10, 0.50}},
	{"opus-4-8", modelPrice{5, 25, 6.25, 10, 0.50}},
	{"opus-4-7", modelPrice{5, 25, 6.25, 10, 0.50}},
	{"opus-4-6", modelPrice{5, 25, 6.25, 10, 0.50}},
	{"opus-4-5", modelPrice{5, 25, 6.25, 10, 0.50}},
	{"opus-4-1", modelPrice{15, 75, 18.75, 30, 1.50}},
	{"opus-4", modelPrice{15, 75, 18.75, 30, 1.50}}, // 4.0
	{"opus-3", modelPrice{15, 75, 18.75, 30, 1.50}},
	{"sonnet", modelPrice{3, 15, 3.75, 6, 0.30}},
	{"haiku", modelPrice{1, 5, 1.25, 2, 0.10}},
}

// defaultModelPrice is used when the model string matches nothing (the router
// on this host runs claude-opus-5-5[1m], so default to current Opus rates).
var defaultModelPrice = modelPrice{4, 20, 5, 8, 0.20}

func priceForModel(model string) modelPrice {
	m := strings.ToLower(model)
	for _, row := range modelPriceTable {
		if strings.Contains(m, row.match) {
			return row.price
		}
	}
	return defaultModelPrice
}

// costForModel prices one model's token totals.
//
// CacheCreation is the TOTAL of both TTLs and CacheCreation1h the 1h slice, so
// the 5m slice is the difference. Transcripts predating the breakdown carry
// CacheCreation1h == 0 and price entirely at the 5m rate — the old behaviour,
// which stays correct for them. The subtraction is clamped: a malformed line
// claiming more 1h than total must not produce a negative 5m charge.
func costForModel(model string, t AgentTokens) float64 {
	p := priceForModel(model)
	cc1h := t.CacheCreation1h
	if cc1h > t.CacheCreation {
		cc1h = t.CacheCreation
	}
	if cc1h < 0 {
		cc1h = 0
	}
	cc5m := t.CacheCreation - cc1h
	return float64(t.In)/1e6*p.in +
		float64(t.Out)/1e6*p.out +
		float64(cc5m)/1e6*p.cacheWrite +
		float64(cc1h)/1e6*p.cacheWrite1h +
		float64(t.CacheRead)/1e6*p.cacheRead
}

// ─── JSONL parsing ──────────────────────────────────────────────────────────

type ccUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	// CacheCreation is the per-TTL breakdown of CacheCreationInputTokens. A
	// pointer so "absent" (old transcript) is distinguishable from "present and
	// zero"; nil simply leaves cache1h at 0 → everything priced at the 5m rate.
	CacheCreation *struct {
		Ephemeral1h int64 `json:"ephemeral_1h_input_tokens"`
		Ephemeral5m int64 `json:"ephemeral_5m_input_tokens"`
	} `json:"cache_creation"`
}

// cache1h is the 1h-TTL slice of this line's cache writes (0 when the transcript
// predates the breakdown). Clamped to the reported total so a malformed line
// cannot inflate the bill beyond the tokens actually written.
func (u *ccUsage) cache1h() int64 {
	if u == nil || u.CacheCreation == nil {
		return 0
	}
	n := u.CacheCreation.Ephemeral1h
	if n < 0 {
		return 0
	}
	if n > u.CacheCreationInputTokens {
		return u.CacheCreationInputTokens
	}
	return n
}

type ccEnvelope struct {
	Model string   `json:"model,omitempty"`
	Usage *ccUsage `json:"usage,omitempty"`
}

type ccLine struct {
	Type      string          `json:"type"`
	SessionID string          `json:"sessionId,omitempty"`
	Timestamp string          `json:"timestamp,omitempty"`
	Message   json.RawMessage `json:"message"`
}

// parseTranscriptUsage sums a JSONL's assistant `usage` blocks (tokens per
// model), returns the total tokens, total cost across models, the session id
// (JSONL basename authority), and a per-DAY cost map (date "2006-01-02" → USD,
// bucketed by each assistant line's timestamp) used by the spend-ceiling checker
// (agent_budget.go, #55). Malformed lines are skipped.
func parseTranscriptUsage(path string) (AgentTokens, float64, string, map[string]float64, error) {
	f, err := os.Open(path)
	if err != nil {
		return AgentTokens{}, 0, "", nil, err
	}
	defer f.Close()

	perModel := map[string]*AgentTokens{}
	// perDayModel[date][model] accumulates tokens so each day is priced per model.
	perDayModel := map[string]map[string]*AgentTokens{}
	var total AgentTokens
	sessionID := strings.TrimSuffix(filepath.Base(path), ".jsonl")

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 16<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var tl ccLine
		if err := json.Unmarshal(line, &tl); err != nil {
			continue
		}
		if tl.Type != "assistant" {
			continue
		}
		var env ccEnvelope
		if err := json.Unmarshal(tl.Message, &env); err != nil || env.Usage == nil {
			continue
		}
		u := env.Usage
		cc1h := u.cache1h()
		total.In += u.InputTokens
		total.Out += u.OutputTokens
		total.CacheRead += u.CacheReadInputTokens
		total.CacheCreation += u.CacheCreationInputTokens
		total.CacheCreation1h += cc1h
		pm := perModel[env.Model]
		if pm == nil {
			pm = &AgentTokens{}
			perModel[env.Model] = pm
		}
		pm.In += u.InputTokens
		pm.Out += u.OutputTokens
		pm.CacheRead += u.CacheReadInputTokens
		pm.CacheCreation += u.CacheCreationInputTokens
		pm.CacheCreation1h += cc1h
		// Day bucket: the timestamp is RFC3339 ("2006-01-02T15:04:05Z…"); the
		// leading 10 chars are the date. Empty/short timestamps skip bucketing
		// (they still count toward the lifetime total above).
		if len(tl.Timestamp) >= 10 {
			date := tl.Timestamp[:10]
			dm := perDayModel[date]
			if dm == nil {
				dm = map[string]*AgentTokens{}
				perDayModel[date] = dm
			}
			dpm := dm[env.Model]
			if dpm == nil {
				dpm = &AgentTokens{}
				dm[env.Model] = dpm
			}
			dpm.In += u.InputTokens
			dpm.Out += u.OutputTokens
			dpm.CacheRead += u.CacheReadInputTokens
			dpm.CacheCreation += u.CacheCreationInputTokens
			dpm.CacheCreation1h += cc1h
		}
	}
	if err := sc.Err(); err != nil {
		return AgentTokens{}, 0, "", nil, err
	}
	var cost float64
	for model, tok := range perModel {
		cost += costForModel(model, *tok)
	}
	byDay := make(map[string]float64, len(perDayModel))
	for date, dm := range perDayModel {
		var c float64
		for model, tok := range dm {
			c += costForModel(model, *tok)
		}
		byDay[date] = c
	}
	return total, cost, sessionID, byDay, nil
}

// newestProjectJSONL returns the newest .jsonl in the CC project dir mapped
// from cwd (<claudeHome>/projects/<mangled-cwd>/). "" when none.
func newestProjectJSONL(claudeHome, cwd string) string {
	dir := filepath.Join(claudeHome, "projects", mangleProjectDir(cwd))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var best string
	var bestMod int64
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if m := info.ModTime().UnixNano(); m > bestMod {
			bestMod, best = m, filepath.Join(dir, e.Name())
		}
	}
	return best
}

// ─── aggregator ─────────────────────────────────────────────────────────────

// agentCostEntry caches a session's last-parsed result keyed by JSONL mtime so
// unchanged transcripts are not re-read.
type agentCostEntry struct {
	path   string
	mtime  int64
	tokens AgentTokens
	cost   float64
	cc     string
	byDay  map[string]float64 // date → USD (for spend-ceiling day/month totals)
}

// startAgentStatusAggregator launches the periodic cost/token aggregator.
// Cancelable via ctx; no-op when the stores are unavailable (degraded boot).
func (r *Router) startAgentStatusAggregator(ctx context.Context) {
	if r == nil || r.agentStatus == nil || r.agentCWD == nil {
		return
	}
	if r.agentCostCache == nil {
		r.agentCostCache = map[string]agentCostEntry{}
	}
	go func() {
		t := time.NewTicker(60 * time.Second)
		defer t.Stop()
		r.aggregateAgentCosts() // prime immediately at boot
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				r.aggregateAgentCosts()
			}
		}
	}()
}

// aggregateAgentCosts runs one pass: for each mapped session, refresh
// tokens+cost from its newest JSONL (mtime-cached), then prune the status file
// to the currently-known session set. Runs only on the aggregator goroutine, so
// r.agentCostCache needs no lock.
func (r *Router) aggregateAgentCosts() {
	home := r.cfg.ClaudeHome
	if home == "" {
		home = "/root/.claude"
	}
	cwds := r.agentCWD.All()
	known := make(map[string]bool, len(cwds))
	for name, cwd := range cwds {
		known[name] = true
		jsonl := newestProjectJSONL(home, cwd)
		if jsonl == "" {
			continue
		}
		info, err := os.Stat(jsonl)
		if err != nil {
			continue
		}
		mtime := info.ModTime().UnixNano()
		if c, ok := r.agentCostCache[name]; ok && c.path == jsonl && c.mtime == mtime {
			r.agentStatus.setTokens(name, c.tokens, c.cost, c.cc)
			continue
		}
		tok, cost, cc, byDay, err := parseTranscriptUsage(jsonl)
		if err != nil {
			continue
		}
		r.agentCostCache[name] = agentCostEntry{path: jsonl, mtime: mtime, tokens: tok, cost: cost, cc: cc, byDay: byDay}
		r.agentStatus.setTokens(name, tok, cost, cc)
	}
	// Drop cache entries for sessions no longer mapped, then prune the file.
	for name := range r.agentCostCache {
		if !known[name] {
			delete(r.agentCostCache, name)
		}
	}
	r.agentStatus.prune(known)

	// Spend ceilings (#55): roll up day/month/per-session totals from the cached
	// per-day cost maps and alert on any exceeded cap (alert-only, no auto-halt).
	today := time.Now().Format("2006-01-02")
	thisMonth := time.Now().Format("2006-01")
	var dayTotal, monthTotal float64
	perSession := make(map[string]float64, len(r.agentCostCache))
	for name, c := range r.agentCostCache {
		perSession[name] = c.cost
		for date, cost := range c.byDay {
			if date == today {
				dayTotal += cost
			}
			if strings.HasPrefix(date, thisMonth) {
				monthTotal += cost
			}
		}
	}
	r.checkAgentBudgets(dayTotal, monthTotal, perSession)
}
