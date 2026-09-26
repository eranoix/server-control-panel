// agent_budget.go — VPSM Wave-3 #55: spend ceilings (ALERT-ONLY).
//
// Configurable USD caps (per-session, per-day, per-month) live in a tiny config
// <DataDir>/agent-budget.json (all default 0 = off). The cost aggregator
// (agent_cost.go) computes the day/month/per-session totals each pass and calls
// checkAgentBudgets, which dispatches a notify Event (reusing the notify spine)
// when a cap is exceeded — throttled to ONCE per breach per period so a busy day
// doesn't spam.
//
// Deliberately NO auto-halt: killing a live agent session mid-work is too risky
// (lost context / half-applied edits). Auto-halt is noted as a FUTURE option; for
// now the operator decides what to do when alerted.
package api

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"server-control-panel/internal/notify"
)

// AgentBudget holds the configurable spend ceilings, USD. 0 = that cap is off.
type AgentBudget struct {
	DailyUSD   float64 `json:"daily_usd,omitempty"`
	MonthlyUSD float64 `json:"monthly_usd,omitempty"`
	SessionUSD float64 `json:"session_usd,omitempty"`
}

// agentBudgetStore owns <DataDir>/agent-budget.json (atomic temp+rename).
type agentBudgetStore struct {
	mu   sync.Mutex
	path string
	b    AgentBudget
}

func newAgentBudgetStore(path string) *agentBudgetStore {
	s := &agentBudgetStore{path: path}
	if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
		_ = json.Unmarshal(data, &s.b) // tolerate corruption: caps stay off
	}
	return s
}

// Get returns a copy of the current budget. nil-safe.
func (s *agentBudgetStore) Get() AgentBudget {
	if s == nil {
		return AgentBudget{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b
}

// Set persists a new budget atomically. nil-safe.
func (s *agentBudgetStore) Set(b AgentBudget) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.b = b
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.b, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// checkAgentBudgets dispatches an alert for each exceeded cap. It runs ONLY on
// the aggregator goroutine (agent_cost.go), so r.budgetNotified needs no lock.
// Throttle: the DedupKey embeds the period (day/month), and budgetNotified
// guards against re-dispatch within the SAME process across ticks — so each
// breach fires at most once per period.
func (r *Router) checkAgentBudgets(dayTotal, monthTotal float64, perSession map[string]float64) {
	if r == nil || r.agentBudget == nil || r.notify == nil {
		return
	}
	b := r.agentBudget.Get()
	if b.DailyUSD <= 0 && b.MonthlyUSD <= 0 && b.SessionUSD <= 0 {
		return // all caps off — nothing to do
	}
	if r.budgetNotified == nil {
		r.budgetNotified = map[string]bool{}
	}
	now := time.Now()
	day := now.Format("2006-01-02")
	month := now.Format("2006-01")

	fireOnce := func(dedup, title, body string) {
		if r.budgetNotified[dedup] {
			return
		}
		r.budgetNotified[dedup] = true
		r.notify.Dispatch(notify.Event{
			Type:     notify.TypeMetricThreshold,
			Severity: notify.SeverityWarning,
			Source:   "agent-budget",
			Owner:    r.cfg.Primary,
			Title:    title,
			Body:     body,
			Labels:   map[string]string{"kind": "agent_budget"},
			TS:       now.Unix(),
			DedupKey: dedup,
		})
	}

	if b.DailyUSD > 0 && dayTotal > b.DailyUSD {
		fireOnce("agent-budget:daily:"+day,
			fmt.Sprintf("Daily agent cost above the cap: $%.2f > $%.2f", dayTotal, b.DailyUSD),
			fmt.Sprintf("Spend accumulated today (%s) on agent sessions: $%.2f. Daily cap: $%.2f. (Alert only — no session was interrupted.)", day, dayTotal, b.DailyUSD))
	}
	if b.MonthlyUSD > 0 && monthTotal > b.MonthlyUSD {
		fireOnce("agent-budget:monthly:"+month,
			fmt.Sprintf("Monthly agent cost above the cap: $%.2f > $%.2f", monthTotal, b.MonthlyUSD),
			fmt.Sprintf("Spend accumulated this month (%s) on agent sessions: $%.2f. Monthly cap: $%.2f. (Alert only.)", month, monthTotal, b.MonthlyUSD))
	}
	if b.SessionUSD > 0 {
		for name, cost := range perSession {
			if cost > b.SessionUSD {
				fireOnce("agent-budget:session:"+name+":"+day,
					fmt.Sprintf("Session %s above the per-session cap: $%.2f > $%.2f", name, cost, b.SessionUSD),
					fmt.Sprintf("Agent session %q has accumulated $%.2f (per-session cap: $%.2f). (Alert only.)", name, cost, b.SessionUSD))
			}
		}
	}
}
