package notify

import "strings"

// Rule routes events to channels. An event matches a rule when ALL configured
// predicates pass (empty predicate = wildcard). A matching rule fans the event
// out to every channel ID in Channels. Rules are persisted to
// <DataDir>/notify/rules.json and edited via the Alertas tab.
//
// Matching predicates (all optional, AND-combined):
//   - TypePrefix:   strings.HasPrefix(ev.Type, TypePrefix). "job." matches all
//     job.* events; "job.failed" matches exactly that. "" matches any type.
//   - MinSeverity:  ev severity rank >= rule rank. "" = info (matches all).
//   - SourcePrefix: strings.HasPrefix(ev.Source, SourcePrefix). Lets a rule
//     target only scheduler-originated jobs ("scheduler:") etc.
//   - Labels:       every k=v must equal ev.Labels[k] (exact). Enables routing
//     by kind ("kind"="shell"), origin ("origin"="ai"), owner, and so on.
type Rule struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Enabled      bool              `json:"enabled"`
	TypePrefix   string            `json:"type_prefix,omitempty"`
	MinSeverity  string            `json:"min_severity,omitempty"`
	SourcePrefix string            `json:"source_prefix,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
	Channels     []string          `json:"channels"` // ChannelDef IDs
}

// matches reports whether ev satisfies every configured predicate. A disabled
// rule never matches. Called only from the Router worker against a
// copy-on-write snapshot, so it needs no locking of its own.
func (rl Rule) matches(ev Event) bool {
	if !rl.Enabled {
		return false
	}
	if rl.TypePrefix != "" && !strings.HasPrefix(ev.Type, rl.TypePrefix) {
		return false
	}
	if rl.MinSeverity != "" && severityRank(ev.Severity) < severityRank(rl.MinSeverity) {
		return false
	}
	if rl.SourcePrefix != "" && !strings.HasPrefix(ev.Source, rl.SourcePrefix) {
		return false
	}
	for k, v := range rl.Labels {
		if ev.Labels[k] != v {
			return false
		}
	}
	return true
}
