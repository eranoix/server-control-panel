package pty

import (
	"path/filepath"
	"testing"
)

// TestAssignOverwritesOwner checks that Assign overwrites the owner (unlike
// Claim, which refuses to steal) and persists.
func TestAssignOverwritesOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "own.json")
	o, err := LoadOwnership(path)
	if err != nil {
		t.Fatalf("LoadOwnership: %v", err)
	}
	if err := o.Claim("s", "sam"); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	// Claim would refuse (ErrOwnedByOther); Assign forces it.
	if err := o.Assign("s", "jordan"); err != nil {
		t.Fatalf("Assign: %v", err)
	}
	if got := o.Owner("s"); got != "jordan" {
		t.Errorf("owner after Assign = %q, want jordan", got)
	}
	// Persistence: it survives a reload.
	o2, err := LoadOwnership(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := o2.Owner("s"); got != "jordan" {
		t.Errorf("after reload owner = %q, want jordan", got)
	}
	// An empty target is a no-op (it neither erases nor corrupts).
	if err := o.Assign("s", ""); err != nil {
		t.Fatalf("Assign empty: %v", err)
	}
	if got := o.Owner("s"); got != "jordan" {
		t.Errorf("Assign empty target mutated owner to %q", got)
	}
}

// TestAudienceAllVisibleToEveryone: a "*" session is visible (and attachable) to
// any user, admin or not.
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
	// Someone else's private session stays invisible to a non-owner non-admin.
	if err := o.Assign("priv", "sam"); err != nil {
		t.Fatalf("Assign priv: %v", err)
	}
	if o.VisibleTo("priv", "rando", false) {
		t.Errorf("private session leaked to non-owner non-admin")
	}
}

// TestSessionsOfIgnoresAudienceAll: an "Everyone" session counts against nobody's
// quota.
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

// TestOwnsSessionManagementGate: the MANAGEMENT gate is stricter than
// visibility. An admin manages any session; a non-admin only their own — and does
// NOT manage an "Everyone" session even while seeing it in the picker.
func TestOwnsSessionManagementGate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "own.json")
	o, _ := LoadOwnership(path)
	_ = o.Assign("shared", AudienceAll)
	_ = o.Claim("sams", "sam")

	// Admin gerencia tudo.
	if !OwnsSession("jordan", "sams", true, o) {
		t.Errorf("admin should manage another's session")
	}
	if !OwnsSession("jordan", "shared", true, o) {
		t.Errorf("admin should manage a '*' session")
	}
	// Non-admin: only their own.
	if !OwnsSession("sam", "sams", false, o) {
		t.Errorf("non-admin should manage own session")
	}
	// A non-admin does NOT manage "Everyone" (but does see it — see below).
	if OwnsSession("rando", "shared", false, o) {
		t.Errorf("non-admin must NOT manage a '*' session")
	}
	// ...yet the "Everyone" session stays VISIBLE to the non-admin in the picker.
	if !o.VisibleTo("shared", "rando", false) {
		t.Errorf("'*' session should still be visible to non-admin in picker")
	}
	// A non-admin does not manage someone else's session.
	if OwnsSession("rando", "sams", false, o) {
		t.Errorf("non-admin must NOT manage another's session")
	}
}
