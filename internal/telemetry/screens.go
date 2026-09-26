// Package telemetry implements usage instrumentation for the panel's screens:
// the canonical list of ids, the append-only JSONL sink and the
// POST /api/telemetry endpoint.
package telemetry

import (
	_ "embed"
	"strings"
)

// screensRaw is the canonical list of screen ids: tab-level ids, sub-actions
// and the `unknown` bucket. The ids are a wire contract with the clients, and
// some (for example `system.fans` and the `games.*` group) are never
// emitted by this build and report 0 by design.
//
// The file must stay byte-identical to the list used by the other deployment,
// because triage matches the two JSONL outputs by the file's sha256. That is
// why it carries no comments and no blank lines.
//
//go:embed screens.txt
var screensRaw string

// screenList is the list in file order, materialized once.
var screenList = func() []string {
	lines := strings.Split(strings.TrimSpace(screensRaw), "\n")
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}()

var screenSet = func() map[string]struct{} {
	m := make(map[string]struct{}, len(screenList))
	for _, l := range screenList {
		m[l] = struct{}{}
	}
	return m
}()

// IsKnownScreen tells whether the id came from the canonical list. An unknown
// id is NEVER written raw to the JSONL — it becomes the "unknown" bucket
// (tampering / log poisoning).
func IsKnownScreen(id string) bool { _, ok := screenSet[id]; return ok }

// AllScreens returns the list in file order. The report needs it in order to
// print with 0 the screens nobody opened — a screen missing from the output is a
// screen invisible to triage.
//
// It returns a copy: the internal slice must not be shuffled by the caller.
func AllScreens() []string {
	out := make([]string, len(screenList))
	copy(out, screenList)
	return out
}
