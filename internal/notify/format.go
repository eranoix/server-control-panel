package notify

import "strings"

// FormatText renders an Event as a plain-text message (PT-BR), the default body
// for text channels like WhatsApp. It is deliberately dependency-free and
// channel-agnostic — a webhook channel would marshal the Event as JSON
// instead of calling this. alert/format.go stays dedicated to the Alertmanager
// payload; this is the Event-native formatter.
func FormatText(ev Event) string {
	var b strings.Builder
	b.WriteString(severityEmoji(ev.Severity))
	b.WriteString(" ")
	if ev.Title != "" {
		b.WriteString(ev.Title)
	} else {
		b.WriteString(ev.Type)
	}

	// Context subline: "kind · origin · owner" from whatever labels exist.
	var parts []string
	if k := ev.Labels["kind"]; k != "" {
		parts = append(parts, k)
	}
	if o := ev.Labels["origin"]; o != "" {
		parts = append(parts, o)
	}
	if ev.Owner != "" {
		parts = append(parts, ev.Owner)
	}
	if len(parts) > 0 {
		b.WriteString("\n")
		b.WriteString(strings.Join(parts, " · "))
	}

	if ev.Body != "" {
		b.WriteString("\n")
		b.WriteString(ev.Body)
	}
	return b.String()
}

func severityEmoji(sev string) string {
	switch sev {
	case SeverityCritical:
		return "🔴"
	case SeverityWarning:
		return "🟡"
	default:
		return "🔵"
	}
}
