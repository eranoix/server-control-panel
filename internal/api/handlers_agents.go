package api

import (
	"net/http"
	"sort"
)

// handlers_agents.go — unified view of the agent sessions (kanban + cost
// dashboard). Joins the state (session-status.json, populated by the hooks + the
// cost aggregator) with the cwd (session-cwd.json). Primary-only.

type agentSessionView struct {
	Name    string       `json:"name"`
	State   string       `json:"state"`
	Updated int64        `json:"updated"`
	CostUSD float64      `json:"cost_usd"`
	Tokens  *AgentTokens `json:"tokens,omitempty"`
	CWD     string       `json:"cwd,omitempty"`
	CC      string       `json:"cc_session,omitempty"`
}

// GET /api/agent/sessions
func (r *Router) handleAgentSessions(w http.ResponseWriter, req *http.Request) {
	if _, ok := r.mustPrimary(w, req); !ok {
		return
	}
	status := map[string]AgentStatus{}
	if r.agentStatus != nil {
		status = r.agentStatus.Snapshot()
	}
	cwds := map[string]string{}
	if r.agentCWD != nil {
		cwds = r.agentCWD.All()
	}
	names := map[string]bool{}
	for n := range status {
		names[n] = true
	}
	for n := range cwds {
		names[n] = true
	}
	out := make([]agentSessionView, 0, len(names))
	counts := map[string]int{}
	var total float64
	for n := range names {
		st := status[n]
		state := st.State
		if state == "" {
			state = agentStateIdle
		}
		out = append(out, agentSessionView{
			Name: n, State: state, Updated: st.Updated,
			CostUSD: st.CostUSD, Tokens: st.Tokens, CWD: cwds[n], CC: st.CCSession,
		})
		total += st.CostUSD
		counts[state]++
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Updated != out[j].Updated {
			return out[i].Updated > out[j].Updated
		}
		return out[i].Name < out[j].Name
	})
	writeJSON(w, map[string]any{
		"sessions":       out,
		"total_cost_usd": total,
		"counts":         counts,
	})
}
