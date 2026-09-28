package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"server-control-panel/internal/config"
	"server-control-panel/internal/scope"
	"server-control-panel/internal/secrets"
)

const wadStateRoot = "/var/lib/panel-wad"

func wahaGowsDB(user string) string {
	return filepath.Join("/var/lib/panel-whatsapp", user, "sessions/gows/default/gows.db")
}

func whatsappWadMigrate(args []string) error {
	if len(args) < 1 || args[0] == "" {
		return fmt.Errorf("usage: panelctl whatsapp wad-migrate <user>")
	}
	user := args[0]
	u, err := scope.New(user)
	if err != nil {
		return fmt.Errorf("invalid user %q: %w", user, err)
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if cfg.JWTSecret == "" {
		return fmt.Errorf("config.JWTSecret empty — refusing to open vault")
	}
	vault, err := secrets.Open(filepath.Join(cfg.DataDir, "secrets.vault"), cfg.JWTSecret)
	if err != nil {
		return fmt.Errorf("open vault: %w", err)
	}
	uv := scope.NewUserVault(vault, u)
	hmacSec, ok := uv.Get("waha_hmac_secret")
	if !ok || hmacSec == "" {
		return fmt.Errorf("user %s has no waha_hmac_secret in vault (provision WhatsApp first)", user)
	}

	src := wahaGowsDB(user)
	if _, err := os.Stat(src); err != nil {
		return fmt.Errorf("gows.db not found at %s: %w", src, err)
	}

	fmt.Printf("→ making sure user %s's WAHA is stopped (best-effort; WAHA was removed)...\n", user)
	_, _ = exec.Command("systemctl", "stop", "panel-whatsapp@"+user+".service").CombinedOutput()
	_, _ = exec.Command("systemctl", "mask", "panel-whatsapp@"+user+".service").CombinedOutput()

	dir := filepath.Join(wadStateRoot, user)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	dst := filepath.Join(dir, "session.db")
	if err := copyFile(src, dst); err != nil {
		return fmt.Errorf("copy session: %w", err)
	}
	for _, ext := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(src + ext); err == nil {
			_ = copyFile(src+ext, dst+ext)
		}
	}
	fmt.Printf("→ session copied to %s\n", dst)

	apiKey, err := randomHex(24)
	if err != nil {
		return err
	}
	meta, _ := json.Marshal(map[string]string{"hmac_secret": hmacSec, "api_key": apiKey})
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), meta, 0o600); err != nil {
		return fmt.Errorf("write meta.json: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "enabled"), []byte("1\n"), 0o600); err != nil {
		return fmt.Errorf("write enabled flag: %w", err)
	}

	fmt.Println("→ restarting the panel-wad daemon...")
	if out, err := exec.Command("systemctl", "restart", "panel-wad.service").CombinedOutput(); err != nil {
		return fmt.Errorf("restart panel-wad (install the unit first?): %w: %s", err, out)
	}

	fmt.Printf("\n✅ %s migrated to the whatsmeow daemon.\n", user)
	fmt.Println("   Apply on the server now:    systemctl restart server-control-panel")
	fmt.Println("   Rollback:                   panelctl whatsapp wad-rollback " + user)
	return nil
}

func whatsappWadRollback(args []string) error {
	if len(args) < 1 || args[0] == "" {
		return fmt.Errorf("usage: panelctl whatsapp wad-rollback <user>")
	}
	user := args[0]
	dir := filepath.Join(wadStateRoot, user)
	_ = os.Remove(filepath.Join(dir, "enabled"))
	fmt.Printf("→ flag removed; restarting the daemon and turning WAHA back on for %s...\n", user)
	_, _ = exec.Command("systemctl", "restart", "panel-wad.service").CombinedOutput()
	_, _ = exec.Command("systemctl", "enable", "panel-whatsapp@"+user+".service").CombinedOutput()
	if out, err := exec.Command("systemctl", "start", "panel-whatsapp@"+user+".service").CombinedOutput(); err != nil {
		return fmt.Errorf("start WAHA: %w: %s", err, out)
	}
	fmt.Printf("\n✅ %s reverted to WAHA (original gows.db intact).\n", user)
	fmt.Println("   Apply on the server:  systemctl restart server-control-panel")
	return nil
}

func whatsappWadFixMeta(args []string) error {
	if len(args) < 1 || args[0] == "" {
		return fmt.Errorf("usage: panelctl whatsapp wad-fixmeta <user>")
	}
	user := args[0]
	if _, err := scope.New(user); err != nil {
		return fmt.Errorf("invalid user %q: %w", user, err)
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if cfg.JWTSecret == "" {
		return fmt.Errorf("config.JWTSecret empty — refusing to open vault")
	}
	vault, err := secrets.Open(filepath.Join(cfg.DataDir, "secrets.vault"), cfg.JWTSecret)
	if err != nil {
		return fmt.Errorf("open vault: %w", err)
	}
	u, _ := scope.New(user)
	uv := scope.NewUserVault(vault, u)
	hmacSec, ok := uv.Get("waha_hmac_secret")
	if !ok || hmacSec == "" {
		return fmt.Errorf("user %s has no waha_hmac_secret in vault", user)
	}

	metaPath := filepath.Join(wadStateRoot, user, "meta.json")
	raw, err := os.ReadFile(metaPath)
	if err != nil {
		return fmt.Errorf("read meta.json (migrate first?): %w", err)
	}
	var meta map[string]string
	if err := json.Unmarshal(raw, &meta); err != nil {
		return fmt.Errorf("parse meta.json: %w", err)
	}
	already := meta["hmac_secret"] == hmacSec
	meta["hmac_secret"] = hmacSec
	out, _ := json.Marshal(meta)
	tmp := metaPath + ".tmp"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return fmt.Errorf("write meta.json: %w", err)
	}
	if err := os.Rename(tmp, metaPath); err != nil {
		return fmt.Errorf("rename meta.json: %w", err)
	}
	if already {
		fmt.Printf("→ %s: meta.json was already correct (no drift).\n", user)
	} else {
		fmt.Printf("→ %s: hmac_secret re-synced with the vault.\n", user)
	}

	if apiKey := meta["api_key"]; apiKey != "" {
		req, _ := http.NewRequest(http.MethodPost, "http://127.0.0.1:8769/u/"+user+"/reload", bytes.NewReader(nil))
		req.Header.Set("Authorization", "Bearer "+apiKey)
		if resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req); err != nil {
			fmt.Printf("→ daemon reload failed (%v); restart it by hand: systemctl restart panel-wad\n", err)
		} else {
			resp.Body.Close()
			fmt.Printf("→ session reloaded in the daemon (HTTP %d).\n", resp.StatusCode)
		}
	}
	fmt.Printf("\n✅ %s: live push restored. Check it: a new message should show up without hitting 'Sync'.\n", user)
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
