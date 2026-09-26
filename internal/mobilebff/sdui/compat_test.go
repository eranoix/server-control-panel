package sdui

import "testing"

// contractWith assembles a minimal synthetic Contract with a single "widget"
// component type containing the fields passed in — enough to exercise Compat
// without depending on any of the 7 real types of the production
// vocabulary.
func contractWith(fields map[string]FieldContract) Contract {
	return Contract{
		ComponentTypes: map[string]ComponentContract{
			"widget": {Fields: fields},
		},
	}
}

func hasBreak(breaks []Break, kind, field string) bool {
	for _, b := range breaks {
		if b.Kind == kind && (field == "" || b.Field == field) {
			return true
		}
	}
	return false
}

func breakingOnly(breaks []Break) []Break {
	var out []Break
	for _, b := range breaks {
		if b.Severity == SeverityBreaking {
			out = append(out, b)
		}
	}
	return out
}

// Test 1 (BREAKING): a field mandatory in frozen, absent from current.
func TestCompat_FieldRemoved(t *testing.T) {
	frozen := contractWith(map[string]FieldContract{
		"name": {JSONType: "string", Required: true},
	})
	current := contractWith(map[string]FieldContract{})

	breaks := Compat(frozen, current)
	if !hasBreak(breaks, "field_removed", "name") {
		t.Fatalf("expected field_removed for \"name\", breaks=%+v", breaks)
	}
	if len(breakingOnly(breaks)) == 0 {
		t.Fatal("expected at least one breaking Break")
	}
}

// Test 2 (BREAKING): a field mandatory in frozen turns optional in current.
func TestCompat_FieldOptionalized(t *testing.T) {
	frozen := contractWith(map[string]FieldContract{
		"name": {JSONType: "string", Required: true},
	})
	current := contractWith(map[string]FieldContract{
		"name": {JSONType: "string", Required: false},
	})

	breaks := Compat(frozen, current)
	if !hasBreak(breaks, "field_optionalized", "name") {
		t.Fatalf("expected field_optionalized for \"name\", breaks=%+v", breaks)
	}
}

// Test 3 (BREAKING): a field's JSONType changed.
func TestCompat_TypeChanged(t *testing.T) {
	frozen := contractWith(map[string]FieldContract{
		"tags": {JSONType: "string", Required: true},
	})
	current := contractWith(map[string]FieldContract{
		"tags": {JSONType: "array", Required: true},
	})

	breaks := Compat(frozen, current)
	if !hasBreak(breaks, "type_changed", "tags") {
		t.Fatalf("expected type_changed for \"tags\", breaks=%+v", breaks)
	}
}

// Test 4 (BREAKING): a field's Const changed.
func TestCompat_ConstChanged(t *testing.T) {
	frozen := contractWith(map[string]FieldContract{
		"type": {JSONType: "string", Required: true, Const: "widget"},
	})
	current := contractWith(map[string]FieldContract{
		"type": {JSONType: "string", Required: true, Const: "gadget"},
	})

	breaks := Compat(frozen, current)
	if !hasBreak(breaks, "const_changed", "type") {
		t.Fatalf("expected const_changed for \"type\", breaks=%+v", breaks)
	}
}

// Test 5 (BREAKING): a whole component type vanished from the current contract.
func TestCompat_ComponentTypeRemoved(t *testing.T) {
	frozen := Contract{
		ComponentTypes: map[string]ComponentContract{
			"widget": {Fields: map[string]FieldContract{"name": {JSONType: "string", Required: true}}},
		},
	}
	current := Contract{ComponentTypes: map[string]ComponentContract{}}

	breaks := Compat(frozen, current)
	if !hasBreak(breaks, "component_type_removed", "") {
		t.Fatalf("expected component_type_removed, breaks=%+v", breaks)
	}
}

// Test 6 (BREAKING): an enum value vanished.
func TestCompat_EnumNarrowed(t *testing.T) {
	frozen := contractWith(map[string]FieldContract{
		"kind": {JSONType: "string", Required: true, Enum: []string{"line", "bar"}},
	})
	current := contractWith(map[string]FieldContract{
		"kind": {JSONType: "string", Required: true, Enum: []string{"line"}},
	})

	breaks := Compat(frozen, current)
	if !hasBreak(breaks, "enum_narrowed", "kind") {
		t.Fatalf("expected enum_narrowed for \"kind\", breaks=%+v", breaks)
	}
}

// Test 7 (COMPATIBLE): a new component type in current, absent from frozen —
// the old app simply skips what it does not know.
func TestCompat_NewComponentType_IsCompatible(t *testing.T) {
	frozen := Contract{
		ComponentTypes: map[string]ComponentContract{
			"widget": {Fields: map[string]FieldContract{"name": {JSONType: "string", Required: true}}},
		},
	}
	current := Contract{
		ComponentTypes: map[string]ComponentContract{
			"widget": {Fields: map[string]FieldContract{"name": {JSONType: "string", Required: true}}},
			"gadget": {Fields: map[string]FieldContract{"label": {JSONType: "string", Required: true}}},
		},
	}

	breaks := breakingOnly(Compat(frozen, current))
	if len(breaks) != 0 {
		t.Fatalf("a new component type should not break, breaks=%+v", breaks)
	}
}

