// Package sdui_test — the test that makes the static catalog acceptable.
//
// catalog.go declares, next to each builder, whether a screen appears in the
// app's section picker. That is a SECOND declaration about permission: the
// first, and the only one that actually authorizes, is the gate inside the
// builder itself (ErrScreenNotFound). Two declarations about the same subject
// diverge — not "if", "when": all it takes is somebody touching an
// `if !v.IsAdmin()` in a builder without remembering the catalog's predicate.
//
// This file closes that divergence by construction, walking
// RegisteredScreens() × {admin, non-admin} and demanding the biconditional:
//
//	present in the catalog  ⟺  Build != ErrScreenNotFound
//
// It runs in the SAME test binary as the goldens (docker_golden_test.go and
// company register the 25 production screens through the same Register*(deps)
// that internal/api/api.go calls), so "every registered screen" here is
// literally the production surface, not a sample.
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

// catalogMismatch is a divergence found between catalog and builder, naming
// screen and role so that the failure message points at the exact line to fix
// instead of just saying "the catalog is wrong".
type catalogMismatch struct {
	Screen string
	Role   string
	Detail string
}

// checkCatalogAgainstBuilders is the core of the gate, written as a PURE
// function over injected dependencies (the id list, the viewers, the catalog
// and Build) instead of reading the global registries directly.
//
// That is not gratuitous abstraction: it is what lets
// TestCatalogCheckerCatchesLie exercise the checker itself against a catalog
// that LIES on purpose, without registering a synthetic screen in the real
// global registry — which would pollute RegisteredScreens() and break
// TestGoldenScreens, which compares every registered screen against a
// committed golden.
//
// It returns an error (rather than a mismatch) if Build fails for a reason
// other than ErrScreenNotFound: that is a broken builder, not a permission
// divergence, and conflating the two would hide a real bug behind a message
// about the catalog.
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
	// Stable order: a CI failure list has to be the same between runs,
	// otherwise a log diff turns into noise.
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
						Detail: "o construtor MONTA a tela para este papel, mas o catálogo a omite — a tela fica inalcançável pelo seletor",
					})
				}
			case errors.Is(err, sdui.ErrScreenNotFound):
				if visible[id] {
					mismatches = append(mismatches, catalogMismatch{
						Screen: id, Role: role,
						Detail: "o catálogo OFERECE a tela para este papel, mas o construtor devolve ErrScreenNotFound — o seletor mostraria um item que abre em 404",
					})
				}
			default:
				return nil, fmt.Errorf("Build(%q) para o papel %q falhou por um motivo que não é ErrScreenNotFound: %w", id, role, err)
			}
		}
	}
	return mismatches, nil
}

// syntheticScreenPrefix is the prefix this package's INTERNAL tests
// (registry_test.go, actionregistry_test.go) were already using before this
// file existed, to enroll fake screens in the global registry — "test.registry.".
//
// Why filtering is necessary, and why it is not a loophole: the registry is
// PROCESS-global, and one test binary runs the internal package's tests
// (sdui) BEFORE the external package's (sdui_test, this file). When the tests
// here run as part of a whole-package run, RegisteredScreens() already
// contains the synthetic screens registry_test.go enrolled — screens that do
// not exist in production, have no catalog entry and never should have one.
// Without this filter, the gate would report three permanent failures and
// somebody would end up switching the gate off to silence the noise, which is
// the worst possible outcome.
//
// The loophole this does NOT open: no production screen can hide here.
// TestCatalogNoProductionScreenUsesSyntheticPrefix pins that the real
// Register*(deps) never emit an id with this prefix, so filtering on it never
// removes a real screen from the check.
const syntheticScreenPrefix = "test.registry."

// productionScreens is RegisteredScreens() minus the synthetic screens
// described in syntheticScreenPrefix — the surface the app actually sees.
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

// TestCatalogNoProductionScreenUsesSyntheticPrefix is what keeps the filter
// above honest: if a production screen were ever registered with the
// synthetic prefix, it would silently drop out of every other test in this
// file. Here that becomes an explicit failure.
//
// The check runs against the catalog (which only production screens feed),
// because it is the only list in this process the internal tests do not pollute.
func TestCatalogNoProductionScreenUsesSyntheticPrefix(t *testing.T) {
	for _, id := range sdui.CatalogIDs() {
		if strings.HasPrefix(id, syntheticScreenPrefix) {
			t.Errorf("production screen %q uses the synthetic prefix %q — it would be excluded from the visibility gate without anyone noticing", id, syntheticScreenPrefix)
		}
	}
}

