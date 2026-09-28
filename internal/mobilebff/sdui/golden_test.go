package sdui

import (
	"bytes"
	"context"
	"flag"
	"strings"
	"testing"

	"server-control-panel/internal/config"
)

var updateGoldenScreens = flag.Bool("update", false, "regenerates the SDUI screen goldens in contracts/sdui/fixtures/screens/")

const screensGoldenDir = fixturesDir + "/screens"

var screensWithNoRoleDifference = map[string]string{
	"docker.volumes":  "list-only for any viewer: internal/docker exposes no single-item delete for volumes, and the web panel (handleVolumes) is read-only too — there is no admin-only action or data to omit.",
	"docker.networks": "list-only for any viewer: internal/docker exposes no single-item delete for networks, and the web panel (handleNetworks) is read-only too — there is no admin-only action or data to omit.",
	"system.history":  "list-only for any viewer: handleHistory has no admin gate in the web panel, and the Builder (buildSystemHistoryScreen) neither receives nor consults the Viewer — there is no admin-only action or data to omit.",
	"system.ports":    "list-only for any viewer: handleListening has no admin gate in the web panel, and the Builder (buildSystemPortsScreen) neither receives nor consults the Viewer — there is no admin-only action or data to omit, and no removal of a listening socket either.",
	"system.metrics":  "list-only for any viewer: history/metrics has no admin gate in the web panel. The screen's only per-viewer content is the Value of the window field (systemMetricsWindowFor(v.Username)), which is per-USER state, not per-ROLE — the two golden viewers (golden-admin/golden-user) never called system.metrics.window, so both read the same defaultSystemMetricsWindow and the golden comes out byte-identical; this is not an RBAC omission, it is the same default value read twice.",
	"jira.issues":     "the gate is by per-user CREDENTIAL (MiscDeps.JiraStatus(v.Username)), never by IsAdmin() — buildJiraIssuesScreen does not even consult v.IsAdmin(). The golden harness wires both synthetic viewers (golden-admin/golden-user) to the same fake Jira state, so both see the SAME connected screen; this is not an RBAC omission, it is a screen whose visibility axis is a different one.",
	"queue.jobs":      "list-only for any authenticated viewer: buildQueueJobsScreen receives the Viewer but does not even consult it — the screen structure (table + retry/cancel actions + confirm_destructive) is identical for everyone. The real per-role scoping happens in the rows ENDPOINT (queue.jobs.rows in RegisterMisc: owner=\"\" for admin, owner=v.Username for non-admin), outside the Envelope this golden compares, and in the ownership check inside the action handlers (misc_actions.go) — it is not an RBAC omission in the Builder, it is RBAC living in two layers that this golden does not reach.",
}

var forbiddenForNonAdmin = map[string]func() []string{}

func lookupForbiddenForNonAdmin(screenID string) (func() []string, bool) {
	if fn, ok := forbiddenForNonAdmin[screenID]; ok {
		return fn, true
	}
	return forbiddenForNonAdminExtEntry(screenID)
}

func testGoldenViewers() map[string]Viewer {
	cfg := &config.Config{
		SchemaVersion: config.CurrentSchemaVersion,
		Primary:       "golden-admin",
		Users: []config.User{
			{Username: "golden-admin", PasswordHash: "h"},
			{Username: "golden-user", PasswordHash: "h"},
		},
	}
	return map[string]Viewer{
		"admin":    ViewerFrom(cfg, "golden-admin"),
		"nonadmin": ViewerFrom(cfg, "golden-user"),
	}
}

func TestGoldenScreens(t *testing.T) {
	screens := RegisteredScreens()
	viewers := testGoldenViewers()

	if *updateGoldenScreens {
		if err := WriteGoldens(screensGoldenDir, screens, viewers); err != nil {
			t.Fatalf("WriteGoldens: %v", err)
		}
		t.Logf("goldens regenerated in %s for %d screen(s)", screensGoldenDir, len(screens))
		return
	}

	diffs, err := CompareGoldens(screensGoldenDir, screens, viewers)
	if err != nil {
		t.Fatalf("CompareGoldens: %v", err)
	}
	for _, d := range diffs {
		t.Errorf("screen %q, role %q: %s\nrun `make sdui-golden` and review the diff", d.Screen, d.Role, d.Detail)
	}
}

func TestGoldenScreens_EmptyRegistryPassesTrivially(t *testing.T) {
	diffs, err := CompareGoldens(t.TempDir(), []string{}, testGoldenViewers())
	if err != nil {
		t.Fatalf("CompareGoldens with zero screens should not fail: %v", err)
	}
	if len(diffs) != 0 {
		t.Fatalf("CompareGoldens with zero screens should return 0 diffs, got %d", len(diffs))
	}
}

func TestGoldenScreens_RoleOmissionIsReasoned(t *testing.T) {
	ctx := context.Background()
	viewers := testGoldenViewers()

	for _, screen := range RegisteredScreens() {
		adminOut, err := buildGoldenBytes(ctx, screen, viewers["admin"])
		if err != nil {
			t.Fatalf("building %q (admin): %v", screen, err)
		}
		nonAdminOut, err := buildGoldenBytes(ctx, screen, viewers["nonadmin"])
		if err != nil {
			t.Fatalf("building %q (nonadmin): %v", screen, err)
		}

		identical := bytes.Equal(adminOut, nonAdminOut)
		reason, allowlisted := screensWithNoRoleDifference[screen]

		if identical && !allowlisted {
			t.Errorf("screen %q: the admin golden and the non-admin golden are byte-identical and the screen is not in screensWithNoRoleDifference — if this is intentional, add an entry with the reason; if not, either the screen leaks admin-only content to everyone, or the Builder is not filtering anything by mistake", screen)
		}
		if identical && allowlisted && reason == "" {
			t.Errorf("screen %q: entry in screensWithNoRoleDifference with no reason written", screen)
		}

		if !identical {
			if _, ok := lookupForbiddenForNonAdmin(screen); !ok {
				t.Errorf("screen %q: the admin golden and the non-admin golden DIFFER but there is no entry in forbiddenForNonAdmin (not even via RegisterForbiddenForNonAdmin) — register the set of what the non-admin golden must never contain", screen)
			}
		}
	}
}

func TestGoldenScreens_NonAdminNeverContainsForbiddenStrings(t *testing.T) {
	ctx := context.Background()
	viewers := testGoldenViewers()

	for _, screen := range RegisteredScreens() {
		forbiddenFn, ok := lookupForbiddenForNonAdmin(screen)
		if !ok {
			continue
		}

		nonAdminOut, err := buildGoldenBytes(ctx, screen, viewers["nonadmin"])
		if err != nil {
			t.Fatalf("building %q (nonadmin): %v", screen, err)
		}

		for _, forbidden := range forbiddenFn() {
			if strings.Contains(string(nonAdminOut), forbidden) {
				t.Errorf("screen %q: the non-admin golden contains %q, which forbiddenForNonAdmin declares forbidden for this role", screen, forbidden)
			}
		}
	}
}
