package config

// app_only_test.go — the app-only marker has to SURVIVE the write/read cycle
// of config.json.
//
// This is the most likely failure mode of the feature: Go rewrites the whole
// config.json from the struct (Save → json.Marshal), so a field the struct
// does not know about silently disappears on the next save (a password
// change, MFA, user CRUD) and the gate opens by itself — no error, no log,
// nobody the wiser until someone walks into the panel.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAppOnly_SurvivesSaveLoadCycle writes a config with the marker on,
// reloads it from disk and requires IsAppOnly to still be true.
func TestAppOnly_SurvivesSaveLoadCycle(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	cfg := &Config{
		SchemaVersion: CurrentSchemaVersion,
		Primary:       "sam",
		DataDir:       dir,
		JWTSecret:     "test-secret-not-used-in-prod",
		Users: []User{
			{Username: "sam"},
			{Username: "tester", Admin: true, AppOnly: true},
		},
	}
	if err := Save(cfg, path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	t.Setenv("PANEL_CONFIG", path)
	reloaded, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reloaded.IsAppOnly("tester") {
		t.Fatalf("the app-only flag vanished in the Save/Load cycle: %+v", reloaded.Users)
	}
	if reloaded.IsAppOnly("sam") {
		t.Fatalf("an account without the flag came back from disk flagged")
	}
	// Neighbours preserved — the marker must not have trampled another field.
	if !reloaded.IsAdmin("tester") {
		t.Fatalf("the admin flag was lost along with it")
	}

	// Second cycle: a Save of the ALREADY reloaded config (which is what
	// happens in production on every password/MFA change) must not lose the
	// marker either.
	path2 := filepath.Join(dir, "config2.json")
	if err := Save(reloaded, path2); err != nil {
		t.Fatalf("Save 2: %v", err)
	}
	t.Setenv("PANEL_CONFIG", path2)
	again, err := Load()
	if err != nil {
		t.Fatalf("Load 2: %v", err)
	}
	if !again.IsAppOnly("tester") {
		t.Fatalf("the flag vanished in the SECOND write cycle")
	}
}

// TestAppOnly_OmitEmptyKeepsConfigClean proves that anyone not using the
// feature does not get the field in the file — the config of a normal
// install stays the same.
func TestAppOnly_OmitEmptyKeepsConfigClean(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	cfg := &Config{
		SchemaVersion: CurrentSchemaVersion,
		Primary:       "sam",
		DataDir:       dir,
		JWTSecret:     "test-secret-not-used-in-prod",
		Users:         []User{{Username: "sam"}},
	}
	if err := Save(cfg, path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if strings.Contains(string(raw), "app_only") {
		t.Fatalf("app_only field written for an account without the flag:\n%s", raw)
	}

	// And the name of the field in the JSON is the contract with the
	// production config.json (which is hand-edited) — pin it here.
	marked := &Config{Users: []User{{Username: "tester", AppOnly: true}}}
	b, err := json.Marshal(marked)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(b), `"app_only":true`) {
		t.Fatalf(`expected "app_only":true in the serialized JSON, got %s`, b)
	}
}
