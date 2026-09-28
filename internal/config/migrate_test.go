package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/secrets"
)

func setupLegacyDataDir(t *testing.T) (dataDir, configPath string) {
	t.Helper()
	dataDir = t.TempDir()
	configPath = filepath.Join(dataDir, "config.json")

	c := &Config{
		Listen:       ":8765",
		DataDir:      dataDir,
		JWTSecret:    "test-secret",
		Username:     "admin",
		PasswordHash: "$2a$12$fakehash",
		ClaudeHome:   "/root/.claude",
		Users: []User{
			{Username: "sam", PasswordHash: "$2a$12$samhash", TOTPSecret: "ATOTP"},
			{Username: "jordan", PasswordHash: "$2a$12$cheyhash"},
		},
	}
	raw, _ := json.MarshalIndent(c, "", "  ")
	if err := os.WriteFile(configPath, raw, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	mustMkdir(t, filepath.Join(dataDir, "whatsapp"))
	mustWrite(t, filepath.Join(dataDir, "whatsapp", "state.json"), `{"Status":"WORKING"}`)
	mustWrite(t, filepath.Join(dataDir, "whatsapp", "chats.json"), `[]`)
	mustMkdir(t, filepath.Join(dataDir, "whatsapp", "messages", "chat1"))
	mustWrite(t, filepath.Join(dataDir, "whatsapp", "messages", "chat1", "2026-05.jsonl"),
		`{"id":"m1","body":"hi"}`+"\n")

	mustWrite(t, filepath.Join(dataDir, "browser-instances.json"),
		`{"ports":[5000]}`)

	mustMkdir(t, filepath.Join(dataDir, "videocalls"))
	mustWrite(t, filepath.Join(dataDir, "videocalls", "rooms.json"),
		`[{"id":"r1","name":"x","owner":"admin","members":["jordan"]},`+
			`{"id":"r2","name":"y","owner":""}]`)

	return dataDir, configPath
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", p, err)
	}
}

func mustWrite(t *testing.T, p, body string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
}

func loadCfg(t *testing.T, path string) *Config {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatalf("parse config: %v", err)
	}
	return &c
}

func openVault(t *testing.T, dataDir string) *secrets.Store {
	t.Helper()
	v, err := secrets.Open(filepath.Join(dataDir, "secrets.vault"), "test-pass")
	if err != nil {
		t.Fatalf("vault open: %v", err)
	}
	return v
}

func TestMigrateV1ToV2_HappyPath(t *testing.T) {
	dataDir, configPath := setupLegacyDataDir(t)
	cfg := loadCfg(t, configPath)
	vault := openVault(t, dataDir)
	if err := vault.Set("waha_api_key", "secret-A"); err != nil {
		t.Fatalf("vault set: %v", err)
	}
	if err := vault.Set("waha_hmac_secret", "secret-B"); err != nil {
		t.Fatalf("vault set: %v", err)
	}
	if err := vault.Set("JWT_SECRET", "global-jwt"); err != nil {
		t.Fatalf("vault set: %v", err)
	}

	if err := MigrateV1ToV2(MigrationDeps{
		Cfg:        cfg,
		ConfigPath: configPath,
		DataDir:    dataDir,
		Vault:      vault,
		Primary:    "sam",
	}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	if cfg.SchemaVersion != CurrentSchemaVersion {
		t.Fatalf("schema_version = %d, want %d", cfg.SchemaVersion, CurrentSchemaVersion)
	}
	if cfg.Username != "" || cfg.PasswordHash != "" {
		t.Fatalf("admin not stripped: %q / %q", cfg.Username, cfg.PasswordHash)
	}
	if cfg.HasUser("admin") {
		t.Fatalf("admin still in Users[]")
	}
	if !cfg.HasUser("sam") || !cfg.HasUser("jordan") {
		t.Fatalf("sam/jordan lost: %+v", cfg.Users)
	}

	disk := loadCfg(t, configPath)
	if disk.SchemaVersion != CurrentSchemaVersion {
		t.Fatalf("on-disk schema_version = %d, want %d", disk.SchemaVersion, CurrentSchemaVersion)
	}

	userRoot := filepath.Join(dataDir, "users", "sam")
	for _, sub := range []string{"whatsapp", "uploads", "browser"} {
		if _, err := os.Stat(filepath.Join(userRoot, sub)); err != nil {
			t.Fatalf("missing %s: %v", sub, err)
		}
	}
	if _, err := os.Stat(filepath.Join(userRoot, "whatsapp", "state.json")); err != nil {
		t.Fatalf("state.json not moved: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "whatsapp", "state.json")); !os.IsNotExist(err) {
		t.Fatalf("legacy state.json still present: %v", err)
	}
	if _, err := os.Stat(filepath.Join(userRoot, "whatsapp", "messages", "chat1", "2026-05.jsonl")); err != nil {
		t.Fatalf("messages tree not moved: %v", err)
	}
	if _, err := os.Stat(filepath.Join(userRoot, "browser-instances.json")); err != nil {
		t.Fatalf("browser-instances not moved: %v", err)
	}

	if v, ok := vault.Get("sam:waha_api_key"); !ok || v != "secret-A" {
		t.Fatalf("vault sam:waha_api_key = %q ok=%v, want secret-A", v, ok)
	}
	if v, ok := vault.Get("sam:waha_hmac_secret"); !ok || v != "secret-B" {
		t.Fatalf("vault sam:waha_hmac_secret = %q ok=%v, want secret-B", v, ok)
	}
	if _, ok := vault.Get("waha_api_key"); ok {
		t.Fatalf("unprefixed waha_api_key should be gone")
	}
	if v, ok := vault.Get("JWT_SECRET"); !ok || v != "global-jwt" {
		t.Fatalf("JWT_SECRET should remain global, got %q ok=%v", v, ok)
	}

	roomsRaw, _ := os.ReadFile(filepath.Join(dataDir, "videocalls", "rooms.json"))
	var rooms []map[string]any
	if err := json.Unmarshal(roomsRaw, &rooms); err != nil {
		t.Fatalf("rooms.json unmarshal: %v", err)
	}
	for _, r := range rooms {
		if r["owner"] != "sam" {
			t.Fatalf("room %v owner = %v, want sam", r["id"], r["owner"])
		}
	}

	entries, _ := os.ReadDir(filepath.Dir(dataDir))
	hasBak := false
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), filepath.Base(dataDir)+".bak.") {
			hasBak = true
		}
	}
	if !hasBak {
		t.Fatalf("backup dir not found")
	}
}

