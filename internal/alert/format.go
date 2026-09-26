package alert

import (
	"fmt"
	"sort"
	"strings"
)

// FormatMessage turns the Alertmanager payload into text ready for WhatsApp.
//
// Format:
//
//	1 alert:
//	  🚨 *FIRING* [critical] BinaryDown
//	  vps-manager binary down
//	  Instance 127.0.0.1:8765 has been down for > 3 minutes.
//	  ⏱ 2026-06-08 15:30 UTC
//
//	N alerts (batch):
//	  🚨 *Alertmanager* — firing: 2, resolved: 1
//	  ─────────────
//	  [critical] BinaryDown @ 127.0.0.1:8765
//	  [warning] LoginFailuresHigh — Rate > 5/min for 2m.
//	  ✅ [warning] WSCliff (resolved)
//
// Emojis stay only on the header line — the body is plain text so as not to
// clutter the phone notification.
func FormatMessage(p *Payload) string {
	if len(p.Alerts) == 0 {
		return ""
	}
	if len(p.Alerts) == 1 {
		return formatSingle(&p.Alerts[0])
	}
	return formatBatch(p)
}

func formatSingle(a *Alert) string {
	var b strings.Builder
	icon := iconFor(a.Status, a.Labels["severity"])
	name := a.Labels["alertname"]
	severity := a.Labels["severity"]
	if severity == "" {
		severity = "info"
	}

	state := "FIRING"
	if a.Status == "resolved" {
		state = "RESOLVED"
	}
	fmt.Fprintf(&b, "%s *%s* [%s] %s\n", icon, state, severity, name)

	if summary := a.Annotations["summary"]; summary != "" {
		b.WriteString(summary + "\n")
	}
	if desc := a.Annotations["description"]; desc != "" {
		b.WriteString(desc + "\n")
	}

	when := a.StartsAt
	if a.Status == "resolved" && !a.EndsAt.IsZero() {
		when = a.EndsAt
	}
	if !when.IsZero() {
		fmt.Fprintf(&b, "⏱ %s\n", when.UTC().Format("2006-01-02 15:04 UTC"))
	}

	return strings.TrimRight(b.String(), "\n")
}

func formatBatch(p *Payload) string {
	var b strings.Builder
	firing, resolved := 0, 0
	for i := range p.Alerts {
		if p.Alerts[i].Status == "resolved" {
			resolved++
		} else {
			firing++
		}
	}
	fmt.Fprintf(&b, "🚨 *Alertmanager* — firing: %d, resolved: %d\n", firing, resolved)
	b.WriteString(strings.Repeat("─", 13) + "\n")

	// Stable: firing first, then resolved; within each, by severity (critical>warning>info) and alertname.
	sorted := make([]Alert, len(p.Alerts))
	copy(sorted, p.Alerts)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Status != sorted[j].Status {
			return sorted[i].Status == "firing"
		}
		if severityRank(sorted[i].Labels["severity"]) != severityRank(sorted[j].Labels["severity"]) {
			return severityRank(sorted[i].Labels["severity"]) < severityRank(sorted[j].Labels["severity"])
		}
		return sorted[i].Labels["alertname"] < sorted[j].Labels["alertname"]
	})

	for i := range sorted {
		a := &sorted[i]
		severity := a.Labels["severity"]
		if severity == "" {
			severity = "info"
		}
		name := a.Labels["alertname"]
		instance := a.Labels["instance"]
		summary := a.Annotations["summary"]

		prefix := fmt.Sprintf("[%s] %s", severity, name)
		if a.Status == "resolved" {
			prefix = "✅ " + prefix + " (resolved)"
		}
		line := prefix
		if instance != "" {
			line += " @ " + instance
		}
		if summary != "" && a.Status != "resolved" {
			line += " — " + summary
		}
		b.WriteString(line + "\n")
	}

	return strings.TrimRight(b.String(), "\n")
}

func iconFor(status, severity string) string {
	if status == "resolved" {
		return "✅"
	}
	switch severity {
	case "critical":
		return "🚨"
	case "warning":
		return "⚠️"
	default:
		return "ℹ️"
	}
}

// severityRank gives an order for sort: lower number = more urgent.
func severityRank(s string) int {
	switch s {
	case "critical":
		return 0
	case "warning":
		return 1
	case "info":
		return 2
	default:
		return 3
	}
}

// PassesFilter returns true if at least one alert in the payload has a severity
// equal to or worse than minSeverity. Empty = no filter (everything passes).
func PassesFilter(p *Payload, minSeverity string) bool {
	if minSeverity == "" {
		return true
	}
	threshold := severityRank(minSeverity)
	for i := range p.Alerts {
		if severityRank(p.Alerts[i].Labels["severity"]) <= threshold {
			return true
		}
	}
	return false
}
