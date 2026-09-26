package mobilebff

import (
	"testing"

	"server-control-panel/internal/config"
)

func capsTestCfg() *config.Config {
	return &config.Config{
		SchemaVersion: config.CurrentSchemaVersion,
		Primary:       "capsadmin",
		Users: []config.User{
			{Username: "capsadmin", PasswordHash: "h"},
			{Username: "capsuser", PasswordHash: "h"},
			{Username: "capsflaguser", PasswordHash: "h", Admin: true},
		},
	}
}

func TestCapabilitiesFor_PrimaryIsAdmin(t *testing.T) {
	cfg := capsTestCfg()
	isAdmin, caps := CapabilitiesFor(cfg, "capsadmin")
	if !isAdmin {
		t.Fatalf("isAdmin = false, want true for the Primary")
	}
	found := false
	for _, c := range caps {
		if c == AdminCapability {
			found = true
		}
	}
	if !found {
		t.Errorf("capabilities = %v, want it to contain %q", caps, AdminCapability)
	}
}

func TestCapabilitiesFor_AdminFlagUser(t *testing.T) {
	cfg := capsTestCfg()
	isAdmin, caps := CapabilitiesFor(cfg, "capsflaguser")
	if !isAdmin {
		t.Fatalf("isAdmin = false, want true for a user with Admin:true")
	}
	if len(caps) != 1 || caps[0] != AdminCapability {
		t.Errorf("capabilities = %v, want [%q]", caps, AdminCapability)
	}
}

func TestCapabilitiesFor_NonAdmin(t *testing.T) {
	cfg := capsTestCfg()
	isAdmin, caps := CapabilitiesFor(cfg, "capsuser")
	if isAdmin {
		t.Fatalf("isAdmin = true, want false for a regular user")
	}
	if len(caps) != 0 {
		t.Errorf("capabilities = %v, want empty", caps)
	}
}

func TestCapabilitiesFor_UnknownUsername_NeverAdmin(t *testing.T) {
	cfg := capsTestCfg()
	isAdmin, caps := CapabilitiesFor(cfg, "quemsabe")
	if isAdmin {
		t.Fatalf("isAdmin = true for an unknown username, want false")
	}
	if len(caps) != 0 {
		t.Errorf("capabilities = %v, want empty for an unknown username", caps)
	}
}

func TestCapabilitiesFor_NilConfig_NeverPanicsNeverAdmin(t *testing.T) {
	isAdmin, caps := CapabilitiesFor(nil, "capsadmin")
	if isAdmin {
		t.Fatalf("isAdmin = true with cfg nil, want false")
	}
	if len(caps) != 0 {
		t.Errorf("capabilities = %v, want empty with cfg nil", caps)
	}
}

func TestCapabilitiesFor_EmptyUsername_NeverAdmin(t *testing.T) {
	cfg := capsTestCfg()
	isAdmin, caps := CapabilitiesFor(cfg, "")
	if isAdmin {
		t.Fatalf("isAdmin = true with an empty username, want false")
	}
	if len(caps) != 0 {
		t.Errorf("capabilities = %v, want empty with an empty username", caps)
	}
}
