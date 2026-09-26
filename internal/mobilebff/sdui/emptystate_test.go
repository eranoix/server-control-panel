package sdui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

// emptystate_test.go — a table's empty state is PRODUCT COPY, not a technical
// observation. An operator who opens "Regras de alerta" for the first time and
// reads "Nenhuma regra encontrada" learns exactly what the blank screen was
// already showing. The tests in this file exist so that regression does not
// come back in silence: they run over EVERY registered screen
// (RegisteredScreens(), populated by the *_golden_test.go files of the
// external sdui_test package in the same test binary), for both synthetic
// roles, and cover the four requirements that separate "reporting emptiness"
// from "teaching":
//
//  1. no registered table is left without an empty state;
//  2. no text is recycled between screens (text that would serve any screen
//     is a sign that it teaches nothing about THIS one);
//  3. every text has substance — more than one sentence, not an observation;
//  4. every action the text OFFERS really exists on the screen, for the role
//     that is reading it (a promise the Builder's own RBAC has already
//     removed is worse than no promise at all).
//
// Check 4 is why these tests build the screen per role instead of inspecting
// the descriptors' source: "há formulário abaixo" is true for an admin and
// may be a lie for a non-admin, and only the Envelope actually assembled
// knows which of the two is the case.

// emptyStateScreenPointers records, per screen, the OTHER screens the empty
// state text tells the user to open. Each entry is verified on three fronts:
// the target screen exists, it is visible to every role that sees the source
// screen (RBAC by omission — never send somebody somewhere they cannot see),
// and the target's catalog LABEL appears literally in the text (so that
// renaming the target screen breaks the test instead of leaving the text
// pointing at a name that no longer exists).
var emptyStateScreenPointers = map[string][]string{
	"docker.containers": {"docker.compose"},
	"docker.networks":   {"system.systemd"},
	"security.economia": {"security.devices"},
}

// emptyStateFormPromise is the textual trigger of the "the form I promised
// really exists" check: any text containing this phrase obliges the screen
// assembled for THAT role to contain a usable FormComponent.
const emptyStateFormPromise = "formulário abaixo"

// minEmptyStateRunes is the size floor that separates an observation
// ("Nenhum container encontrado.", 28 runes) from a text that answers the
// three questions of an empty state. It is not a verbosity target: the real
// ceiling is editorial (two or three lines), and this floor exists only so
// that nobody brings the bare observation back without CI noticing.
const minEmptyStateRunes = 120

// minRegisteredTables is the floor for "the registry really was populated".
// Today that is 20 tables across 19 registered screens (jira.issues has two
// screen shapes — connected and not connected — and only the connected one
// has a table). The floor is deliberately today's exact number: growing is
// free, shrinking is a screen that lost its table without anyone noticing.
const minRegisteredTables = 20

// screenTables builds screen for v and returns the Envelope's tables, or
// (nil, false) when the Builder refuses the whole screen for this Viewer —
// a refusal is a legitimate result (RBAC by omission), never a test failure.
func screenTables(t *testing.T, screen string, v Viewer) ([]TableComponent, bool) {
	t.Helper()
	env, err := Build(context.Background(), screen, v)
	if err != nil {
		if errors.Is(err, ErrScreenNotFound) {
			return nil, false
		}
		t.Fatalf("buildando %q: %v", screen, err)
	}
	var tables []TableComponent
	for _, c := range env.Screen.Components {
		if tbl, ok := c.(TableComponent); ok {
			tables = append(tables, tbl)
		}
	}
	return tables, true
}

// screenForms returns the FormComponents of screen's Envelope for v.
func screenForms(t *testing.T, screen string, v Viewer) []FormComponent {
	t.Helper()
	env, err := Build(context.Background(), screen, v)
	if err != nil {
		if errors.Is(err, ErrScreenNotFound) {
			return nil
		}
		t.Fatalf("buildando %q: %v", screen, err)
	}
	var forms []FormComponent
	for _, c := range env.Screen.Components {
		if f, ok := c.(FormComponent); ok {
			forms = append(forms, f)
		}
	}
	return forms
}

// TestEmptyState_EveryRegisteredTableHasOne is requirement 1: a TableComponent
// with no EmptyState falls back to the Kotlin renderer's generic text ("Nada
// para mostrar."), which is exactly the copy that teaches nothing. The server
// is what knows why that particular table is empty, so the server writes it.
func TestEmptyState_EveryRegisteredTableHasOne(t *testing.T) {
	// Without this count, EVERY test in this file would pass trivially the
	// day a refactor stopped registering the screens in this test binary —
	// and a silent green over zero tables is worse than a red one.
	distinct := map[string]bool{}

	for _, screen := range RegisteredScreens() {
		for role, v := range testGoldenViewers() {
			tables, visible := screenTables(t, screen, v)
			if !visible {
				continue
			}
			for _, tbl := range tables {
				distinct[screen+"/"+tbl.ID] = true
				if tbl.EmptyState == nil {
					t.Errorf("screen %q (%s), table %q: no EmptyState — the client would fall through to the generic fallback; write the text in the descriptor", screen, role, tbl.ID)
					continue
				}
				if strings.TrimSpace(tbl.EmptyState.Text) == "" {
					t.Errorf("screen %q (%s), table %q: EmptyState with empty text", screen, role, tbl.ID)
				}
			}
		}
	}

	if len(distinct) < minRegisteredTables {
		t.Fatalf("only %d distinct table(s) visited (minimum %d) — the screen registry was not populated in this test binary and everything else in this file would be passing over emptiness", len(distinct), minRegisteredTables)
	}
}

