package sdui

import (
	"encoding/json"
	"fmt"
)

type Severity string

const (
	SeverityBreaking Severity = "breaking"
	SeverityNote     Severity = "note"
)

type Break struct {
	Kind          string
	ComponentType string
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

type fixtureCompatScreen struct {
	SDUIVersion *int                     `json:"sdui_version"`
	Screen      *fixtureCompatScreenBody `json:"screen"`
}

type fixtureCompatScreenBody struct {
	ID         string                   `json:"id"`
	Components []map[string]interface{} `json:"components"`
}

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
