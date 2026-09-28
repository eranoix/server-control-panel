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

type modelPrice struct {
	in, out, cacheWrite, cacheWrite1h, cacheRead float64
}

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
	{"opus-4", modelPrice{15, 75, 18.75, 30, 1.50}},
	{"opus-3", modelPrice{15, 75, 18.75, 30, 1.50}},
	{"sonnet", modelPrice{3, 15, 3.75, 6, 0.30}},
	{"haiku", modelPrice{1, 5, 1.25, 2, 0.10}},
}

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

type ccUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheCreation            *struct {
		Ephemeral1h int64 `json:"ephemeral_1h_input_tokens"`
		Ephemeral5m int64 `json:"ephemeral_5m_input_tokens"`
	} `json:"cache_creation"`
}

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

func parseTranscriptUsage(path string) (AgentTokens, float64, string, map[string]float64, error) {
	f, err := os.Open(path)
	if err != nil {
		return AgentTokens{}, 0, "", nil, err
	}
	defer f.Close()

	perModel := map[string]*AgentTokens{}
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

type agentCostEntry struct {
	path   string
	mtime  int64
	tokens AgentTokens
	cost   float64
	cc     string
	byDay  map[string]float64
}

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
		r.aggregateAgentCosts()
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
	for name := range r.agentCostCache {
		if !known[name] {
			delete(r.agentCostCache, name)
		}
	}
	r.agentStatus.prune(known)

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