func TestMigrateV1ToV2_Idempotent(t *testing.T) {
	dataDir, configPath := setupLegacyDataDir(t)
	cfg := loadCfg(t, configPath)
	vault := openVault(t, dataDir)

	deps := MigrationDeps{
		Cfg: cfg, ConfigPath: configPath, DataDir: dataDir,
		Vault: vault, Primary: "sam",
	}
	if err := MigrateV1ToV2(deps); err != nil {
		t.Fatalf("first migrate: %v", err)
	}

	parentEntries, _ := os.ReadDir(filepath.Dir(dataDir))
	bakCount1 := 0
	for _, e := range parentEntries {
		if strings.HasPrefix(e.Name(), filepath.Base(dataDir)+".bak.") {
			bakCount1++
		}
	}

	if err := MigrateV1ToV2(deps); err != nil {
		t.Fatalf("second migrate: %v", err)
	}

	parentEntries, _ = os.ReadDir(filepath.Dir(dataDir))
	bakCount2 := 0
	for _, e := range parentEntries {
		if strings.HasPrefix(e.Name(), filepath.Base(dataDir)+".bak.") {
			bakCount2++
		}
	}
	if bakCount2 != bakCount1 {
		t.Fatalf("second migrate created another backup (had %d, now %d)", bakCount1, bakCount2)
	}
}

func TestMigrateV1ToV2_AbortOnMissingPrimary(t *testing.T) {
	dataDir, configPath := setupLegacyDataDir(t)
	cfg := loadCfg(t, configPath)
	vault := openVault(t, dataDir)

	err := MigrateV1ToV2(MigrationDeps{
		Cfg: cfg, ConfigPath: configPath, DataDir: dataDir,
		Vault: vault, Primary: "ghost",
	})
	if err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("expected abort on missing primary, got %v", err)
	}
	if cfg.SchemaVersion != 0 {
		t.Fatalf("schema bumped despite abort: %d", cfg.SchemaVersion)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "users", "ghost")); err == nil {
		t.Fatalf("users/ghost should not have been created")
	}
}

func TestMigrateV1ToV2_ConcurrentLock(t *testing.T) {
	dataDir, configPath := setupLegacyDataDir(t)
	vault := openVault(t, dataDir)

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cfg := loadCfg(t, configPath)
			errs[i] = MigrateV1ToV2(MigrationDeps{
				Cfg: cfg, ConfigPath: configPath, DataDir: dataDir,
				Vault: vault, Primary: "sam",
			})
		}(i)
	}
	wg.Wait()

	succeeded := 0
	for _, e := range errs {
		if e == nil {
			succeeded++
		} else if e != ErrConcurrentMigration {
			t.Fatalf("unexpected error: %v", e)
		}
	}
	if succeeded == 0 {
		t.Fatalf("both migrations failed: %v / %v", errs[0], errs[1])
	}

	disk := loadCfg(t, configPath)
	if disk.SchemaVersion != CurrentSchemaVersion {
		t.Fatalf("post-race schema_version = %d", disk.SchemaVersion)
	}
	if disk.Username != "" {
		t.Fatalf("post-race admin not stripped")
	}
}

func TestMigrateV1ToV2_AuditAppended(t *testing.T) {
	dataDir, configPath := setupLegacyDataDir(t)
	cfg := loadCfg(t, configPath)
	vault := openVault(t, dataDir)

	auditLog, err := auth.NewAuditLog(filepath.Join(dataDir, "audit.log"))
	if err != nil {
		t.Fatalf("audit: %v", err)
	}

	if err := MigrateV1ToV2(MigrationDeps{
		Cfg: cfg, ConfigPath: configPath, DataDir: dataDir,
		Vault: vault, Audit: auditLog, Primary: "sam",
	}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	tail := auditLog.Tail(10)
	found := false
	for _, e := range tail {
		if e.Action == "migration.v2" && e.User == "system" && e.Target == "sam" {
			found = true
		}
	}
	if !found {
		t.Fatalf("migration.v2 event not in audit tail: %+v", tail)
	}
}
