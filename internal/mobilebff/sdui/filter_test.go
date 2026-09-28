package sdui

import (
	"bytes"
	"encoding/json"
	"testing"
)

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
			return false
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

func TestFilter_NonAdmin_OmitsAdminOnlyFromBytes(t *testing.T) {
	screen := buildFilterTestScreen(false)
	out := marshalScreen(t, screen)

	for _, forbidden := range []string{"test.admin.only", "run_as_root"} {
		if bytes.Contains(out, []byte(forbidden)) {
			t.Errorf("non-admin output contains %q, should be absent from the bytes: %s", forbidden, out)
		}
	}
	if bytes.Contains(out, []byte("test.admin.only.row")) {
		t.Errorf("non-admin output contains the admin-only row_action, should be absent from the bytes: %s", out)
	}
}

func TestFilter_Admin_IncludesAdminOnlyInBytes(t *testing.T) {
	screen := buildFilterTestScreen(true)
	out := marshalScreen(t, screen)

	for _, want := range []string{"test.admin.only", "run_as_root", "test.admin.only.row"} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("admin output does not contain %q, should be present: %s", want, out)
		}
	}
}

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
	DropComponents(&screen, func(Component) bool { return true })

	out := marshalScreen(t, screen)
	if !bytes.Contains(out, []byte("test.hinted.action")) {
		t.Errorf("a component with permission_hint should remain present (the hint is not the gate): %s", out)
	}
	if !bytes.Contains(out, []byte(`"permission_hint":"admin.write"`)) {
		t.Errorf("permission_hint should have been serialized as-is, with no gating logic: %s", out)
	}
}

func TestFilter_EmptyForm_IsDropped(t *testing.T) {
	form := FormComponent{
		ComponentBase: ComponentBase{Type: ComponentTypeForm, ID: "empty-form"},
		Fields: []FormField{
			{Key: "only_admin_field", Label: "Only Admin"},
		},
		SubmitAction: ActionRef{ActionID: "test.emptyform.submit"},
	}
	DropFormFields(&form, func(FormField) bool { return false })

	screen := Screen{
		ID:         "test.filter.emptyform",
		Title:      "Empty Form Test",
		Components: []Component{form},
	}
	DropComponents(&screen, func(Component) bool { return true })

	if len(screen.Components) != 0 {
		t.Fatalf("Components = %d, want 0 (empty form should have been removed)", len(screen.Components))
	}

	out := marshalScreen(t, screen)
	if bytes.Contains(out, []byte("empty-form")) {
		t.Errorf("empty form should not appear in the output: %s", out)
	}
}
