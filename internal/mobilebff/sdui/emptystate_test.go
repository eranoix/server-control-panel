package sdui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

var emptyStateScreenPointers = map[string][]string{
	"docker.containers": {"docker.compose"},
	"docker.networks":   {"system.systemd"},
	"security.savings":  {"security.devices"},
}

const emptyStateFormPromise = "form below"

const minEmptyStateRunes = 120

const minRegisteredTables = 20

func screenTables(t *testing.T, screen string, v Viewer) ([]TableComponent, bool) {
	t.Helper()
	env, err := Build(context.Background(), screen, v)
	if err != nil {
		if errors.Is(err, ErrScreenNotFound) {
			return nil, false
		}
		t.Fatalf("building %q: %v", screen, err)
	}
	var tables []TableComponent
	for _, c := range env.Screen.Components {
		if tbl, ok := c.(TableComponent); ok {
			tables = append(tables, tbl)
		}
	}
	return tables, true
}

func screenForms(t *testing.T, screen string, v Viewer) []FormComponent {
	t.Helper()
	env, err := Build(context.Background(), screen, v)
	if err != nil {
		if errors.Is(err, ErrScreenNotFound) {
			return nil
		}
		t.Fatalf("building %q: %v", screen, err)
	}
	var forms []FormComponent
	for _, c := range env.Screen.Components {
		if f, ok := c.(FormComponent); ok {
			forms = append(forms, f)
		}
	}
	return forms
}

func TestEmptyState_EveryRegisteredTableHasOne(t *testing.T) {
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

func TestEmptyState_TextIsNeverRecycled(t *testing.T) {
	seen := map[string]string{}

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
					continue
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
