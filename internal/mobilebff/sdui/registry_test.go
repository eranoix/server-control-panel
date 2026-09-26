package sdui

import (
	"context"
	"errors"
	"testing"

	"server-control-panel/internal/config"
)

func testCfg() *config.Config {
	return &config.Config{
		SchemaVersion: config.CurrentSchemaVersion,
		Primary:       "admin1",
		Users: []config.User{
			{Username: "admin1", PasswordHash: "h"},
			{Username: "viewer1", PasswordHash: "h"},
		},
	}
}

// Test 1: Register then Build returns the builder's envelope, with
// SDUIVersion stamped by the registry regardless of what the builder set.
func TestRegistry_RegisterAndBuild_StampsVersion(t *testing.T) {
	Register("test.registry.basic", func(ctx context.Context, v Viewer) (*Envelope, error) {
		return &Envelope{
			SDUIVersion: 999, // a builder may not declare its own version
			Screen:      Screen{ID: "test.registry.basic", Title: "Basic"},
		}, nil
	})

	env, err := Build(context.Background(), "test.registry.basic", Viewer{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if env.SDUIVersion != CurrentSDUIVersion {
		t.Errorf("SDUIVersion = %d, want %d (the registry must stamp it, not the builder)", env.SDUIVersion, CurrentSDUIVersion)
	}
	if env.Screen.ID != "test.registry.basic" {
		t.Errorf("Screen.ID = %q, want %q", env.Screen.ID, "test.registry.basic")
	}
}

// Test 2: Build on an unregistered id returns ErrScreenNotFound.
func TestRegistry_Build_UnregisteredID(t *testing.T) {
	_, err := Build(context.Background(), "test.registry.does.not.exist", Viewer{})
	if !errors.Is(err, ErrScreenNotFound) {
		t.Errorf("err = %v, want ErrScreenNotFound", err)
	}
}

// Test 3: registering the same id twice panics at init time.
func TestRegistry_Register_DuplicatePanics(t *testing.T) {
	Register("test.registry.duplicate", func(ctx context.Context, v Viewer) (*Envelope, error) {
		return &Envelope{}, nil
	})

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("duplicate Register should have panicked")
		}
	}()
	Register("test.registry.duplicate", func(ctx context.Context, v Viewer) (*Envelope, error) {
		return &Envelope{}, nil
	})
}

// Test 4: ViewerFrom resolves admin status through the single existing RBAC
// check, defaulting to non-admin for unknown/empty identities.
func TestViewerFrom_ResolvesAdminStatus(t *testing.T) {
	cfg := testCfg()

	admin := ViewerFrom(cfg, "admin1")
	if !admin.IsAdmin() {
		t.Error("admin1 (primary) should be IsAdmin() == true")
	}

	nonAdmin := ViewerFrom(cfg, "viewer1")
	if nonAdmin.IsAdmin() {
		t.Error("viewer1 (non-admin) should be IsAdmin() == false")
	}

	empty := ViewerFrom(cfg, "")
	if empty.IsAdmin() {
		t.Error("empty username should be IsAdmin() == false (defensive default)")
	}
}

// Test 5: RegisteredScreens returns ids sorted, for deterministic
// enumeration.
func TestRegistry_RegisteredScreens_Sorted(t *testing.T) {
	Register("test.registry.zzz", func(ctx context.Context, v Viewer) (*Envelope, error) { return &Envelope{}, nil })
	Register("test.registry.aaa", func(ctx context.Context, v Viewer) (*Envelope, error) { return &Envelope{}, nil })

	ids := RegisteredScreens()
	foundAAA, foundZZZ := -1, -1
	for i, id := range ids {
		switch id {
		case "test.registry.aaa":
			foundAAA = i
		case "test.registry.zzz":
			foundZZZ = i
		}
	}
	if foundAAA == -1 || foundZZZ == -1 {
		t.Fatalf("RegisteredScreens() = %v, missing expected test ids", ids)
	}
	if foundAAA > foundZZZ {
		t.Errorf("RegisteredScreens() is not sorted: aaa at index %d, zzz at index %d", foundAAA, foundZZZ)
	}
	for i := 1; i < len(ids); i++ {
		if ids[i-1] > ids[i] {
			t.Errorf("RegisteredScreens() out of order at %d: %q > %q", i, ids[i-1], ids[i])
		}
	}
}
