package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
	if !reloaded.IsAdmin("tester") {
		t.Fatalf("the admin flag was lost along with it")
	}

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

	marked := &Config{Users: []User{{Username: "tester", AppOnly: true}}}
	b, err := json.Marshal(marked)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(b), `"app_only":true`) {
		t.Fatalf(`expected "app_only":true in the serialized JSON, got %s`, b)
	}
}