// catalogTestViewers builds the two roles through the SAME construction path
// production uses (config.Config + ViewerFrom), never through a literal
// Viewer{} — the same discipline as testGoldenViewers in golden_test.go, so
// that the test exercises the real admin resolution instead of an assumption about it.
func catalogTestViewers() map[string]sdui.Viewer {
	cfg := &config.Config{
		SchemaVersion: config.CurrentSchemaVersion,
		Primary:       "catalogo-admin",
		Users: []config.User{
			{Username: "catalogo-admin", PasswordHash: "h"},
			{Username: "catalogo-user", PasswordHash: "h"},
		},
	}
	return map[string]sdui.Viewer{
		"admin":    sdui.ViewerFrom(cfg, "catalogo-admin"),
		"nonadmin": sdui.ViewerFrom(cfg, "catalogo-user"),
	}
}

// TestCatalogMatchesBuilderVisibility is THE test: for every registered
// screen and for both roles, the catalog has to agree with the builder.
//
// A failure here is NOT fixed by touching the test. It means the app's picker
// is offering a screen that opens as a 404, or hiding a screen the person can
// open — fix the entry's `visible` predicate in internal/mobilebff/screens,
// or the builder's gate, whichever of the two is wrong.
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

// TestCatalogCoversEveryRegisteredScreen catches the case the biconditional
// above does NOT catch on its own: a screen registered in Register() that
// nobody cataloged and that is admin-only. For the non-admin it vanishes from
// both sides (Build gives a 404, the catalog does not have it) and for the
// admin the biconditional would flag it — but the error reads far better put
// this way, and this test stays correct even if one day there is a role that
// sees no screen at all.
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

// TestCatalogCatalogedScreensAllExist is the inverse: a catalog entry for an
// id nobody registered becomes a menu item that opens as a 404 for EVERYONE
// (usually a mistyped id, or a removed screen whose entry was left behind).
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

// TestCatalogCheckerCatchesLie proves the checker FAILS when the catalog lies
// — in both directions. Without this proof, a checker that always returned
// zero mismatches would go unnoticed and the whole gate would be decorative.
//
// It uses synthetic registries (not the global ones) for exactly the reason
// described in checkCatalogAgainstBuilders: really registering fake screens
// would break the goldens.
func TestCatalogCheckerCatchesLie(t *testing.T) {
	viewers := catalogTestViewers()

	// "mentira-oferecida": the catalog offers it to everyone; the builder
	// only delivers it to admins. The non-admin would see an item that 404s.
	// "mentira-omitida": the builder delivers to everyone; the catalog
	// hides it from everyone. The screen becomes unreachable from the picker.
	const oferecida, omitida = "mentira.oferecida", "mentira.omitida"

	catalogFor := func(v sdui.Viewer) []sdui.CatalogEntry {
		return []sdui.CatalogEntry{{ID: oferecida, Group: sdui.GroupSistema, Label: "Oferecida"}}
	}
	build := func(_ context.Context, id string, v sdui.Viewer) (*sdui.Envelope, error) {
		if id == oferecida && !v.IsAdmin() {
			return nil, sdui.ErrScreenNotFound
		}
		return &sdui.Envelope{}, nil
	}

	mismatches, err := checkCatalogAgainstBuilders(
		context.Background(), []string{oferecida, omitida}, viewers, catalogFor, build,
	)
	if err != nil {
		t.Fatalf("checkCatalogAgainstBuilders: %v", err)
	}

	got := map[string]bool{}
	for _, m := range mismatches {
		got[m.Screen+"/"+m.Role] = true
	}
	for _, want := range []string{
		oferecida + "/nonadmin", // catalog offers, builder 404
		omitida + "/admin",      // builder assembles, catalog omits
		omitida + "/nonadmin",
	} {
		if !got[want] {
			t.Errorf("the verifier did NOT catch the lie %q — mismatches: %+v", want, mismatches)
		}
	}
	// The only legitimate combination is oferecida/admin: the catalog offers and
	// the builder delivers. If it shows up as a mismatch, the checker is
	// reporting a false positive.
	if got[oferecida+"/admin"] {
		t.Errorf("the verifier flagged a false positive on %q/admin (catalog and builder agree)", oferecida)
	}
}

// TestCatalogFilteringIsByOmission pins the anti-enumeration posture at the
// catalog level: what a non-admin may not see does not arrive marked
// unavailable, it arrives ABSENT. A disabled item in the list would confirm
// the screen exists and would hand back the enumeration of the administrative
// surface that handlers_screens.go's 404 denies.
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

// TestCatalogOrderIsDeterministicAndGrouped pins that the returned order is
// stable and grouped — the app draws the list in the order it arrives, and an
// unstable order would make items jump around under the finger between one
// opening of the picker and the next.
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

	// Every item of a group arrives contiguous: a group that reappeared after
	// another would make the app draw two headers with the same name.
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
