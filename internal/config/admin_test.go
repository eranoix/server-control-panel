package config

import "testing"

// baseCfg is a v2-style config: sam is Primary (in Users[]), jordan is a
// plain user, matching the live install.
func baseCfg() *Config {
	return &Config{
		SchemaVersion: CurrentSchemaVersion,
		Primary:       "sam",
		Users: []User{
			{Username: "sam", PasswordHash: "h"},
			{Username: "jordan", PasswordHash: "h"},
		},
	}
}

func TestIsAdmin_PrimaryAndFlag(t *testing.T) {
	c := baseCfg()
	if !c.IsAdmin("sam") {
		t.Error("primary sam must be admin")
	}
	if c.IsAdmin("jordan") {
		t.Error("jordan is not admin before promotion")
	}
	if c.IsAdmin("") || c.IsAdmin("ghost") {
		t.Error("empty/unknown user must not be admin")
	}
	// Promote jordan via the flag.
	if err := c.SetAdmin("jordan", true); err != nil {
		t.Fatalf("SetAdmin(jordan,true): %v", err)
	}
	if !c.IsAdmin("jordan") {
		t.Error("jordan must be admin after promotion — parity with sam")
	}
	// Both are admins now.
	if !c.IsAdmin("sam") || !c.IsAdmin("jordan") {
		t.Error("both sam and jordan must be admins")
	}
}

func TestSetAdmin_PrimaryProtections(t *testing.T) {
	c := baseCfg()
	// Demoting the primary is rejected (never zero admins).
	if err := c.SetAdmin("sam", false); err == nil {
		t.Error("demoting the primary must error")
	}
	// Promoting the primary is a harmless no-op success.
	if err := c.SetAdmin("sam", true); err != nil {
		t.Errorf("promoting primary should be no-op success, got %v", err)
	}
	// Unknown user errors.
	if err := c.SetAdmin("ghost", true); err == nil {
		t.Error("SetAdmin on unknown user must error")
	}
	// Revoke works for a non-primary admin.
	_ = c.SetAdmin("jordan", true)
	if err := c.SetAdmin("jordan", false); err != nil {
		t.Errorf("revoking jordan: %v", err)
	}
	if c.IsAdmin("jordan") {
		t.Error("jordan should no longer be admin after revoke")
	}
}

func TestAdminsList(t *testing.T) {
	c := baseCfg()
	_ = c.SetAdmin("jordan", true)
	got := c.Admins()
	if len(got) != 2 || got[0] != "sam" || got[1] != "jordan" {
		t.Errorf("Admins() = %v, want [sam jordan] (primary first)", got)
	}
}

// Once a second admin exists, that admin must NOT be able to delete the
// primary — RemoveUser guards the v2 Primary binding (not just the legacy
// top-level Username).
func TestRemoveUser_ProtectsV2Primary(t *testing.T) {
	c := baseCfg()
	if err := c.RemoveUser("sam"); err == nil {
		t.Error("RemoveUser must reject deleting the v2 primary (cfg.Primary)")
	}
	if err := c.RemoveUser("jordan"); err != nil {
		t.Errorf("RemoveUser(jordan) should succeed: %v", err)
	}
}

// The Admin flag must round-trip through SetAdmin → it persists on the Users[]
// entry so config.Save serializes it.
func TestSetAdmin_PersistsOnEntry(t *testing.T) {
	c := baseCfg()
	_ = c.SetAdmin("jordan", true)
	for _, u := range c.Users {
		if u.Username == "jordan" && !u.Admin {
			t.Error("Admin flag not set on the jordan Users[] entry")
		}
	}
}
