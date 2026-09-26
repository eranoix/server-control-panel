// wad.go — vpsmctl whatsapp wad-migrate/wad-rollback: cut a user over from the
// paywalled WAHA backend to the free whatsmeow daemon (cmd/wad), and back.
//
// The cutover is done here (not by the agent) because it must read the user's
// waha_hmac_secret from the vault — the daemon pushes inbound events to the
// server's existing webhook signed with that exact secret, so server-side
// validation keeps working unchanged.
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

const wadStateRoot = "/var/lib/vpsm-wad"

func wahaGowsDB(user string) string {
	return filepath.Join("/var/lib/vpsm-whatsapp", user, "sessions/gows/default/gows.db")
}

// whatsappWadMigrate performs the per-user cutover: stop WAHA (frees the device
// + quiesces gows.db), copy the paired session into the daemon's own dir, write
// meta.json (hmac from vault + a fresh api key) and the `enabled` flag, then
// restart the daemon so it loads the user. The running server picks up the flag
// and routes the user through the daemon on its next Service rebuild (restart
// vps-manager to apply immediately).
func whatsappWadMigrate(args []string) error {
	if len(args) < 1 || args[0] == "" {
		return fmt.Errorf("usage: vpsmctl whatsapp wad-migrate <user>")
	}
	user := args[0]
	u, err := scope.New(user)
	if err != nil {
		return fmt.Errorf("invalid user %q: %w", user, err)
	}

	// 1) read the per-user webhook HMAC from the vault.
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

	// 2) stop WAHA for this user — one device, one connection. Frees the device
	//    for the daemon and quiesces the sqlite WAL before we copy it.
	fmt.Printf("→ making sure user %s's WAHA is stopped (best-effort; WAHA was removed)...\n", user)
	// best-effort: WAHA has been removed; if a unit still exists, stop+mask it.
	_, _ = exec.Command("systemctl", "stop", "vpsm-whatsapp@"+user+".service").CombinedOutput()
	_, _ = exec.Command("systemctl", "mask", "vpsm-whatsapp@"+user+".service").CombinedOutput()

	// 3) copy the paired session into the daemon's per-user dir.
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

	// 4) write meta.json (hmac from vault + fresh api key).
	apiKey, err := randomHex(24)
	if err != nil {
		return err
	}
	meta, _ := json.Marshal(map[string]string{"hmac_secret": hmacSec, "api_key": apiKey})
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), meta, 0o600); err != nil {
		return fmt.Errorf("write meta.json: %w", err)
	}
	// 5) enable flag (server's buildService routes this user to the daemon).
	if err := os.WriteFile(filepath.Join(dir, "enabled"), []byte("1\n"), 0o600); err != nil {
		return fmt.Errorf("write enabled flag: %w", err)
	}

	// 6) restart the daemon so it loads this user (rescans state dir on boot).
	fmt.Println("→ restarting the vpsm-wad daemon...")
	if out, err := exec.Command("systemctl", "restart", "vpsm-wad.service").CombinedOutput(); err != nil {
		return fmt.Errorf("restart vpsm-wad (install the unit first?): %w: %s", err, out)
	}

	fmt.Printf("\n✅ %s migrated to the whatsmeow daemon.\n", user)
	fmt.Println("   Apply on the server now:    systemctl restart vps-manager")
	fmt.Println("   Rollback:                   vpsmctl whatsapp wad-rollback " + user)
	return nil
}

// whatsappWadRollback reverts a user to WAHA: drop the flag, restart the daemon
// (drops the session), restart WAHA on the original (untouched) gows.db.
func whatsappWadRollback(args []string) error {
	if len(args) < 1 || args[0] == "" {
		return fmt.Errorf("usage: vpsmctl whatsapp wad-rollback <user>")
	}
	user := args[0]
	dir := filepath.Join(wadStateRoot, user)
	_ = os.Remove(filepath.Join(dir, "enabled"))
	fmt.Printf("→ flag removed; restarting the daemon and turning WAHA back on for %s...\n", user)
	_, _ = exec.Command("systemctl", "restart", "vpsm-wad.service").CombinedOutput()
	// Re-enable the unit (the migration had disabled it) and bring the container up.
	_, _ = exec.Command("systemctl", "enable", "vpsm-whatsapp@"+user+".service").CombinedOutput()
	if out, err := exec.Command("systemctl", "start", "vpsm-whatsapp@"+user+".service").CombinedOutput(); err != nil {
		return fmt.Errorf("start WAHA: %w: %s", err, out)
	}
	fmt.Printf("\n✅ %s reverted to WAHA (original gows.db intact).\n", user)
	fmt.Println("   Apply on the server:  systemctl restart vps-manager")
	return nil
}

// whatsappWadFixMeta re-syncs the daemon's per-user meta.json `hmac_secret` to
// the CANONICAL value in the server's vault, then reloads only that user's
// session in the daemon. Fixes the failure mode where the daemon signs live
// webhook pushes with a stale secret (e.g. meta.json clobbered by a non-
// canonical worktree) → server 401s every push → whatsmeow never re-delivers →
// messages silently lost (only manual "full sync" works, since that path is a
// server→daemon pull that doesn't use the webhook HMAC).
//
// Surgical: rewrites ONLY hmac_secret (api_key and session.db untouched → no
// re-pair), and reconnects just this user via the daemon's /reload endpoint.
func whatsappWadFixMeta(args []string) error {
	if len(args) < 1 || args[0] == "" {
		return fmt.Errorf("usage: vpsmctl whatsapp wad-fixmeta <user>")
	}
	user := args[0]
	if _, err := scope.New(user); err != nil {
		return fmt.Errorf("invalid user %q: %w", user, err)
	}

	// 1) canonical secret from the server's vault (opened exactly as the server does).
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

	// 2) rewrite ONLY hmac_secret in meta.json (preserve api_key + any other field).
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

	// 3) reload just this user's session in the daemon (re-reads meta.json,
	//    reconnects; session.db untouched). Best-effort via the loopback API.
	if apiKey := meta["api_key"]; apiKey != "" {
		req, _ := http.NewRequest(http.MethodPost, "http://127.0.0.1:8769/u/"+user+"/reload", bytes.NewReader(nil))
		req.Header.Set("Authorization", "Bearer "+apiKey)
		if resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req); err != nil {
			fmt.Printf("→ daemon reload failed (%v); restart it by hand: systemctl restart vpsm-wad\n", err)
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
