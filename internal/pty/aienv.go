// Package pty — aienv.go: the `?ai=` query param on /ws/shell used to pin a
// terminal pane at a non-default upstream (the proxy / uncensored / venice
// bridges), so different panes could run different AI backends.
//
// The app was consolidated to Claude-only: there are no alternate providers
// left. Every pane uses the default Claude Code config, which goes through the
// claude-router OAuth passthrough (ANTHROPIC_BASE_URL=:8788 from settings.json).
// These stubs keep HostShell's call site (pty.go) stable without the old
// env-file lookup machinery.

package pty

import "strings"

// loadAIEnv returns extra env vars for a pane's AI provider. Claude-only: always
// nil — no override; the pane's `claude` uses the router OAuth upstream.
func loadAIEnv(_, _ string) []string { return nil }

// safeAIProvider sanitizes the `?ai=` value. After the Claude-only
// consolidation only the OAuth-equivalent values remain; anything else → "".
func safeAIProvider(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "oauth", "router":
		return "oauth"
	default:
		return ""
	}
}
