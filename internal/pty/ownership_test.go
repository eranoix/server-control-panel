package pty

import (
	"path/filepath"
	"testing"
)

// TestOwnershipRename checks that Rename carries the ownership over to the new
// name, removes the old key and persists (surviving a reload).
func TestOwnershipRename(t *testing.T) {
	path := filepath.Join(t.TempDir(), "own.json")
	o, err := LoadOwnership(path)
	if err != nil {
		t.Fatalf("LoadOwnership: %v", err)
	}
	if err := o.Claim("old-name", "sam"); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if err := o.Rename("old-name", "new-name"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if o.Owner("old-name") != "" {
		t.Errorf("old name still owned: %q", o.Owner("old-name"))
	}
	if got := o.Owner("new-name"); got != "sam" {
		t.Errorf("new name owner = %q, want sam", got)
	}
	// Persistence: reload from disk and confirm.
	o2, err := LoadOwnership(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := o2.Owner("new-name"); got != "sam" {
		t.Errorf("after reload owner = %q, want sam", got)
	}

	// Renaming a key that does not exist is a no-op (it creates no entry).
	if err := o.Rename("ghost", "phantom"); err != nil {
		t.Fatalf("Rename ghost: %v", err)
	}
	if o.Owner("phantom") != "" {
		t.Errorf("Rename of missing key created an entry")
	}
}