// Test 8 (COMPATIBLE): a new OPTIONAL field in current — no break at all,
// not even an informative one (ignoreUnknownKeys on the client side covers
// it with no reaction required).
func TestCompat_NewOptionalField_IsCompatible(t *testing.T) {
	frozen := contractWith(map[string]FieldContract{
		"name": {JSONType: "string", Required: true},
	})
	current := contractWith(map[string]FieldContract{
		"name":     {JSONType: "string", Required: true},
		"subtitle": {JSONType: "string", Required: false},
	})

	breaks := Compat(frozen, current)
	if len(breaks) != 0 {
		t.Fatalf("a new optional field should not generate any Break, breaks=%+v", breaks)
	}
}

// Test 9 (COMPATIBLE, with a note): a new MANDATORY field in current that
// frozen never had — it does not break the old client (which never knew that
// field existed), but it is recorded as Severity note for review.
func TestCompat_NewRequiredField_IsCompatibleButNoted(t *testing.T) {
	frozen := contractWith(map[string]FieldContract{
		"name": {JSONType: "string", Required: true},
	})
	current := contractWith(map[string]FieldContract{
		"name":  {JSONType: "string", Required: true},
		"owner": {JSONType: "string", Required: true},
	})

	breaks := Compat(frozen, current)
	if len(breakingOnly(breaks)) != 0 {
		t.Fatalf("a new required field should not break the old app, breaks=%+v", breaks)
	}
	if !hasBreak(breaks, "field_added_required", "owner") {
		t.Fatalf("expected a field_added_required note for \"owner\", breaks=%+v", breaks)
	}
	for _, b := range breaks {
		if b.Kind == "field_added_required" && b.Severity != SeverityNote {
			t.Fatalf("field_added_required should have Severity note, had %q", b.Severity)
		}
	}
}

// Test 10 (FixtureRenderable, BREAKING): a fixture with a component whose
// type is unknown to frozen AND critical:true — the already published app
// would show an update card today.
func TestFixtureRenderable_CriticalUnknown_IsBreaking(t *testing.T) {
	frozen := Contract{ComponentTypes: map[string]ComponentContract{}}
	fixture := []byte(`{
		"sdui_version": 1,
		"screen": {"id": "s", "title": "S", "components": [
			{"type": "gantt", "id": "g1", "critical": true}
		]}
	}`)

	breaks := FixtureRenderable(fixture, frozen)
	if !hasBreak(breaks, "critical_unknown_for_client", "") {
		t.Fatalf("expected critical_unknown_for_client, breaks=%+v", breaks)
	}
	if len(breakingOnly(breaks)) == 0 {
		t.Fatal("expected a breaking Break")
	}
}

// Test 11 (FixtureRenderable, COMPATIBLE): the same unknown type, without
// critical (or false) — the old app ignores the component by design.
func TestFixtureRenderable_NonCriticalUnknown_IsCompatible(t *testing.T) {
	frozen := Contract{ComponentTypes: map[string]ComponentContract{}}
	fixture := []byte(`{
		"sdui_version": 1,
		"screen": {"id": "s", "title": "S", "components": [
			{"type": "gantt", "id": "g1"}
		]}
	}`)

	breaks := FixtureRenderable(fixture, frozen)
	if len(breakingOnly(breaks)) != 0 {
		t.Fatalf("an unknown non-critical type should not break, breaks=%+v", breaks)
	}
}

// Test 12 (FixtureRenderable, BREAKING): a fixture whose component (of a
// type frozen knows) omits a field frozen marks
// mandatory.
func TestFixtureRenderable_RequiredFieldMissing_IsBreaking(t *testing.T) {
	frozen := Contract{
		ComponentTypes: map[string]ComponentContract{
			"table": {Fields: map[string]FieldContract{
				"type":        {JSONType: "string", Required: true, Const: "table"},
				"id":          {JSONType: "string", Required: true},
				"columns":     {JSONType: "array", Required: true},
				"rows_source": {JSONType: "object", Required: true},
			}},
		},
	}
	fixture := []byte(`{
		"sdui_version": 1,
		"screen": {"id": "s", "title": "S", "components": [
			{"type": "table", "id": "t1", "columns": []}
		]}
	}`)

	breaks := FixtureRenderable(fixture, frozen)
	if !hasBreak(breaks, "required_field_missing_in_payload", "rows_source") {
		t.Fatalf("expected required_field_missing_in_payload for \"rows_source\", breaks=%+v", breaks)
	}
}

// Not one of the twelve cases above, but it closes the same reasoning: a
// fixture with no "sdui_version" (e.g. validation-error.json) is not a screen
// and must produce no Break at all.
func TestFixtureRenderable_NonScreenFixture_IsSkipped(t *testing.T) {
	frozen := Contract{ComponentTypes: map[string]ComponentContract{}}
	fixture := []byte(`{"error": "validation_failed", "fields": {"name": ["required"]}}`)

	breaks := FixtureRenderable(fixture, frozen)
	if len(breaks) != 0 {
		t.Fatalf("a non-screen fixture should not generate any Break, breaks=%+v", breaks)
	}
}
