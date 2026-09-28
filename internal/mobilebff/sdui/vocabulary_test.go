package sdui

import (
	"encoding/json"
	"reflect"
	"testing"
)

var allComponentGoTypes = []reflect.Type{
	reflect.TypeOf(FormComponent{}),
	reflect.TypeOf(TableComponent{}),
	reflect.TypeOf(ListComponent{}),
	reflect.TypeOf(DetailComponent{}),
	reflect.TypeOf(ActionComponent{}),
	reflect.TypeOf(ChartComponent{}),
	reflect.TypeOf(ConfirmDestructiveComponent{}),
}

func TestVocabularyIsExactlySevenTypes(t *testing.T) {
	got := AllComponentTypes()
	if len(got) != 7 {
		t.Fatalf("AllComponentTypes() has %d types, want exactly 7 (closed vocabulary) — if this was a deliberate type addition, update this test explicitly; the documented pitfall", len(got))
	}

	want := map[ComponentType]bool{
		ComponentTypeForm:               true,
		ComponentTypeTable:              true,
		ComponentTypeList:               true,
		ComponentTypeDetail:             true,
		ComponentTypeAction:             true,
		ComponentTypeChart:              true,
		ComponentTypeConfirmDestructive: true,
	}
	seen := map[ComponentType]bool{}
	for _, ct := range got {
		if !want[ct] {
			t.Errorf("AllComponentTypes() contains %q, outside the expected closed set", ct)
		}
		if seen[ct] {
			t.Errorf("AllComponentTypes() repeats %q", ct)
		}
		seen[ct] = true
	}
	for ct := range want {
		if !seen[ct] {
			t.Errorf("AllComponentTypes() does not contain %q", ct)
		}
	}

	if len(allComponentGoTypes) != len(got) {
		t.Fatalf("allComponentGoTypes (tests) has %d structs, AllComponentTypes() has %d — this test file's reflect list fell out of sync with the vocabulary", len(allComponentGoTypes), len(got))
	}
}

var componentTypeInterfaceType = reflect.TypeOf((*Component)(nil)).Elem()

var forbiddenFieldNames = map[string]bool{
	"children":      true,
	"components":    true,
	"condition":     true,
	"conditions":    true,
	"expression":    true,
	"visible_when":  true,
	"hidden_when":   true,
	"formula":       true,
	"repeat_over":   true,
	"template_expr": true,
	"bindings":      true,
}

func walkExportedFields(t reflect.Type, visit func(structName string, f reflect.StructField)) {
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		if f.Anonymous && f.Type.Kind() == reflect.Struct {
			walkExportedFields(f.Type, visit)
			continue
		}
		visit(t.Name(), f)
	}
}

func TestNoNestingOrConditionalFields(t *testing.T) {
	for _, ct := range allComponentGoTypes {
		walkExportedFields(ct, func(structName string, f reflect.StructField) {
			jsonName := jsonFieldName(f)
			if jsonName != "" && forbiddenFieldNames[jsonName] {
				t.Errorf(
					"%s.%s has json:%q — forbidden field name (programming-language-in-JSON). The SDUI vocabulary cannot express client-side conditionals, expressions or repetition.",
					structName, f.Name, jsonName,
				)
			}

			ft := f.Type
			switch ft.Kind() {
			case reflect.Slice, reflect.Array:
				if ft.Elem() == componentTypeInterfaceType {
					t.Errorf("%s.%s is []Component — nesting a component inside a component is forbidden ()", structName, f.Name)
				}
			case reflect.Map:
				if ft.Elem() == componentTypeInterfaceType {
					t.Errorf("%s.%s is map[...]Component — nesting a component inside a component is forbidden ()", structName, f.Name)
				}
			case reflect.Interface:
				if ft == componentTypeInterfaceType {
					t.Errorf("%s.%s is of type Component — a component must not reference another component directly ()", structName, f.Name)
				}
			}
		})
	}
}

func jsonFieldName(f reflect.StructField) string {
	tag := f.Tag.Get("json")
	if tag == "" || tag == "-" {
		return ""
	}
	for i := 0; i < len(tag); i++ {
		if tag[i] == ',' {
			return tag[:i]
		}
	}
	return tag
}

func countExportedFields(t reflect.Type) int {
	n := 0
	walkExportedFields(t, func(string, reflect.StructField) { n++ })
	return n
}

func TestComponentFieldCountWarningSign(t *testing.T) {
	const maxFields = 8
	for _, ct := range allComponentGoTypes {
		n := countExportedFields(ct)
		if n > maxFields {
			t.Errorf("%s has %d exported fields (limit %d, including the 4 base fields) — a warning sign: a type accumulating properties for multiple uses. Consider a new component instead of another field.", ct.Name(), n, maxFields)
		}
	}
}

