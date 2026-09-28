package sdui_test

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"

	"server-control-panel/internal/config"
	"server-control-panel/internal/mobilebff/sdui"
)

type catalogMismatch struct {
	Screen string
	Role   string
	Detail string
}

func checkCatalogAgainstBuilders(
	ctx context.Context,
	ids []string,
	viewers map[string]sdui.Viewer,
	catalogFor func(sdui.Viewer) []sdui.CatalogEntry,
	build func(context.Context, string, sdui.Viewer) (*sdui.Envelope, error),
) ([]catalogMismatch, error) {
	roles := make([]string, 0, len(viewers))
	for role := range viewers {
		roles = append(roles, role)
	}
	sort.Strings(roles)

	var mismatches []catalogMismatch
	for _, role := range roles {
		viewer := viewers[role]

		visible := map[string]bool{}
		for _, entry := range catalogFor(viewer) {
			visible[entry.ID] = true
		}

		for _, id := range ids {
			_, err := build(ctx, id, viewer)
			switch {
			case err == nil:
				if !visible[id] {
					mismatches = append(mismatches, catalogMismatch{
						Screen: id, Role: role,
						Detail: "the builder BUILDS the screen for this role, but the catalog omits it: the screen is unreachable from the picker",
					})
				}
			case errors.Is(err, sdui.ErrScreenNotFound):
				if visible[id] {
					mismatches = append(mismatches, catalogMismatch{
						Screen: id, Role: role,
						Detail: "the catalog OFFERS the screen for this role, but the builder returns ErrScreenNotFound: the picker would show an item that opens as a 404",
					})
				}
			default:
				return nil, fmt.Errorf("Build(%q) for role %q failed for a reason other than ErrScreenNotFound: %w", id, role, err)
			}
		}
	}
	return mismatches, nil
}

const syntheticScreenPrefix = "test.registry."

func productionScreens() []string {
	all := sdui.RegisteredScreens()
	out := make([]string, 0, len(all))
	for _, id := range all {
		if strings.HasPrefix(id, syntheticScreenPrefix) {
			continue
		}
		out = append(out, id)
	}
	return out
}

func TestCatalogNoProductionScreenUsesSyntheticPrefix(t *testing.T) {
	for _, id := range sdui.CatalogIDs() {
		if strings.HasPrefix(id, syntheticScreenPrefix) {
			t.Errorf("production screen %q uses the synthetic prefix %q — it would be excluded from the visibility gate without anyone noticing", id, syntheticScreenPrefix)
		}
	}
}

func catalogTestViewers() map[string]sdui.Viewer {
	cfg := &config.Config{
		SchemaVersion: config.CurrentSchemaVersion,
		Primary:       "catalog-admin",
		Users: []config.User{
			{Username: "catalog-admin", PasswordHash: "h"},
			{Username: "catalog-user", PasswordHash: "h"},
		},
	}
	return map[string]sdui.Viewer{
		"admin":    sdui.ViewerFrom(cfg, "catalog-admin"),
		"nonadmin": sdui.ViewerFrom(cfg, "catalog-user"),
	}
}

func TestCatalogMatchesBuilderVisibility(t *testing.T) {
	ids := productionScreens()
	if len(ids) == 0 {
		t.Fatal("productionScreens() returned zero screens — the test binary lost the golden_test registrations; without them this gate verifies nothing")
	}

	mismatches, err := checkCatalogAgainstBuilders(
		context.Background(), ids, catalogTestViewers(), sdui.CatalogFor, sdui.Build,
	)
	if err != nil {
		t.Fatalf("checkCatalogAgainstBuilders: %v", err)
	}
	for _, m := range mismatches {
		t.Errorf("screen %q, role %q: %s", m.Screen, m.Role, m.Detail)
	}
}

