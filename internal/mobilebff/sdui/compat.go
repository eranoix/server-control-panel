package sdui

import (
	"encoding/json"
	"fmt"
)

// Compat and FixtureRenderable exist to answer, mechanically, the question
// the compatibility gate demands: "does this server change break an Android
// app that is already installed on somebody's phone?"
//
// The ASYMMETRY that makes the rules below correct — and that a future reader
// will get wrong unless they read this comment before touching anything here:
//
//	frozen  = what an ALREADY PUBLISHED app knows how to interpret (frozen at
//	          the moment that build was shipped to F-Droid).
//	current = what the server is able to emit TODAY.
//
// Compatibility means "everything the old app requires, the new server still
// guarantees". Hence:
//   - ADDITIONS are safe: a new component type, a new field, a new enum
//     value — the old app simply ignores what it does not know (that is the
//     whole point of the tolerant client).
//   - REMOVALS and WEAKENINGS are not: a field the old app requires
//     disappearing, turning optional, changing type, or an enum value the old
//     app has already seen disappearing from the current enum — all of that
//     is a change the old app cannot absorb, because it will never run again
//     the code that would decide how to react to that change.
//
// Swapping frozen and current in the calls below produces a gate that passes
// on exactly the changes it exists to catch — silently, because every
// "removal" seen backwards looks like an "addition". The compatibility tests
// (the additive cases) in compat_test.go fail loudly if the direction is
// inverted.

// Severity classifies a Break: "breaking" fails the build (Compat and
// FixtureRenderable return it for every change an already published app does
// not survive); "note" is merely informative — recorded for visibility (e.g.
// a new mandatory field on the server, which does not break the old app
// because it never knew that field existed) but never fails the gate.
type Severity string

const (
	SeverityBreaking Severity = "breaking"
	SeverityNote     Severity = "note"
)

// Break is a struct, not a string, so that cmd/sdui-compat can group and
// count without parsing messages — a CI failure names exactly what broke:
// which type/object, which field, what kind of break.
type Break struct {
	Kind          string
	ComponentType string // component type, object name, or "action_descriptor"
	Field         string
	Detail        string
	Severity      Severity
}

func (b Break) String() string {
	if b.Field != "" {
		return fmt.Sprintf("[%s] %s.%s: %s", b.Severity, b.ComponentType, b.Field, b.Detail)
	}
	return fmt.Sprintf("[%s] %s: %s", b.Severity, b.ComponentType, b.Detail)
}

// Compat compares a frozen manifest (what an already published app knows how
// to interpret) against the current contract (what the server emits today)
// and returns every divergence — covering the 7 component types, every
// referenced supporting object (DataSource, TableColumn, FormField, ...) and
// the ActionDescriptor. A type/object removed from the current contract is by
// itself enough to fail (there are no fields left to compare); otherwise the
// fields on both sides are compared one by one by compareFields.
func Compat(frozen, current Contract) []Break {
	var breaks []Break

	for ct, frozenCC := range frozen.ComponentTypes {
		currentCC, ok := current.ComponentTypes[ct]
		if !ok {
			breaks = append(breaks, Break{
				Kind:          "component_type_removed",
				ComponentType: ct,
				Severity:      SeverityBreaking,
				Detail:        fmt.Sprintf("component type %q exists in the frozen manifest but was removed from the current contract — a reviewed vocabulary removal is a deliberate event, never a silent one", ct),
			})
			continue
		}
		breaks = append(breaks, compareFields(ct, frozenCC.Fields, currentCC.Fields)...)
	}

	for name, frozenOC := range frozen.Objects {
		currentOC, ok := current.Objects[name]
		if !ok {
			breaks = append(breaks, Break{
				Kind:          "object_removed",
				ComponentType: name,
				Severity:      SeverityBreaking,
				Detail:        fmt.Sprintf("supporting object %q exists in the frozen manifest but was removed from the current contract", name),
			})
			continue
		}
		breaks = append(breaks, compareFields(name, frozenOC.Fields, currentOC.Fields)...)
	}

	breaks = append(breaks, compareFields("action_descriptor", frozen.ActionDescriptor.Fields, current.ActionDescriptor.Fields)...)

	return breaks
}

