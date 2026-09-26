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

// TestAppOnly_SobreviveCicloSaveLoad writes a config with the marker on,
// reloads it from disk and requires IsAppOnly to still be true.
func TestAppOnly_SobreviveCicloSaveLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	cfg := &Config{
		SchemaVersion: CurrentSchemaVersion,
		Primary:       "sam",
		DataDir:       dir,
		JWTSecret:     "segredo-de-teste-nao-usado-em-prod",
		Users: []User{
			{Username: "sam"},
			{Username: "teste", Admin: true, AppOnly: true},
		},
	}
	if err := Save(cfg, path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	t.Setenv("VPSM_CONFIG", path)
	reloaded, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reloaded.IsAppOnly("teste") {
		t.Fatalf("the app-only flag vanished in the Save/Load cycle: %+v", reloaded.Users)
	}
	if reloaded.IsAppOnly("sam") {
		t.Fatalf("an account without the flag came back from disk flagged")
	}
	// Neighbours preserved — the marker must not have trampled another field.
	if !reloaded.IsAdmin("teste") {
		t.Fatalf("the admin flag was lost along with it")
	}

	// Second cycle: a Save of the ALREADY reloaded config (which is what
	// happens in production on every password/MFA change) must not lose the
	// marker either.
	path2 := filepath.Join(dir, "config2.json")
	if err := Save(reloaded, path2); err != nil {
		t.Fatalf("Save 2: %v", err)
	}
	t.Setenv("VPSM_CONFIG", path2)
	again, err := Load()
	if err != nil {
		t.Fatalf("Load 2: %v", err)
	}
	if !again.IsAppOnly("teste") {
		t.Fatalf("the flag vanished in the SECOND write cycle")
	}
}

// TestAppOnly_OmitEmptyNaoPoluiConfig proves that anyone not using the
// feature does not get the field in the file — the config of a normal
// install stays the same.
func TestAppOnly_OmitEmptyNaoPoluiConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	cfg := &Config{
		SchemaVersion: CurrentSchemaVersion,
		Primary:       "sam",
		DataDir:       dir,
		JWTSecret:     "segredo-de-teste-nao-usado-em-prod",
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
	marcado := &Config{Users: []User{{Username: "teste", AppOnly: true}}}
	b, err := json.Marshal(marcado)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(b), `"app_only":true`) {
		t.Fatalf(`esperava "app_only":true no JSON serializado, got %s`, b)
	}
}
