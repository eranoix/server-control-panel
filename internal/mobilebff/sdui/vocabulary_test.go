package sdui

import (
	"encoding/json"
	"reflect"
	"testing"
)

// allComponentGoTypes is the list of the 7 concrete structs of the
// vocabulary, used by the closure tests below. Deliberately separate from the
// implementation (component.go does not need reflect) — if an eighth type is
// added to AllComponentTypes() without editing this list, TestVocabulary
// (Test 1) and TestVocabularyMatchesGoTypes catch the divergence already.
var allComponentGoTypes = []reflect.Type{
	reflect.TypeOf(FormComponent{}),
	reflect.TypeOf(TableComponent{}),
	reflect.TypeOf(ListComponent{}),
	reflect.TypeOf(DetailComponent{}),
	reflect.TypeOf(ActionComponent{}),
	reflect.TypeOf(ChartComponent{}),
	reflect.TypeOf(ConfirmDestructiveComponent{}),
}

// TestVocabularyIsExactlySevenTypes is Test 1: the vocabulary has exactly 7
// types and is exactly the documented set. `len(...) != 7` fails literally —
// adding an eighth type is a deliberate, reviewed edit of this test, never an
// incidental addition. See the documented pitfall and the package comment in
// component.go.
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
			t.Errorf("AllComponentTypes() repete %q", ct)
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

// componentTypeInterfaceType is used by Test 2 (the anti-nesting guard) to
// detect a field whose type is a slice/map of Component — the concrete shape
// a "children: [component]" would take in Go.
var componentTypeInterfaceType = reflect.TypeOf((*Component)(nil)).Elem()

// forbiddenFieldNames are the JSON field names that, if they appear in ANY
// component struct, mean the vocabulary has turned into a programming
// language (conditionals, expressions, client-side repetition)
// — exactly the documented the documented pitfall.
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

// walkExportedFields visits every exported field of t, recursing into
// anonymous fields (embedding) so that ComponentBase is included in the sweep
// of each concrete type.
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

// TestNoNestingOrConditionalFields is Test 2 (the
// anti-programming-language guard): no field of any component may be called
// children/condition/expression/etc, and no field may be a slice/map of
// Component (which would be component-inside-component nesting). The failure
// cites the documented pitfall by name, deliberately.
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

// jsonFieldName extracts the JSON key name from a struct tag, ignoring
// options such as omitempty. Fields tagged "-" or with no json tag have no
// name relevant to the check.
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

// countExportedFields counts exported fields recursing into embeds, the same
// way walkExportedFields does, but returning only the count.
func countExportedFields(t reflect.Type) int {
	n := 0
	walkExportedFields(t, func(string, reflect.StructField) { n++ })
	return n
}

// TestComponentFieldCountWarningSign is Test 3: no component struct may have
// more than 8 exported fields counting the 4 base fields
// (Type/ID/PermissionHint/Critical) — the warning sign documented in
// of a type accumulating properties to cover multiple use cases
// instead of a new, specific type being born.
func TestComponentFieldCountWarningSign(t *testing.T) {
	const maxFields = 8
	for _, ct := range allComponentGoTypes {
		n := countExportedFields(ct)
		if n > maxFields {
			t.Errorf("%s has %d exported fields (limit %d, including the 4 base fields) — a warning sign: a type accumulating properties for multiple uses. Consider a new component instead of another field.", ct.Name(), n, maxFields)
		}
	}
}

// TestEnvelopeRoundTrip is Test 4: a Screen with one component of each of the
// 7 types serializes and comes back byte-for-byte identical through
// UnmarshalScreen with DisallowUnknownFields enabled.
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
						{Key: "name", Label: "Nome", Kind: "text", Required: true},
					},
					SubmitAction: ActionRef{ActionID: "notify.rule.create"},
				},
				TableComponent{
					ComponentBase: ComponentBase{Type: ComponentTypeTable, ID: "t1"},
					Columns: []TableColumn{
						{Key: "name", Label: "Nome", Kind: "text"},
						{Key: "status", Label: "Status", Kind: "badge", BadgeMap: map[string]string{"running": "success"}},
					},
					RowsSource: DataSource{Endpoint: "/api/mobile/v1/docker/containers", Method: "GET"},
					RowActions: []ActionRef{{ActionID: "container.stop"}},
					EmptyState: &EmptyState{Text: "Nenhum container"},
				},
				ListComponent{
					ComponentBase: ComponentBase{Type: ComponentTypeList, ID: "l1"},
					ItemTemplate:  "notification_card",
					RowsSource:    DataSource{Endpoint: "/api/mobile/v1/notify/inbox"},
				},
				DetailComponent{
					ComponentBase: ComponentBase{Type: ComponentTypeDetail, ID: "d1"},
					DataSource:    DataSource{Endpoint: "/api/mobile/v1/docker/containers/{id}"},
					Fields:        []DetailField{{Key: "image", Label: "Imagem"}},
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
					Message:                  "Isto vai parar o container permanentemente.",
					RequireTypedConfirmation: "meu-container",
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

// TestUnknownComponentTypeIsServerSideError proves the asymmetry documented
// in component.go: on the Go side, a "type" outside the vocabulary is always
// an error (ErrUnknownComponentType) — never silently ignored. Tolerance
// for an unknown type is the Kotlin client's responsibility.
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

// TestDisallowUnknownFieldsRejectsExtraKey proves that UnmarshalScreen
// rejects a field outside the contract inside a KNOWN component — the same
// check the unknown-extra-fields.json fixture exercises at corpus level
// (contract_test.go), here at unit level.
func TestDisallowUnknownFieldsRejectsExtraKey(t *testing.T) {
	payload := []byte(`{"sdui_version":1,"screen":{"id":"s","title":"S","components":[{"type":"table","id":"t1","columns":[],"rows_source":{"endpoint":"/x"},"sort_default":"name"}]}}`)
	_, err := UnmarshalScreen(payload)
	if err == nil {
		t.Fatal("UnmarshalScreen accepted a field (\"sort_default\") outside the table contract without error")
	}
}
