package notify

import "strings"

type Rule struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Enabled      bool              `json:"enabled"`
	TypePrefix   string            `json:"type_prefix,omitempty"`
	MinSeverity  string            `json:"min_severity,omitempty"`
	SourcePrefix string            `json:"source_prefix,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
	Channels     []string          `json:"channels"`
}

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
