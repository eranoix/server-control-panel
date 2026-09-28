package telemetry

import (
	"regexp"
	"strings"
	"testing"
)

var knownGroups = map[string]struct{}{
	"dashboard": {}, "system": {}, "games": {}, "docker": {},
	"dev": {}, "security": {}, "apps": {}, "operations": {},
	"config": {},
}

var soloScreens = map[string]struct{}{"dashboard": {}, "config": {}}

const wantTotal = 76

func TestScreenIDsMatchInventory(t *testing.T) {
	all := AllScreens()

	t.Run("total-76", func(t *testing.T) {
		if len(all) != wantTotal {
			t.Fatalf("expected=%d observed=%d ids in the canonical list", wantTotal, len(all))
		}
	})

	t.Run("no-duplicates", func(t *testing.T) {
		seen := make(map[string]int, len(all))
		for i, id := range all {
			if j, dup := seen[id]; dup {
				t.Errorf("duplicate id %q on lines %d and %d", id, j+1, i+1)
			}
			seen[id] = i
		}
	})

	t.Run("known-group", func(t *testing.T) {
		for _, id := range all {
			if id == "unknown" {
				continue
			}
			group := id
			if i := strings.Index(id, "."); i >= 0 {
				group = id[:i]
			}
			if _, ok := knownGroups[group]; !ok {
				t.Errorf("id %q has group %q outside the 8 navigation groups", id, group)
			}
		}
	})

	t.Run("one-segment-only-standalone-screens", func(t *testing.T) {
		for _, id := range all {
			if id == "unknown" || strings.Contains(id, ".") {
				continue
			}
			if _, ok := soloScreens[id]; !ok {
				t.Errorf("the 1-segment id %q is not a standalone screen — a group with no tab is not a screen", id)
			}
		}
	})

	t.Run("unknown-present", func(t *testing.T) {
		if !IsKnownScreen("unknown") {
			t.Error("the `unknown` bucket has to be on the list: it is where an id outside the allowlist lands")
		}
	})

	t.Run("sub-action-anchors", func(t *testing.T) {
		for _, id := range []string{
			"operations.tasks.jira.detail.worklog",
			"operations.git.reflog",
			"docker.containers.logs",
			"dev.ai.routing",
			"system.fans",
		} {
			if !IsKnownScreen(id) {
				t.Errorf("anchor id %q missing from the canonical list", id)
			}
		}
	})

	t.Run("list-re-edition", func(t *testing.T) {
		for _, id := range []string{
			"config",
			"operations.proxmox",
			"operations.nodes",
			"operations.backup",
			"operations.embedded",
		} {
			if !IsKnownScreen(id) {
				t.Errorf("id %q missing from the canonical list — was the re-edition undone?", id)
			}
		}
	})

	t.Run("counts-per-family", func(t *testing.T) {
		account := func(pref string) int {
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
			{"operations.tasks.jira.detail.", 7},
			{"docker.containers.", 8},
			{"dev.ai.", 5},
			{"operations.git.", 7},
		} {
			if got := account(c.pref); got != c.want {
				t.Errorf("prefix %q: expected=%d observed=%d", c.pref, c.want, got)
			}
		}
	})
}

func TestScreenIDFormat(t *testing.T) {
	re := regexp.MustCompile(`^[a-z][a-z0-9-]*(\.[a-z0-9-]+)*$`)
	for _, id := range AllScreens() {
		if !re.MatchString(id) {
			t.Errorf("id outside the format: %q", id)
		}
	}
}

func TestUnknownScreenIsNotAllowlisted(t *testing.T) {
	for _, id := range []string{
		"../../etc/passwd",
		"",
		"docker.containers.logs\n",
		"DOCKER.CONTAINERS",
		" dashboard",
		"dashboard ",
		"dev.code\r",
		`{"screen":"x"}`,
		"no-such-screen",
	} {
		if IsKnownScreen(id) {
			t.Errorf("IsKnownScreen(%q) returned true — it should be false", id)
		}
	}
}

func TestAllScreensIsACopy(t *testing.T) {
	a := AllScreens()
	orig := a[0]
	a[0] = "POISONED"
	if b := AllScreens(); b[0] != orig {
		t.Fatalf("AllScreens returned the internal slice: expected=%q observed=%q", orig, b[0])
	}
}
