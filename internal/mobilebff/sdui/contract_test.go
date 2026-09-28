package sdui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const committedContractPath = "../../../contracts/sdui/contract.json"

const fixturesDir = "../../../contracts/sdui/fixtures"

func TestContractComponentTypesMatchVocabulary(t *testing.T) {
	if len(componentGoTypes) != len(AllComponentTypes()) {
		t.Fatalf("componentGoTypes has %d entries, AllComponentTypes() has %d", len(componentGoTypes), len(AllComponentTypes()))
	}
	for _, ct := range AllComponentTypes() {
		if _, ok := componentGoTypes[ct]; !ok {
			t.Errorf("componentGoTypes has no entry for %q", ct)
		}
	}
}

func TestContractHasExactlySevenComponentTypes(t *testing.T) {
	c := GenerateContract()
	if len(c.ComponentTypes) != 7 {
		t.Fatalf("Contract.ComponentTypes has %d keys, want 7", len(c.ComponentTypes))
	}
	for _, ct := range AllComponentTypes() {
		if _, ok := c.ComponentTypes[string(ct)]; !ok {
			t.Errorf("Contract.ComponentTypes does not contain %q", ct)
		}
	}
}

func TestContractTableRequiredOptionalSplit(t *testing.T) {
	c := GenerateContract()
	table, ok := c.ComponentTypes["table"]
	if !ok {
		t.Fatal("Contract.ComponentTypes does not have \"table\"")
	}

	wantRequired := map[string]bool{
		"type":            true,
		"id":              true,
		"columns":         true,
		"rows_source":     true,
		"permission_hint": false,
		"critical":        false,
		"row_actions":     false,
		"empty_state":     false,
	}

	if len(table.Fields) != len(wantRequired) {
		t.Fatalf("table.Fields has %d fields, test expects %d — struct TableComponent changed without updating this test", len(table.Fields), len(wantRequired))
	}

	for name, wantReq := range wantRequired {
		fc, ok := table.Fields[name]
		if !ok {
			t.Errorf("table.Fields does not have %q", name)
			continue
		}
		if fc.Required != wantReq {
			t.Errorf("table.Fields[%q].Required = %v, want %v", name, fc.Required, wantReq)
		}
	}
}

func TestContractDriftAgainstCommittedFile(t *testing.T) {
	generated, err := MarshalContract(GenerateContract())
	if err != nil {
		t.Fatalf("MarshalContract: %v", err)
	}

	committed, err := os.ReadFile(committedContractPath)
	if err != nil {
		t.Fatalf("reading %s: %v (the file should exist and be committed)", committedContractPath, err)
	}

	if string(generated) != string(committed) {
		t.Fatalf("contracts/sdui/contract.json is out of date relative to the Go types — run `make sdui-contract` and commit the result")
	}
}

func TestContractNoDanglingRefs(t *testing.T) {
	c := GenerateContract()

	checkFields := func(where string, fields map[string]FieldContract) {
		for name, fc := range fields {
			if fc.Ref != "" {
				if _, ok := c.Objects[fc.Ref]; !ok {
					t.Errorf("%s.%s: Ref=%q does not exist in Contract.Objects", where, name, fc.Ref)
				}
			}
			if fc.ItemRef != "" {
				if _, ok := c.Objects[fc.ItemRef]; !ok {
					t.Errorf("%s.%s: ItemRef=%q does not exist in Contract.Objects", where, name, fc.ItemRef)
				}
			}
		}
	}

	for ctName, cc := range c.ComponentTypes {
		checkFields("component_types."+ctName, cc.Fields)
	}
	for objName, oc := range c.Objects {
		checkFields("objects."+objName, oc.Fields)
	}
	checkFields("action_descriptor", c.ActionDescriptor.Fields)
}

type fixtureScreen struct {
	SDUIVersion *int               `json:"sdui_version"`
	Screen      *fixtureScreenBody `json:"screen"`
}

type fixtureScreenBody struct {
	ID         string                   `json:"id"`
	Title      string                   `json:"title"`
	Components []map[string]interface{} `json:"components"`
}