// compareFields compares the fields of a single component type/object between
// frozen and current, labelling every Break with label (the name of the
// type/object/"action_descriptor" the fields belong to).
func compareFields(label string, frozenFields, currentFields map[string]FieldContract) []Break {
	var breaks []Break

	for name, ffc := range frozenFields {
		cfc, ok := currentFields[name]
		if !ok {
			breaks = append(breaks, Break{
				Kind:          "field_removed",
				ComponentType: label,
				Field:         name,
				Severity:      SeverityBreaking,
				Detail:        fmt.Sprintf("field %q existed in the frozen manifest and no longer exists in the current contract — the already published app may depend on it", name),
			})
			continue
		}

		if ffc.Required && !cfc.Required {
			breaks = append(breaks, Break{
				Kind:          "field_optionalized",
				ComponentType: label,
				Field:         name,
				Severity:      SeverityBreaking,
				Detail:        fmt.Sprintf("field %q was required in the frozen manifest and is now optional — the server may start omitting it", name),
			})
		}

		if ffc.JSONType != cfc.JSONType {
			breaks = append(breaks, Break{
				Kind:          "type_changed",
				ComponentType: label,
				Field:         name,
				Severity:      SeverityBreaking,
				Detail:        fmt.Sprintf("json_type of field %q changed from %q to %q", name, ffc.JSONType, cfc.JSONType),
			})
		}

		if ffc.Const != cfc.Const {
			breaks = append(breaks, Break{
				Kind:          "const_changed",
				ComponentType: label,
				Field:         name,
				Severity:      SeverityBreaking,
				Detail:        fmt.Sprintf("const of field %q changed from %q to %q", name, ffc.Const, cfc.Const),
			})
		}

		if len(ffc.Enum) > 0 {
			currentSet := make(map[string]bool, len(cfc.Enum))
			for _, v := range cfc.Enum {
				currentSet[v] = true
			}
			for _, v := range ffc.Enum {
				if !currentSet[v] {
					breaks = append(breaks, Break{
						Kind:          "enum_narrowed",
						ComponentType: label,
						Field:         name,
						Severity:      SeverityBreaking,
						Detail:        fmt.Sprintf("enum value %q of field %q existed in the frozen manifest and was removed from the current contract", v, name),
					})
				}
			}
		}
	}

	for name, cfc := range currentFields {
		if _, existed := frozenFields[name]; existed {
			continue
		}
		if cfc.Required {
			// COMPATIBLE for the old app (it never knew this field existed, and the
			// tolerant parser ignores unknown keys) — but informative, because a new
			// field marked mandatory in Go is usually a sign that something on the
			// server side started depending on it without accounting for older
			// clients.
			breaks = append(breaks, Break{
				Kind:          "field_added_required",
				ComponentType: label,
				Field:         name,
				Severity:      SeverityNote,
				Detail:        fmt.Sprintf("field %q is new and required in the current contract; the already published app never knew it and ignores the key (no breakage), but it is worth a review", name),
			})
		}
	}

	return breaks
}

// fixtureCompatScreen is the minimum shape needed to walk a fixture's
// components without depending on UnmarshalScreen — identical to
// contract_test.go's fixtureScreen, deliberately duplicated here because this
// function runs in production (via cmd/sdui-compat), not only in tests, and
// must not import private symbols from a _test.go file.
type fixtureCompatScreen struct {
	SDUIVersion *int                     `json:"sdui_version"`
	Screen      *fixtureCompatScreenBody `json:"screen"`
}

type fixtureCompatScreenBody struct {
	ID         string                   `json:"id"`
	Components []map[string]interface{} `json:"components"`
}

// FixtureRenderable checks whether a fixture payload — the exact byte shape
// the server would emit today for some screen — is still renderable by a
// client whose manifest is frozen in frozen. This covers a hole Compat does
// not: Compat compares the VOCABULARY (which fields each component type has);
// FixtureRenderable compares a real PAYLOAD against that frozen vocabulary,
// catching a screen Builder that stopped emitting a mandatory field without
// the vocabulary itself having changed.
//
// A fixture with no "sdui_version" (e.g. validation-error.json) is not a
// screen and returns nil — outside this function's scope.
func FixtureRenderable(fixture []byte, frozen Contract) []Break {
	var top map[string]interface{}
	if err := json.Unmarshal(fixture, &top); err != nil {
		return []Break{{
			Kind:     "fixture_unreadable",
			Severity: SeverityBreaking,
			Detail:   fmt.Sprintf("fixture is not valid JSON: %v", err),
		}}
	}
	if _, hasVersion := top["sdui_version"]; !hasVersion {
		return nil
	}

	var fx fixtureCompatScreen
	if err := json.Unmarshal(fixture, &fx); err != nil || fx.Screen == nil {
		return []Break{{
			Kind:     "fixture_unreadable",
			Severity: SeverityBreaking,
			Detail:   fmt.Sprintf("fixture has sdui_version but does not decode as a screen: %v", err),
		}}
	}

	var breaks []Break
	for i, comp := range fx.Screen.Components {
		typ, _ := comp["type"].(string)
		cc, known := frozen.ComponentTypes[typ]
		if !known {
			critical, _ := comp["critical"].(bool)
			if critical {
				breaks = append(breaks, Break{
					Kind:          "critical_unknown_for_client",
					ComponentType: typ,
					Field:         fmt.Sprintf("components[%d]", i),
					Severity:      SeverityBreaking,
					Detail:        fmt.Sprintf("component %d has type=%q, absent from the frozen manifest, with critical=true — the already published app would show an \"update the app\" card in this position today", i, typ),
				})
			}
			// critical=false/absent: COMPATIBLE — the old app ignores the unknown
			// component and renders the rest, by design.
			continue
		}

		for fieldName, fc := range cc.Fields {
			if !fc.Required {
				continue
			}
			if _, present := comp[fieldName]; !present {
				breaks = append(breaks, Break{
					Kind:          "required_field_missing_in_payload",
					ComponentType: typ,
					Field:         fieldName,
					Severity:      SeverityBreaking,
					Detail:        fmt.Sprintf("component %d (type=%q) does not carry field %q, which the frozen manifest marks required for this type", i, typ, fieldName),
				})
			}
		}
	}
	return breaks
}