func TestEnvelopeRoundTrip(t *testing.T) {
	original := Envelope{
		SDUIVersion: 1,
		Screen: Screen{
			ID:    "fixture.roundtrip",
			Title: "Round Trip",
			Components: []Component{
				FormComponent{
					ComponentBase: ComponentBase{Type: ComponentTypeForm, ID: "f1", PermissionHint: "docker.write"},
					Fields: []FormField{
						{Key: "name", Label: "Name", Kind: "text", Required: true},
					},
					SubmitAction: ActionRef{ActionID: "notify.rule.create"},
				},
				TableComponent{
					ComponentBase: ComponentBase{Type: ComponentTypeTable, ID: "t1"},
					Columns: []TableColumn{
						{Key: "name", Label: "Name", Kind: "text"},
						{Key: "status", Label: "Status", Kind: "badge", BadgeMap: map[string]string{"running": "success"}},
					},
					RowsSource: DataSource{Endpoint: "/api/mobile/v1/docker/containers", Method: "GET"},
					RowActions: []ActionRef{{ActionID: "container.stop"}},
					EmptyState: &EmptyState{Text: "No containers"},
				},
				ListComponent{
					ComponentBase: ComponentBase{Type: ComponentTypeList, ID: "l1"},
					ItemTemplate:  "notification_card",
					RowsSource:    DataSource{Endpoint: "/api/mobile/v1/notify/inbox"},
				},
				DetailComponent{
					ComponentBase: ComponentBase{Type: ComponentTypeDetail, ID: "d1"},
					DataSource:    DataSource{Endpoint: "/api/mobile/v1/docker/containers/{id}"},
					Fields:        []DetailField{{Key: "image", Label: "Image"}},
				},
				ActionComponent{
					ComponentBase: ComponentBase{Type: ComponentTypeAction, ID: "a1", Critical: true},
					Label:         "Deploy",
					ActionID:      "deploy.trigger",
					Style:         "primary",
				},
				ChartComponent{
					ComponentBase: ComponentBase{Type: ComponentTypeChart, ID: "c1"},
					ChartKind:     "line",
					SeriesSource:  DataSource{Endpoint: "/api/mobile/v1/system/history?metric=cpu"},
					XKey:          "ts",
					YKey:          "value",
				},
				ConfirmDestructiveComponent{
					ComponentBase:            ComponentBase{Type: ComponentTypeConfirmDestructive, ID: "cd1"},
					ActionID:                 "container.stop",
					Message:                  "This will stop the container permanently.",
					RequireTypedConfirmation: "my-container",
				},
			},
		},
	}

	firstPass, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal of the original envelope: %v", err)
	}

	decoded, err := UnmarshalScreen(firstPass)
	if err != nil {
		t.Fatalf("UnmarshalScreen: %v", err)
	}

	secondPass, err := json.Marshal(*decoded)
	if err != nil {
		t.Fatalf("marshal of the decoded envelope: %v", err)
	}

	if string(firstPass) != string(secondPass) {
		t.Fatalf("round-trip is not byte-identical:\n--- first serialization ---\n%s\n--- second serialization (after UnmarshalScreen) ---\n%s", firstPass, secondPass)
	}

	if len(decoded.Screen.Components) != len(AllComponentTypes()) {
		t.Fatalf("decoded.Screen.Components has %d components, want %d (one of each type)", len(decoded.Screen.Components), len(AllComponentTypes()))
	}
}

func TestUnknownComponentTypeIsServerSideError(t *testing.T) {
	payload := []byte(`{"sdui_version":1,"screen":{"id":"s","title":"S","components":[{"type":"gantt","id":"g1"}]}}`)
	_, err := UnmarshalScreen(payload)
	if err == nil {
		t.Fatal("UnmarshalScreen accepted an unknown type (\"gantt\") without error — the server must never interpret a type outside the vocabulary")
	}
	if !isErrUnknownComponentType(err) {
		t.Fatalf("returned error does not wrap ErrUnknownComponentType: %v", err)
	}
}

func isErrUnknownComponentType(err error) bool {
	for err != nil {
		if err == ErrUnknownComponentType {
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

func TestDisallowUnknownFieldsRejectsExtraKey(t *testing.T) {
	payload := []byte(`{"sdui_version":1,"screen":{"id":"s","title":"S","components":[{"type":"table","id":"t1","columns":[],"rows_source":{"endpoint":"/x"},"sort_default":"name"}]}}`)
	_, err := UnmarshalScreen(payload)
	if err == nil {
		t.Fatal("UnmarshalScreen accepted a field (\"sort_default\") outside the table contract without error")
	}
}