// TestEmptyState_TextIsNeverRecycled is requirement 2. A text that appears on
// two screens is, by construction, a text that says nothing specific about
// either of them — the operational definition of "recycled generic". The
// comparison key is the screen+table pair: the same table assembled for admin
// and for non-admin is ONE table, not two.
func TestEmptyState_TextIsNeverRecycled(t *testing.T) {
	seen := map[string]string{} // text -> the "screen/table" that used it first

	for _, screen := range RegisteredScreens() {
		for _, v := range testGoldenViewers() {
			tables, visible := screenTables(t, screen, v)
			if !visible {
				continue
			}
			for _, tbl := range tables {
				if tbl.EmptyState == nil {
					continue // already reported by the previous test
				}
				owner := screen + "/" + tbl.ID
				text := strings.TrimSpace(tbl.EmptyState.Text)
				if prev, dup := seen[text]; dup && prev != owner {
					t.Errorf("empty state recycled between %q and %q — identical text:\n  %s\nWrite a text that only makes sense on the screen where it appears.", prev, owner, text)
					continue
				}
				seen[text] = owner
			}
		}
	}
}

// TestEmptyState_TeachesInsteadOfStatingTheObvious is requirement 3: the text
// has to answer more than "it is empty". Two mechanical proofs, both coarse
// on purpose (editorial quality is a human matter; CI only blocks the obvious
// regression): more than one sentence, and a length above that of a bare
// observation.
func TestEmptyState_TeachesInsteadOfStatingTheObvious(t *testing.T) {
	for _, screen := range RegisteredScreens() {
		for _, v := range testGoldenViewers() {
			tables, visible := screenTables(t, screen, v)
			if !visible {
				continue
			}
			for _, tbl := range tables {
				if tbl.EmptyState == nil {
					continue
				}
				text := strings.TrimSpace(tbl.EmptyState.Text)
				if n := utf8.RuneCountInString(text); n < minEmptyStateRunes {
					t.Errorf("screen %q, table %q: empty state with %d runes (minimum %d) — %q has no room to say what the screen is, why it's empty, and what to do", screen, tbl.ID, n, minEmptyStateRunes, text)
				}
				if sentences := strings.Count(text, ". ") + strings.Count(text, "! "); sentences < 1 {
					t.Errorf("screen %q, table %q: one-sentence empty state — %q reports the emptiness without teaching anything", screen, tbl.ID, text)
				}
			}
		}
	}
}

// TestEmptyState_PromisedFormExists is the "action on the screen itself" half
// of requirement 4. A text that tells the user to fill in "o formulário
// abaixo" is only true if THAT role really received a usable form —
// DropFormFields may have emptied the form and DropComponents removed it
// entirely (filter.go), and in that case the text becomes an impossible order.
func TestEmptyState_PromisedFormExists(t *testing.T) {
	for _, screen := range RegisteredScreens() {
		for role, v := range testGoldenViewers() {
			tables, visible := screenTables(t, screen, v)
			if !visible {
				continue
			}
			for _, tbl := range tables {
				if tbl.EmptyState == nil || !strings.Contains(tbl.EmptyState.Text, emptyStateFormPromise) {
					continue
				}
				forms := screenForms(t, screen, v)
				usable := false
				for _, f := range forms {
					if len(f.Fields) > 0 && f.SubmitAction.ActionID != "" {
						usable = true
						break
					}
				}
				if !usable {
					t.Errorf("screen %q (%s), table %q: the empty state tells the user to use %q but this role's Envelope has no FormComponent with fields and a submit action — a promise the screen does not keep", screen, role, tbl.ID, emptyStateFormPromise)
				}
			}
		}
	}
}

// TestEmptyState_PointedScreenIsRealAndReachable is the "send them to another
// screen" half of requirement 4, and the one that closes the RBAC hole: the target
// has to exist, it has to be in the catalog of EVERY role that sees the source, and
// its label has to appear in the text.
func TestEmptyState_PointedScreenIsRealAndReachable(t *testing.T) {
	registered := map[string]bool{}
	for _, s := range RegisteredScreens() {
		registered[s] = true
	}

	for screen, targets := range emptyStateScreenPointers {
		if !registered[screen] {
			t.Errorf("emptyStateScreenPointers cites the source screen %q, which is not registered — stale entry", screen)
			continue
		}
		for _, target := range targets {
			if !registered[target] {
				t.Errorf("screen %q points the empty state to %q, which is not registered", screen, target)
				continue
			}
			for role, v := range testGoldenViewers() {
				tables, visible := screenTables(t, screen, v)
				if !visible {
					continue // whoever cannot see the source never reads the text
				}

				label := ""
				for _, e := range CatalogFor(v) {
					if e.ID == target {
						label = e.Label
						break
					}
				}
				if label == "" {
					t.Errorf("screen %q (%s): the empty state tells the user to open %q, which is NOT in this role's catalog — sending someone to a screen they cannot see leaks its existence and frustrates whoever tries", screen, role, target)
					continue
				}

				for _, tbl := range tables {
					if tbl.EmptyState == nil {
						continue
					}
					if !strings.Contains(tbl.EmptyState.Text, label) {
						t.Errorf("screen %q (%s), table %q: %q is declared as the empty state's destination, but its catalog label (%q) does not appear in the text:\n  %s", screen, role, tbl.ID, target, label, tbl.EmptyState.Text)
					}
				}
			}
		}
	}
}