func TestCatalogCoversEveryRegisteredScreen(t *testing.T) {
	cataloged := map[string]bool{}
	for _, id := range sdui.CatalogIDs() {
		cataloged[id] = true
	}
	for _, id := range productionScreens() {
		if !cataloged[id] {
			t.Errorf("screen %q is registered but has no catalog entry — it would be silently unreachable from the app's selector, which is exactly the defect the catalog exists to fix", id)
		}
	}
}

func TestCatalogCatalogedScreensAllExist(t *testing.T) {
	registered := map[string]bool{}
	for _, id := range productionScreens() {
		registered[id] = true
	}
	for _, id := range sdui.CatalogIDs() {
		if !registered[id] {
			t.Errorf("catalog has an entry for %q, but no screen with that id is registered — the selector would show an item that opens to a 404 for any user", id)
		}
	}
}

func TestCatalogCheckerCatchesLie(t *testing.T) {
	viewers := catalogTestViewers()

	const offered, omitted = "lie.offered", "lie.omitted"

	catalogFor := func(v sdui.Viewer) []sdui.CatalogEntry {
		return []sdui.CatalogEntry{{ID: offered, Group: sdui.GroupSystem, Label: "Offered"}}
	}
	build := func(_ context.Context, id string, v sdui.Viewer) (*sdui.Envelope, error) {
		if id == offered && !v.IsAdmin() {
			return nil, sdui.ErrScreenNotFound
		}
		return &sdui.Envelope{}, nil
	}

	mismatches, err := checkCatalogAgainstBuilders(
		context.Background(), []string{offered, omitted}, viewers, catalogFor, build,
	)
	if err != nil {
		t.Fatalf("checkCatalogAgainstBuilders: %v", err)
	}

	got := map[string]bool{}
	for _, m := range mismatches {
		got[m.Screen+"/"+m.Role] = true
	}
	for _, want := range []string{
		offered + "/nonadmin",
		omitted + "/admin",
		omitted + "/nonadmin",
	} {
		if !got[want] {
			t.Errorf("the verifier did NOT catch the lie %q — mismatches: %+v", want, mismatches)
		}
	}
	if got[offered+"/admin"] {
		t.Errorf("the verifier flagged a false positive on %q/admin (catalog and builder agree)", offered)
	}
}

func TestCatalogFilteringIsByOmission(t *testing.T) {
	viewers := catalogTestViewers()
	adminIDs := map[string]bool{}
	for _, e := range sdui.CatalogFor(viewers["admin"]) {
		adminIDs[e.ID] = true
	}
	nonAdmin := sdui.CatalogFor(viewers["nonadmin"])

	for _, e := range nonAdmin {
		if !adminIDs[e.ID] {
			t.Errorf("non-admin's catalog has %q, which the admin does not see — the non-admin can never see MORE than the admin", e.ID)
		}
	}
	if len(nonAdmin) >= len(adminIDs) {
		t.Errorf("non-admin's catalog has %d entries and the admin's has %d — with no omission at all, this gate is not proving any filtering", len(nonAdmin), len(adminIDs))
	}
}

func TestCatalogOrderIsDeterministicAndGrouped(t *testing.T) {
	v := catalogTestViewers()["admin"]
	first := sdui.CatalogFor(v)
	if len(first) == 0 {
		t.Fatal("admin's catalog came back empty")
	}
	for i := 0; i < 5; i++ {
		again := sdui.CatalogFor(v)
		if len(again) != len(first) {
			t.Fatalf("CatalogFor returned %d entries and then %d", len(first), len(again))
		}
		for j := range first {
			if again[j] != first[j] {
				t.Fatalf("CatalogFor is not deterministic: index %d was %+v and became %+v", j, first[j], again[j])
			}
		}
	}

	seen := map[string]bool{}
	current := ""
	for _, e := range first {
		if e.Group == current {
			continue
		}
		if seen[e.Group] {
			t.Errorf("group %q reappears after %q — items in a group must be contiguous", e.Group, current)
		}
		seen[e.Group] = true
		current = e.Group
	}
}
