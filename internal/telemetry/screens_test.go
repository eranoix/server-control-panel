package telemetry

import (
	"regexp"
	"strings"
	"testing"
)

// gruposConhecidos are the 8 navigation groups from docs/INVENTARIO-LAB.md,
// plus the two SOLO screens — `dashboard` and `config` — which belong to no
// group at all because they do not live in a tab bar.
var gruposConhecidos = map[string]struct{}{
	"dashboard": {}, "sistema": {}, "jogos": {}, "docker": {},
	"dev": {}, "seguranca": {}, "apps": {}, "operacoes": {},
	"config": {},
}

// telasSolo are the legitimate ONE-segment ids: a screen that exists alone,
// with no tab bar. Any other 1-segment id is a group emitted without a tab.
var telasSolo = map[string]struct{}{"dashboard": {}, "config": {}}

const totalEsperado = 76 // 46 tab-level + 29 sub-actions + the `unknown` bucket

// TestScreenIDsMatchInventory is the test the research demands: it fails any id
// that does not match the inventory. Without it, triage receives ids that
// correspond to no screen at all and gets stuck.
func TestScreenIDsMatchInventory(t *testing.T) {
	all := AllScreens()

	t.Run("total-76", func(t *testing.T) {
		if len(all) != totalEsperado {
			t.Fatalf("expected=%d observed=%d ids in the canonical list", totalEsperado, len(all))
		}
	})

	t.Run("no-duplicates", func(t *testing.T) {
		vistos := make(map[string]int, len(all))
		for i, id := range all {
			if j, dup := vistos[id]; dup {
				t.Errorf("duplicate id %q on lines %d and %d", id, j+1, i+1)
			}
			vistos[id] = i
		}
	})

	t.Run("known-group", func(t *testing.T) {
		for _, id := range all {
			if id == "unknown" {
				continue
			}
			grupo := id
			if i := strings.Index(id, "."); i >= 0 {
				grupo = id[:i]
			}
			if _, ok := gruposConhecidos[grupo]; !ok {
				t.Errorf("id %q has group %q outside the 8 groups of the INVENTARIO-LAB", id, grupo)
			}
		}
	})

	// A tab-level id has exactly 2 segments; the SOLO screens are the only
	// legitimate exceptions. Any other 1-segment id would be a group emitted
	// without a tab — useless data for triage.
	t.Run("one-segment-only-standalone-screens", func(t *testing.T) {
		for _, id := range all {
			if id == "unknown" || strings.Contains(id, ".") {
				continue
			}
			if _, ok := telasSolo[id]; !ok {
				t.Errorf("the 1-segment id %q is not a standalone screen — a group with no tab is not a screen", id)
			}
		}
	})

	t.Run("unknown-present", func(t *testing.T) {
		if !IsKnownScreen("unknown") {
			t.Error("the `unknown` bucket has to be on the list: it is where an id outside the allowlist lands")
		}
	})

	// Anchors: the sub-actions that motivated the inventory ("screen inside a dead screen").
	t.Run("sub-action-anchors", func(t *testing.T) {
		for _, id := range []string{
			"operacoes.tarefas.jira.detalhe.worklog",
			"operacoes.git.reflog",
			"docker.containers.logs",
			"dev.ai.routing",
			"sistema.ventoinhas", // the exception documented above — only exists in this fork
		} {
			if !IsKnownScreen(id) {
				t.Errorf("anchor id %q missing from the canonical list", id)
			}
		}
	})

	// The 5 ids from the later re-edition, done in BOTH forks in the same act:
	// before it, navigating to these screens fell into the `unknown` bucket, and
	// what was missing was the canonical list, not the instrumentation.
	// `operacoes.backup` and `operacoes.embutidas` have no tab yet and report 0
	// until they do — same contract as `sistema.ventoinhas`.
	t.Run("list-re-edition", func(t *testing.T) {
		for _, id := range []string{
			"config",
			"operacoes.proxmox",
			"operacoes.nos",
			"operacoes.backup",
			"operacoes.embutidas",
		} {
			if !IsKnownScreen(id) {
				t.Errorf("id %q missing from the canonical list — was the re-edition undone?", id)
			}
		}
	})

	// Counts that pin the shape of the list down against careless editing.
	t.Run("counts-per-family", func(t *testing.T) {
		conta := func(pref string) int {
			n := 0
			for _, id := range all {
				if strings.HasPrefix(id, pref) {
					n++
				}
			}
			return n
		}
		for _, c := range []struct {
			pref string
			want int
		}{
			{"operacoes.tarefas.jira.detalhe.", 7},
			{"docker.containers.", 8},
			{"dev.ai.", 5},
			{"operacoes.git.", 7},
		} {
			if got := conta(c.pref); got != c.want {
				t.Errorf("prefix %q: expected=%d observed=%d", c.pref, c.want, got)
			}
		}
	})
}

// TestScreenIDFormat is what stops a free-form id from becoming a "valid" id: no
// uppercase, no space, no accent, no slash, no `..`.
func TestScreenIDFormat(t *testing.T) {
	re := regexp.MustCompile(`^[a-z][a-z0-9-]*(\.[a-z0-9-]+)*$`)
	for _, id := range AllScreens() {
		if !re.MatchString(id) {
			t.Errorf("id outside the format: %q", id)
		}
	}
}

// TestUnknownScreenIsNotAllowlisted is the log-poisoning mitigation seen from
// the allowlist side: nothing outside the file may pass.
func TestUnknownScreenIsNotAllowlisted(t *testing.T) {
	for _, id := range []string{
		"../../etc/passwd",
		"",
		"docker.containers.logs\n",
		"DOCKER.CONTAINERS",
		" dashboard",
		"dashboard ",
		"dev.codigo\r",
		`{"screen":"x"}`,
		"nao-existe-essa-tela",
	} {
		if IsKnownScreen(id) {
			t.Errorf("IsKnownScreen(%q) returned true — it should be false", id)
		}
	}
}

// TestAllScreensIsACopy: the caller must not be able to shuffle the internal list.
func TestAllScreensIsACopy(t *testing.T) {
	a := AllScreens()
	orig := a[0]
	a[0] = "ENVENENADO"
	if b := AllScreens(); b[0] != orig {
		t.Fatalf("AllScreens returned the internal slice: expected=%q observed=%q", orig, b[0])
	}
}
