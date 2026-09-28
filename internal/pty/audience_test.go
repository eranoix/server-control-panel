package pty

import (
	"path/filepath"
	"testing"
)

func TestAssignOverwritesOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "own.json")
	o, err := LoadOwnership(path)
	if err != nil {
		t.Fatalf("LoadOwnership: %v", err)
	}
	if err := o.Claim("s", "sam"); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if err := o.Assign("s", "jordan"); err != nil {
		t.Fatalf("Assign: %v", err)
	}
	if got := o.Owner("s"); got != "jordan" {
		t.Errorf("owner after Assign = %q, want jordan", got)
	}
	o2, err := LoadOwnership(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := o2.Owner("s"); got != "jordan" {
		t.Errorf("after reload owner = %q, want jordan", got)
	}
	if err := o.Assign("s", ""); err != nil {
		t.Fatalf("Assign empty: %v", err)
	}
	if got := o.Owner("s"); got != "jordan" {
		t.Errorf("Assign empty target mutated owner to %q", got)
	}
}

func TestAudienceAllVisibleToEveryone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "own.json")
	o, _ := LoadOwnership(path)
	if err := o.Assign("shared", AudienceAll); err != nil {
		t.Fatalf("Assign: %v", err)
	}
	for _, tc := range []struct {
		user    string
		primary bool
	}{
		{"sam", true},
		{"jordan", true},
		{"rando", false},
	} {
		if !o.VisibleTo("shared", tc.user, tc.primary) {
			t.Errorf("VisibleTo(shared, %q, primary=%v) = false, want true", tc.user, tc.primary)
		}
	}
	if err := o.Assign("priv", "sam"); err != nil {
		t.Fatalf("Assign priv: %v", err)
	}
	if o.VisibleTo("priv", "rando", false) {
		t.Errorf("private session leaked to non-owner non-admin")
	}
}

func TestSessionsOfIgnoresAudienceAll(t *testing.T) {
	path := filepath.Join(t.TempDir(), "own.json")
	o, _ := LoadOwnership(path)
	_ = o.Claim("a1", "sam")
	_ = o.Claim("a2", "sam")
	_ = o.Assign("shared", AudienceAll)
	got := o.SessionsOf("sam")
	if len(got) != 2 {
		t.Errorf("SessionsOf(sam) = %v (len %d), want 2 (a1,a2; not the '*' one)", got, len(got))
	}
	if n := len(o.SessionsOf("*")); n != 0 {
		t.Errorf("SessionsOf('*') = %d, want 0 (sentinel is nobody's session)", n)
	}
}

func TestOwnsSessionManagementGate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "own.json")
	o, _ := LoadOwnership(path)
	_ = o.Assign("shared", AudienceAll)
	_ = o.Claim("sams", "sam")

	if !OwnsSession("jordan", "sams", true, o) {
		t.Errorf("admin should manage another's session")
	}
	if !OwnsSession("jordan", "shared", true, o) {
		t.Errorf("admin should manage a '*' session")
	}
	if !OwnsSession("sam", "sams", false, o) {
		t.Errorf("non-admin should manage own session")
	}
	if OwnsSession("rando", "shared", false, o) {
		t.Errorf("non-admin must NOT manage a '*' session")
	}
	if !o.VisibleTo("shared", "rando", false) {
		t.Errorf("'*' session should still be visible to non-admin in picker")
	}
	if OwnsSession("rando", "sams", false, o) {
		t.Errorf("non-admin must NOT manage another's session")
	}
}
