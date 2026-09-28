package sdui

import "testing"

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

func TestFixtureRenderable_NonScreenFixture_IsSkipped(t *testing.T) {
	frozen := Contract{ComponentTypes: map[string]ComponentContract{}}
	fixture := []byte(`{"error": "validation_failed", "fields": {"name": ["required"]}}`)

	breaks := FixtureRenderable(fixture, frozen)
	if len(breaks) != 0 {
		t.Fatalf("a non-screen fixture should not generate any Break, breaks=%+v", breaks)
	}
}