func TestFixtureConformance(t *testing.T) {
	entries, err := os.ReadDir(fixturesDir)
	if err != nil {
		t.Fatalf("reading %s: %v", fixturesDir, err)
	}

	contract := GenerateContract()

	found := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		name := entry.Name()
		path := filepath.Join(fixturesDir, name)
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}

		var top map[string]interface{}
		if err := json.Unmarshal(raw, &top); err != nil {
			t.Fatalf("%s: invalid JSON: %v", name, err)
		}
		if _, hasVersion := top["sdui_version"]; !hasVersion {
			continue
		}
		found++

		var fx fixtureScreen
		if err := json.Unmarshal(raw, &fx); err != nil {
			t.Fatalf("%s: did not parse as an SDUI screen: %v", name, err)
		}
		if fx.Screen == nil {
			t.Fatalf("%s: has sdui_version but no \"screen\" field", name)
		}

		allowsUnknown := strings.HasPrefix(name, "unknown-")

		for i, comp := range fx.Screen.Components {
			rawType, _ := comp["type"].(string)
			cc, known := contract.ComponentTypes[rawType]
			if !known {
				if !allowsUnknown {
					t.Errorf("%s: component %d has type=%q, outside the contract, but the filename does not start with \"unknown-\"", name, i, rawType)
				}
				continue
			}

			for fieldName, fc := range cc.Fields {
				if !fc.Required {
					continue
				}
				if _, present := comp[fieldName]; !present {
					t.Errorf("%s: component %d (type=%q) is missing the required field %q", name, i, rawType, fieldName)
				}
			}

			if !allowsUnknown {
				for fieldName := range comp {
					if _, inContract := cc.Fields[fieldName]; !inContract {
						t.Errorf("%s: component %d (type=%q) has field %q outside the contract — fixtures that do not start with \"unknown-\" may only use contract fields", name, i, rawType, fieldName)
					}
				}
			}
		}
	}

	if found == 0 {
		t.Fatal("no fixture with sdui_version found in " + fixturesDir + ": the golden corpus is missing")
	}
}

func TestFixtureRoundTripMatchesRealMarshaller(t *testing.T) {
	entries, err := os.ReadDir(fixturesDir)
	if err != nil {
		t.Fatalf("reading %s: %v", fixturesDir, err)
	}

	checked := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		if strings.HasPrefix(name, "unknown-") {
			continue
		}

		path := filepath.Join(fixturesDir, name)
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}

		var top map[string]interface{}
		if err := json.Unmarshal(raw, &top); err != nil {
			t.Fatalf("%s: invalid JSON: %v", name, err)
		}
		if _, hasVersion := top["sdui_version"]; !hasVersion {
			continue
		}
		checked++

		envelope, err := UnmarshalScreen(raw)
		if err != nil {
			t.Fatalf("%s: UnmarshalScreen failed — the fixture does not decode through the server's real path: %v", name, err)
		}

		regenerated, err := json.Marshal(envelope)
		if err != nil {
			t.Fatalf("%s: marshal of the decoded envelope (real production path): %v", name, err)
		}

		if diff := diffCanonicalJSON(t, name, raw, regenerated); diff != "" {
			t.Errorf(
				"%s: the fixture does NOT match the real output of the server's marshaller — the FIXTURE is wrong, fix the file to what the server actually emits (never relax this test):\n%s",
				name, diff,
			)
		}
	}

	if checked == 0 {
		t.Fatal("no screen fixture (not \"unknown-\", with sdui_version) found for the round-trip test")
	}
}

func diffCanonicalJSON(t *testing.T, label string, a, b []byte) string {
	t.Helper()
	var va, vb interface{}
	if err := json.Unmarshal(a, &va); err != nil {
		t.Fatalf("%s: original JSON invalid for canonicalization: %v", label, err)
	}
	if err := json.Unmarshal(b, &vb); err != nil {
		t.Fatalf("%s: regenerated JSON invalid for canonicalization: %v", label, err)
	}
	diffs := diffJSONValues("$", va, vb)
	if len(diffs) == 0 {
		return ""
	}
	return strings.Join(diffs, "\n")
}

func diffJSONValues(path string, a, b interface{}) []string {
	switch av := a.(type) {
	case map[string]interface{}:
		bv, ok := b.(map[string]interface{})
		if !ok {
			return []string{fmt.Sprintf("%s: was an object in the original, is %T in the regenerated output", path, b)}
		}
		var diffs []string
		for k, avVal := range av {
			bval, present := bv[k]
			if !present {
				diffs = append(diffs, fmt.Sprintf("%s.%s: present in the original fixture, MISSING from the real marshaller output", path, k))
				continue
			}
			diffs = append(diffs, diffJSONValues(path+"."+k, avVal, bval)...)
		}
		for k := range bv {
			if _, present := av[k]; !present {
				diffs = append(diffs, fmt.Sprintf("%s.%s: missing from the original fixture, PRESENT in the real marshaller output", path, k))
			}
		}
		return diffs
	case []interface{}:
		bv, ok := b.([]interface{})
		if !ok {
			return []string{fmt.Sprintf("%s: was an array in the original, is %T in the regenerated output", path, b)}
		}
		if len(av) != len(bv) {
			return []string{fmt.Sprintf("%s: array length was %d in the original, is %d in the regenerated output", path, len(av), len(bv))}
		}
		var diffs []string
		for i := range av {
			diffs = append(diffs, diffJSONValues(fmt.Sprintf("%s[%d]", path, i), av[i], bv[i])...)
		}
		return diffs
	default:
		if !reflect.DeepEqual(a, b) {
			return []string{fmt.Sprintf("%s: original=%#v, real marshaller output=%#v", path, a, b)}
		}
		return nil
	}
}
