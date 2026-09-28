package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const defaultPath = "/opt/panel/data/config.json"

const maxBackups = 10

func Load() (*Config, error) {
	path := os.Getenv("PANEL_CONFIG")
	if path == "" {
		path = defaultPath
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		c, err := bootstrap(path)
		if err != nil {
			return nil, err
		}
		return c, nil
	}

	c, err := parseFile(path)
	if err == nil {
		applyDefaults(c, path)
		if c.SchemaVersion > CurrentSchemaVersion {
			return nil, fmt.Errorf(
				"config schema_version=%d is newer than this binary (current=%d); downgrade not supported",
				c.SchemaVersion, CurrentSchemaVersion,
			)
		}
		return c, nil
	}

	log.Printf("config: WARNING failed to parse %s: %v — trying latest .bak", path, err)
	bakPath, bakErr := latestBackup(path)
	if bakErr != nil {
		return nil, fmt.Errorf("config corrupt and no backup available: backup=%v: %w", bakErr, err)
	}
	bc, bakParseErr := parseFile(bakPath)
	if bakParseErr != nil {
		return nil, fmt.Errorf("config corrupt and backup %s also unparseable (live=%v): %w", bakPath, err, bakParseErr)
	}
	applyDefaults(bc, path)
	bc.LoadedFromBackup = true
	bc.LoadedBackupName = filepath.Base(bakPath)
	log.Printf("config: RECOVERED from %s — live config will be left untouched for manual inspection; new writes will replace it", bakPath)
	return bc, nil
}

func parseFile(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

func applyDefaults(c *Config, path string) {
	if c.Listen == "" {
		c.Listen = ":8765"
	}
	if c.DataDir == "" {
		c.DataDir = filepath.Dir(path)
	}
	if c.ClaudeHome == "" {
		c.ClaudeHome = "/root/.claude"
	}
	if c.PrivateAIURL == "" {
		c.PrivateAIURL = "http://127.0.0.1:8787"
	}
	if c.PrivateAIAdminTokenFile == "" {
		c.PrivateAIAdminTokenFile = "/etc/private-ai-api/env"
	}
	if c.AdGuardURL == "" {
		c.AdGuardURL = "http://127.0.0.1:3053"
	}
	if c.SingboxConfigPath == "" {
		c.SingboxConfigPath = "/opt/singbox/config.json"
	}
	if c.SingboxContainer == "" {
		c.SingboxContainer = "singbox"
	}
	if c.SingboxClashURL == "" {
		c.SingboxClashURL = "http://127.0.0.1:9095"
	}
	if c.SingboxDevicePortsPath == "" {
		c.SingboxDevicePortsPath = "/opt/singbox/.device-ports"
	}
	if c.DatasaverStateDir == "" {
		c.DatasaverStateDir = "/opt/datasaver/state"
	}
	if c.DatasaverCAPath == "" {
		c.DatasaverCAPath = "/opt/datasaver/ca/mitmproxy-ca-cert.pem"
	}
	if len(c.DatasaverContainers) == 0 {
		c.DatasaverContainers = []string{"datasaver-vps", "datasaver-home"}
	}
	if c.JWTSecretFile != "" && c.JWTSecret == "" {
		b, err := os.ReadFile(c.JWTSecretFile)
		if err != nil {
			log.Printf("config: WARNING failed to read jwt_secret_file=%s: %v", c.JWTSecretFile, err)
			return
		}
		c.JWTSecret = strings.TrimRight(string(b), "\n\r ")
	}
	if c.TactiqIntakeToken == "" {
		c.TactiqIntakeToken = randHex(32)
		log.Printf("config: generated tactiq_intake_token (save config to persist)")
	}
}

func latestBackup(path string) (string, error) {
	dir := filepath.Dir(path)
	base := filepath.Base(path) + ".bak."
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	type bak struct {
		path string
		ts   int64
	}
	var baks []bak
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if !strings.HasPrefix(n, base) {
			continue
		}
		tsStr := strings.TrimPrefix(n, base)
		ts, err := strconv.ParseInt(tsStr, 10, 64)
		if err != nil {
			continue
		}
		baks = append(baks, bak{path: filepath.Join(dir, n), ts: ts})
	}
	if len(baks) == 0 {
		return "", errors.New("no backup found")
	}
	sort.Slice(baks, func(i, j int) bool { return baks[i].ts > baks[j].ts })
	return baks[0].path, nil
}

func bootstrap(path string) (*Config, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	pass := randHex(12)
	hash, err := bcrypt.GenerateFromPassword([]byte(pass), 12)
	if err != nil {
		return nil, err
	}
	c := &Config{
		SchemaVersion: CurrentSchemaVersion,
		Listen:        ":8765",
		DataDir:       filepath.Dir(path),
		JWTSecret:     randHex(32),
		Username:      "admin",
		PasswordHash:  string(hash),
		ClaudeHome:    "/root/.claude",
	}
	if err := Save(c, path); err != nil {
		return nil, err
	}
	credPath := filepath.Join(filepath.Dir(path), "INITIAL_CREDENTIALS.txt")
	_ = os.WriteFile(credPath, []byte("username: admin\npassword: "+pass+"\n"), 0o600)
	return c, nil
}

func Save(c *Config, path string) error {
	dir := filepath.Dir(path)
	toSerialize := *c
	if toSerialize.JWTSecretFile != "" {
		toSerialize.JWTSecret = ""
	}
	b, err := json.MarshalIndent(&toSerialize, "", "  ")
	if err != nil {
		return err
	}

	if cur, err := os.ReadFile(path); err == nil {
		bakName := filepath.Base(path) + ".bak." + strconv.FormatInt(time.Now().Unix(), 10)
		bakPath := filepath.Join(dir, bakName)
		if err := os.WriteFile(bakPath, cur, 0o600); err != nil {
			return fmt.Errorf("config: backup write failed: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("config: backup read failed: %w", err)
	}

	tmpPath := path + ".new"
	if err := writeFileSync(tmpPath, b, 0o600); err != nil {
		return fmt.Errorf("config: write .new failed: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("config: rename failed: %w", err)
	}

	if df, err := os.Open(dir); err == nil {
		_ = df.Sync()
		_ = df.Close()
	}

	pruneBackups(path, maxBackups)
	return nil
}

func writeFileSync(path string, data []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func pruneBackups(path string, keep int) {
	dir := filepath.Dir(path)
	base := filepath.Base(path) + ".bak."
	entries, err := os.ReadDir(dir)
	if err != nil {
		log.Printf("pruneBackups: read %s failed: %v (backups will pile up)", dir, err)
		return
	}
	type bak struct {
		path string
		ts   int64
	}
	var baks []bak
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if !strings.HasPrefix(n, base) {
			continue
		}
		tsStr := strings.TrimPrefix(n, base)
		ts, err := strconv.ParseInt(tsStr, 10, 64)
		if err != nil {
			continue
		}
		baks = append(baks, bak{path: filepath.Join(dir, n), ts: ts})
	}
	if len(baks) <= keep {
		return
	}
	sort.Slice(baks, func(i, j int) bool { return baks[i].ts > baks[j].ts })
	for _, old := range baks[keep:] {
		if err := os.Remove(old.path); err != nil {
			log.Printf("pruneBackups: remove %s failed: %v", old.path, err)
		}
	}
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
