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

// committedContractPath is the path, relative to this package, of the
// committed manifest the CLI (cmd/sdui-contract) writes and that this drift
// test compares byte for byte.
const committedContractPath = "../../../contracts/sdui/contract.json"

// fixturesDir is the directory of the golden fixture corpus.
const fixturesDir = "../../../contracts/sdui/fixtures"

// TestContractComponentTypesMatchVocabulary proves that componentGoTypes (the
// map GenerateContract uses to know what to reflect over) has exactly the
// same keys as AllComponentTypes() — if an eighth type were added to the
// vocabulary without entering componentGoTypes, GenerateContract would panic
// at run time; this test catches it at test time.
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

// TestContractHasExactlySevenComponentTypes is Test 1: the ComponentTypes
// map has exactly the 7 keys of AllComponentTypes().
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

// TestContractTableRequiredOptionalSplit is Test 2: for "table",
// type/id/columns/rows_source are mandatory and
// permission_hint/critical/row_actions/empty_state are optional — derived
// from the presence of omitempty in the struct tag, never from a list written
// by hand in this test (hence the test iterating a map of expectations
// instead of hardcoding an "if field X then Y" per field).
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

// TestContractDriftAgainstCommittedFile is Test 3 (the tamper gate):
// GenerateContract(), formatted exactly as the CLI formats it
// (MarshalContract), has to match the committed
// contracts/sdui/contract.json byte for byte. A manual edit of the file, or
// a struct change without regenerating, fails here.
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

// TestContractNoDanglingRefs is Test 4: every Ref/ItemRef referenced by any
// field (of a component or of an object) exists as a key in
// Contract.Objects.
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

// ---------------------------------------------------------------------------
// Conformance of the golden fixture corpus against the contract.
// ---------------------------------------------------------------------------

// fixtureScreen is the minimum shape needed to validate a fixture against
// the Contract — it deliberately does not use Envelope/UnmarshalScreen,
// because the unknown-* fixtures contain exactly what UnmarshalScreen would
// reject (that is their point: to exercise the CLIENT's tolerance path, not
// the server's strict path).
type fixtureScreen struct {
	SDUIVersion *int               `json:"sdui_version"`
	Screen      *fixtureScreenBody `json:"screen"`
}

type fixtureScreenBody struct {
	ID         string                   `json:"id"`
	Title      string                   `json:"title"`
	Components []map[string]interface{} `json:"components"`
}

// TestFixtureConformance is the corpus gate: every *.json fixture under
// contracts/sdui/fixtures/ whose top level has an sdui_version key has to
// (a) parse and (b), for every component whose "type" IS in the contract,
// contain every field the contract marks mandatory, with no field absent
// from the contract UNLESS the file name starts with "unknown-". Fixtures
// are discovered with os.ReadDir, so a new fixture is covered automatically
// without editing this test.
func TestFixtureConformance(t *testing.T) {
	entries, err := os.ReadDir(fixturesDir)
	if err != nil {
		t.Fatalf("lendo %s: %v", fixturesDir, err)
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
			t.Fatalf("lendo %s: %v", path, err)
		}

		var top map[string]interface{}
		if err := json.Unmarshal(raw, &top); err != nil {
			t.Fatalf("%s: invalid JSON: %v", name, err)
		}
		if _, hasVersion := top["sdui_version"]; !hasVersion {
			// Not an SDUI screen (e.g. validation-error.json) — outside the
			// scope of this conformance test.
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
		t.Fatal("no fixture with sdui_version found in " + fixturesDir + " — a corpus dourada está ausente")
	}
}

// TestFixtureRoundTripMatchesRealMarshaller closes the hole
// TestFixtureConformance leaves open: structural conformance against the
// contract (mandatory fields present, no field outside the contract) does
// NOT prove that Go's real marshaller emits that exact shape — an omitempty
// can make a field disappear, a custom MarshalJSON (such as Screen's) can
// change the shape, and no structural conformance test notices. This is the
// same failure mode this project has already paid for once: a hand-written
// fixture that matched the wrong assumption about the wire format stayed
// green while the real consumer broke on first contact.
//
// For each screen fixture (all but the "unknown-*" ones, see the
// justification below), this test deserializes with UnmarshalScreen — the
// same path the server uses to read back what it produced itself — and
// reserializes with json.Marshal(*Envelope), which invokes
// Screen.MarshalJSON, the SAME marshalling path used in production. The
// result is compared as canonicalized JSON (deserialized values, not a raw
// string) so that key order/spacing can never cause a false negative. A
// divergence points at the exact field and it is always the FIXTURE that is
// wrong — it must be corrected to match the marshaller's real output, never
// the test relaxed to swallow the difference.
//
// The "unknown-*" fixtures are left out by design: they exist specifically
// to carry a "type" or a field the server's Go types cannot represent
// (tolerance for that is the Kotlin client's responsibility) —
// UnmarshalScreen would reject all three with an error, so there is no "real
// marshaller output" to compare against. See fixtures/README.md.
func TestFixtureRoundTripMatchesRealMarshaller(t *testing.T) {
	entries, err := os.ReadDir(fixturesDir)
	if err != nil {
		t.Fatalf("lendo %s: %v", fixturesDir, err)
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
			t.Fatalf("lendo %s: %v", path, err)
		}

		var top map[string]interface{}
		if err := json.Unmarshal(raw, &top); err != nil {
			t.Fatalf("%s: invalid JSON: %v", name, err)
		}
		if _, hasVersion := top["sdui_version"]; !hasVersion {
			// Not an SDUI screen (e.g. validation-error.json) — it does not go
			// through Envelope/UnmarshalScreen, outside this test's scope.
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

// diffCanonicalJSON compares two JSON blobs by VALUE (not by raw string),
// returning a list of divergences with the exact field path, or "" if they
// are semantically identical.
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

// diffJSONValues walks recursively through two already deserialized JSON
// values (map[string]interface{}, []interface{}, or a scalar) and returns
// one divergence per field path where they differ.
func diffJSONValues(path string, a, b interface{}) []string {
	switch av := a.(type) {
	case map[string]interface{}:
		bv, ok := b.(map[string]interface{})
		if !ok {
			return []string{fmt.Sprintf("%s: era objeto no original, é %T na saída regenerada", path, b)}
		}
		var diffs []string
		for k, aval := range av {
			bval, present := bv[k]
			if !present {
				diffs = append(diffs, fmt.Sprintf("%s.%s: presente na fixture original, AUSENTE na saída real do marshaller", path, k))
				continue
			}
			diffs = append(diffs, diffJSONValues(path+"."+k, aval, bval)...)
		}
		for k := range bv {
			if _, present := av[k]; !present {
				diffs = append(diffs, fmt.Sprintf("%s.%s: ausente na fixture original, PRESENTE na saída real do marshaller", path, k))
			}
		}
		return diffs
	case []interface{}:
		bv, ok := b.([]interface{})
		if !ok {
			return []string{fmt.Sprintf("%s: era array no original, é %T na saída regenerada", path, b)}
		}
		if len(av) != len(bv) {
			return []string{fmt.Sprintf("%s: tamanho do array era %d no original, é %d na saída regenerada", path, len(av), len(bv))}
		}
		var diffs []string
		for i := range av {
			diffs = append(diffs, diffJSONValues(fmt.Sprintf("%s[%d]", path, i), av[i], bv[i])...)
		}
		return diffs
	default:
		if !reflect.DeepEqual(a, b) {
			return []string{fmt.Sprintf("%s: original=%#v, saída real do marshaller=%#v", path, a, b)}
		}
		return nil
	}
}
