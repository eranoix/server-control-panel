package pty

import (
	"path/filepath"
	"testing"
)

// An OLDER snapshot (a lower seq) arriving AFTER a newer one must not overwrite
// the disk — otherwise a session "loses" its owner after a restart.
func TestOwnershipPersistOrdering(t *testing.T) {
	p := filepath.Join(t.TempDir(), "own.json")
	o, err := LoadOwnership(p)
	if err != nil {
		t.Fatal(err)
	}
	// Persist the NEW state (seq=2) and then an OLD state (seq=1).
	if err := o.persist(map[string]string{"a": "u1", "b": "u2"}, 2); err != nil {
		t.Fatal(err)
	}
	if err := o.persist(map[string]string{"a": "u1"}, 1); err != nil {
		t.Fatal(err)
	}
	reloaded, err := LoadOwnership(p)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Owner("b") != "u2" {
		t.Errorf("old snapshot (seq=1) overwrote the new one (seq=2): b=%q, wanted u2", reloaded.Owner("b"))
	}
}

func TestRegistryPersistOrdering(t *testing.T) {
	p := filepath.Join(t.TempDir(), "reg.json")
	r, err := LoadRegistry(p)
	if err != nil {
		t.Fatal(err)
	}
	newer := map[string]SessionRecord{
		"a": {Name: "a", Backend: "dtach"},
		"b": {Name: "b", Backend: "dtach"},
	}
	older := map[string]SessionRecord{"a": {Name: "a", Backend: "dtach"}}
	if err := r.persist(newer, 2); err != nil {
		t.Fatal(err)
	}
	if err := r.persist(older, 1); err != nil {
		t.Fatal(err)
	}
	reloaded, err := LoadRegistry(p)
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.Has("b") {
		t.Errorf("record 'b' disappeared: old snapshot (seq=1) overwrote the new one (seq=2)")
	}
}
