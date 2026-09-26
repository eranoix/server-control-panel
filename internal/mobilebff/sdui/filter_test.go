package sdui

import (
	"bytes"
	"encoding/json"
	"testing"
)

// buildFilterTestScreen assembles a screen with an admin-only `action`, a
// `form` with an admin-only field (plus an ordinary field, so the form
// survives filtering in the non-admin case) and a `table` whose row_actions
// includes an admin-only entry — exactly the scenario needed to prove
// omission at all three levels (component, field, row action) at once.
func buildFilterTestScreen(isAdmin bool) Screen {
	action := ActionComponent{
		ComponentBase: ComponentBase{Type: ComponentTypeAction, ID: "act1"},
		Label:         "Admin Only Action",
		ActionID:      "test.admin.only",
	}

	form := FormComponent{
		ComponentBase: ComponentBase{Type: ComponentTypeForm, ID: "form1"},
		Fields: []FormField{
			{Key: "run_as_root", Label: "Run as root"},
			{Key: "name", Label: "Name"},
		},
		SubmitAction: ActionRef{ActionID: "test.form.submit"},
	}
	DropFormFields(&form, func(f FormField) bool {
		return isAdmin || f.Key != "run_as_root"
	})

	table := TableComponent{
		ComponentBase: ComponentBase{Type: ComponentTypeTable, ID: "table1"},
		Columns:       []TableColumn{{Key: "name", Label: "Name", Kind: "text"}},
		RowsSource:    DataSource{Endpoint: "/rows"},
		RowActions: []ActionRef{
			{ActionID: "test.admin.only.row"},
			{ActionID: "view"},
		},
	}
	DropRowActions(&table, func(a ActionRef) bool {
		return isAdmin || a.ActionID != "test.admin.only.row"
	})

	screen := Screen{
		ID:         "test.filter.screen",
		Title:      "Filter Test",
		Components: []Component{action, form, table},
	}
	DropComponents(&screen, func(c Component) bool {
		if !isAdmin && c.Base().ID == "act1" {
			return false // the whole `action` is admin-only
		}
		return true
	})
	return screen
}

func marshalScreen(t *testing.T, s Screen) []byte {
	t.Helper()
	out, err := json.Marshal(Envelope{SDUIVersion: CurrentSDUIVersion, Screen: s})
	if err != nil {
		t.Fatalf("json.Marshal(Envelope): %v", err)
	}
	return out
}

// Test 1 (the security property, over the full bytes): for a non-admin
// Viewer, nothing admin-only survives serialization — not the `action`'s
// action_id, not the form field's key, not the row_action's action_id.
func TestFilter_NonAdmin_OmitsAdminOnlyFromBytes(t *testing.T) {
	screen := buildFilterTestScreen(false)
	out := marshalScreen(t, screen)

	for _, forbidden := range []string{"test.admin.only", "run_as_root"} {
		if bytes.Contains(out, []byte(forbidden)) {
			t.Errorf("non-admin output contains %q, should be absent from the bytes: %s", forbidden, out)
		}
	}
	// The admin-only row_action uses a different action_id from the `action`
	// component ("test.admin.only.row"), checked separately so it does not
	// collide with the shared prefix in the assertion above.
	if bytes.Contains(out, []byte("test.admin.only.row")) {
		t.Errorf("non-admin output contains the admin-only row_action, should be absent from the bytes: %s", out)
	}
}

// Test 2 (the non-vacuous counterpart): for an admin Viewer, all three
// admin-only strings are present. Without this test, Test 1 would pass even
// with a builder that emits nothing.
func TestFilter_Admin_IncludesAdminOnlyInBytes(t *testing.T) {
	screen := buildFilterTestScreen(true)
	out := marshalScreen(t, screen)

	for _, want := range []string{"test.admin.only", "run_as_root", "test.admin.only.row"} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("admin output does not contain %q, should be present: %s", want, out)
		}
	}
}

// Test 3: permission_hint is never the gate. A component carrying
// permission_hint:"admin.write" stays fully present for a non-admin Viewer if
// the builder did not explicitly remove it via DropComponents — real
// filtering is always explicit omission, never an implicit reading of
// permission_hint by the server or by the client.
func TestFilter_PermissionHint_IsNeverTheGate(t *testing.T) {
	screen := Screen{
		ID:    "test.filter.hint",
		Title: "Hint Test",
		Components: []Component{
			ActionComponent{
				ComponentBase: ComponentBase{Type: ComponentTypeAction, ID: "hinted", PermissionHint: "admin.write"},
				Label:         "Hinted Action",
				ActionID:      "test.hinted.action",
			},
		},
	}
	// A "naive" builder that never looks at permission_hint and keeps everything:
	DropComponents(&screen, func(Component) bool { return true })

	out := marshalScreen(t, screen)
	if !bytes.Contains(out, []byte("test.hinted.action")) {
		t.Errorf("a component with permission_hint should remain present (the hint is not the gate): %s", out)
	}
	if !bytes.Contains(out, []byte(`"permission_hint":"admin.write"`)) {
		t.Errorf("permission_hint should have been serialized as-is, with no gating logic: %s", out)
	}
}

// Test 4: filtering a form down to zero fields drops the whole component,
// instead of emitting an empty form (an empty form is a broken screen, not a
// filtered one).
func TestFilter_EmptyForm_IsDropped(t *testing.T) {
	form := FormComponent{
		ComponentBase: ComponentBase{Type: ComponentTypeForm, ID: "empty-form"},
		Fields: []FormField{
			{Key: "only_admin_field", Label: "Only Admin"},
		},
		SubmitAction: ActionRef{ActionID: "test.emptyform.submit"},
	}
	DropFormFields(&form, func(FormField) bool { return false }) // everything drops

	screen := Screen{
		ID:         "test.filter.emptyform",
		Title:      "Empty Form Test",
		Components: []Component{form},
	}
	// keep always true — it is DropComponents that must drop the empty form on
	// its own, not the caller's predicate.
	DropComponents(&screen, func(Component) bool { return true })

	if len(screen.Components) != 0 {
		t.Fatalf("Components = %d, want 0 (empty form should have been removed)", len(screen.Components))
	}

	out := marshalScreen(t, screen)
	if bytes.Contains(out, []byte("empty-form")) {
		t.Errorf("empty form should not appear in the output: %s", out)
	}
}
