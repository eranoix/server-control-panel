// uuid_map_test.go — tests for migration-uuid-map.json (the
// username ↔ email/supabase_uuid mapping). Covers the happy path, a missing
// file, an invalid schema_version, a malformed entry, and case-insensitive
// lookups.
package auth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeMapFile(t *testing.T, dir string, payload any) string {
	t.Helper()
	p := filepath.Join(dir, "migration-uuid-map.json")
	b, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return p
}

func TestLoadUUIDMap_FileMissing_NilNil(t *testing.T) {
	dir := t.TempDir()
	m, err := LoadUUIDMap(filepath.Join(dir, "does-not-exist.json"))
	if err != nil {
		t.Fatalf("expected nil error for missing file, got %v", err)
	}
	if m != nil {
		t.Fatalf("expected nil map for missing file, got %v", m)
	}
}

func TestLoadUUIDMap_SchemaVersionInvalid(t *testing.T) {
	dir := t.TempDir()
	path := writeMapFile(t, dir, map[string]any{
		"schema_version": 999,
		"mappings":       map[string]any{},
	})
	if _, err := LoadUUIDMap(path); err == nil {
		t.Fatal("expected schema_version error, got nil")
	}
}

func TestLoadUUIDMap_EmptyEmailRejected(t *testing.T) {
	dir := t.TempDir()
	path := writeMapFile(t, dir, map[string]any{
		"schema_version": 1,
		"mappings": map[string]any{
			"sam": map[string]any{
				"email":         "",
				"supabase_uuid": "abc",
			},
		},
	})
	if _, err := LoadUUIDMap(path); err == nil {
		t.Fatal("expected error for empty email, got nil")
	}
}

func TestLoadUUIDMap_Happy_LookupAndCase(t *testing.T) {
	dir := t.TempDir()
	path := writeMapFile(t, dir, map[string]any{
		"schema_version": 1,
		"mappings": map[string]any{
			"sam": map[string]any{
				"email":         "Sam@Northwind.example",
				"supabase_uuid": "cc18b0cd-b7b7-4c4d-90cb-7507af88370c",
			},
			"jordan": map[string]any{
				"email":         "j.lee@northwind.example",
				"supabase_uuid": "f232c06e-b9fc-4127-b304-34cf13424e6b",
			},
		},
	})
	m, err := LoadUUIDMap(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if m.Size() != 2 {
		t.Fatalf("expected size 2, got %d", m.Size())
	}
	email, uuid, ok := m.Lookup("sam")
	if !ok || uuid != "cc18b0cd-b7b7-4c4d-90cb-7507af88370c" || email != "Sam@Northwind.example" {
		t.Fatalf("Lookup(sam): got email=%q uuid=%q ok=%v", email, uuid, ok)
	}
	// Lookup is exact-match (case-sensitive on username).
	if _, _, ok := m.Lookup("ARTHUR"); ok {
		t.Fatal("Lookup should be case-sensitive on username")
	}
	// LookupByEmail is case-insensitive.
	uname, ok := m.LookupByEmail("SAM@northwind.example")
	if !ok || uname != "sam" {
		t.Fatalf("LookupByEmail mixed-case: got %q ok=%v", uname, ok)
	}
	// EmailFor preserves the original case.
	if got := m.EmailFor("sam"); got != "Sam@Northwind.example" {
		t.Fatalf("EmailFor: expected case preserved, got %q", got)
	}
	// A nil receiver does not panic.
	var nilMap *UUIDMap
	if _, _, ok := nilMap.Lookup("anyone"); ok {
		t.Fatal("nil UUIDMap.Lookup should return ok=false")
	}
	if nilMap.Size() != 0 {
		t.Fatal("nil UUIDMap.Size should be 0")
	}
}

func TestLoadUUIDMap_CorruptJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "migration-uuid-map.json")
	if err := os.WriteFile(path, []byte("not json {"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := LoadUUIDMap(path); err == nil {
		t.Fatal("expected parse error, got nil")
	}
}
