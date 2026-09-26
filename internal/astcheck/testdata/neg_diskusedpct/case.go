package caso

// Negative control measured in internal/queue/runners_watchdog.go:45 — "pct" here
// is a return name, not a command. A textual search fails this; the AST must not.
func diskUsedPct(path string) (pct int, err error) { return 0, nil }
