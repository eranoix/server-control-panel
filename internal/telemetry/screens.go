// Package telemetry implements usage instrumentation for the panel's screens:
// the canonical list of ids, the append-only JSONL sink and the
// POST /api/telemetry endpoint.
package telemetry

import (
	_ "embed"
	"strings"
)

// The canonical list comes from docs/telemetry-screens.txt in the `Servidor`
// repository, derived 1:1 from docs/INVENTARIO-LAB.md: 46 tab-level ids, 29
// sub-actions and the `unknown` bucket — 76 lines.
//
// A later re-edition (+5 ids), done in BOTH forks in the same act because the
// home fork is frozen right afterwards and after that parity could no longer
// be redone:
//
//	`config`             — the panel's standalone Settings screen. It has always
//	                       existed in this fork and fell into the `unknown` bucket
//	                       only because it was born after the list. Like
//	                       `dashboard`, it belongs to none of the 8 groups.
//	`operacoes.proxmox`  — the Proxmox tab.
//	`operacoes.nos`      — the Nodes screen. It was MERGED into the Proxmox tab, so
//	                       nothing in this fork emits it today; the id exists for
//	                       the node axis, and reports 0 until then — same contract
//	                       as `sistema.ventoinhas` below.
//	`operacoes.backup`   — per-node backup and restore; the tab does not exist yet.
//	`operacoes.embutidas`— embedded tools behind a proxy; likewise.
//
// Documented exception: `sistema.ventoinhas` is the ONLY id outside the
// inventory's 8-group table, and it does NOT exist in this fork — it is the
// sub-tab of the fanhub proxy (see the technical documentation), which only the
// VM's panel has. It stays on the list here anyway, because the file has to be
// byte-identical in both forks (the sha256 is what triage uses to add the two
// JSONLs together). This fork's screen map simply never emits it, and the
// report prints it with 0 — which is the correct information, not a gap.
//
// Symmetrical: the `jogos.*` group is still on the list and in this fork is out
// of the navigation (the "Jogos" item was removed from the menu when Enshrouded
// migrated, see the technical documentation). It also reports 0, same reason.
//
// The file is byte-identical in both forks; parity is checked by sha256 in
// infra/verify/screens-parity.sh. It carries NO comment and no blank line
// precisely so the sha256 can match.
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
