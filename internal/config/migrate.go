package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/secrets"
)

type MigrationDeps struct {
	Cfg        *Config
	ConfigPath string
	DataDir    string
	Vault      *secrets.Store
	Audit      *auth.AuditLog
	Primary    string
}

var ErrConcurrentMigration = errors.New("config: concurrent migration in progress")

func MigrateV1ToV2(d MigrationDeps) error {
	if d.Cfg == nil {
		return errors.New("config: migrate: nil Cfg")
	}
	if d.Cfg.SchemaVersion >= CurrentSchemaVersion {
		return nil
	}
	if d.Primary == "" {
		d.Primary = "sam"
	}

	lockPath := filepath.Join(d.DataDir, ".migrate.lock")
	lockF, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("migrate: open lock: %w", err)
	}
	defer lockF.Close()
	if err := syscall.Flock(int(lockF.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return ErrConcurrentMigration
	}
	defer syscall.Flock(int(lockF.Fd()), syscall.LOCK_UN)

	fresh, err := parseFile(d.ConfigPath)
	if err == nil && fresh.SchemaVersion >= CurrentSchemaVersion {
		*d.Cfg = *fresh
		return nil
	}

	if !d.Cfg.HasUser(d.Primary) {
		return fmt.Errorf("migrate: primary user %q not present in config; aborting", d.Primary)
	}

	bakPath := fmt.Sprintf("%s.bak.%d", d.DataDir, time.Now().Unix())
	if err := snapshotDir(d.DataDir, bakPath); err != nil {
		return fmt.Errorf("migrate: backup snapshot: %w", err)
	}
	log.Printf("migrate v1→v2: backup at %s", bakPath)

	rollback := func(label string, cause error) error {
		log.Printf("migrate v1→v2 FAILED at %s: %v — rolling back from %s", label, cause, bakPath)
		if rmErr := os.RemoveAll(d.DataDir); rmErr != nil {
			return fmt.Errorf("migrate %s: rollback also failed (rm dataDir: %v) — restore from %s manually: %w", label, rmErr, bakPath, cause)
		}
		if mvErr := os.Rename(bakPath, d.DataDir); mvErr != nil {
			return fmt.Errorf("migrate %s: rollback also failed (mv: %v) — restore from %s manually: %w", label, mvErr, bakPath, cause)
		}
		return fmt.Errorf("migrate %s: rolled back from %s: %w", label, bakPath, cause)
	}

	userRoot := filepath.Join(d.DataDir, "users", d.Primary)
	for _, sub := range []string{"whatsapp", "uploads", "browser"} {
		dir := filepath.Join(userRoot, sub)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return rollback("mkdir users/"+d.Primary+"/"+sub, err)
		}
	}

	wsOld := filepath.Join(d.DataDir, "whatsapp")
	wsNew := filepath.Join(userRoot, "whatsapp")
	for _, name := range []string{"state.json", "chats.json", "contacts.json"} {
		src := filepath.Join(wsOld, name)
		dst := filepath.Join(wsNew, name)
		if err := moveIfExists(src, dst); err != nil {
			return rollback("move whatsapp/"+name, err)
		}
	}
	if err := moveIfExists(filepath.Join(wsOld, "messages"), filepath.Join(wsNew, "messages")); err != nil {
		return rollback("move whatsapp/messages", err)
	}

	containerRoot := filepath.Join("/var/lib/panel-whatsapp", d.Primary)
	if _, err := os.Stat("/var/lib/panel-whatsapp/sessions"); err == nil {
		if err := os.MkdirAll(containerRoot, 0o700); err != nil {
			return rollback("mkdir container root", err)
		}
		ts := time.Now().Unix()
		for _, name := range []string{"sessions", "media", "files"} {
			src := filepath.Join("/var/lib/panel-whatsapp", name)
			dst := filepath.Join(containerRoot, name)
			if _, dstErr := os.Stat(dst); dstErr == nil {
				legacyArchive := filepath.Join("/var/lib/panel-whatsapp",
					fmt.Sprintf("%s.legacy-%d", name, ts))
				log.Printf("migrate: %s already exists at %s — archiving the legacy one at %s",
					name, dst, legacyArchive)
				if err := os.Rename(src, legacyArchive); err != nil && !os.IsNotExist(err) {
					return rollback("archive legacy /var/lib/panel-whatsapp/"+name, err)
				}
				continue
			}
			if err := moveIfExists(src, dst); err != nil {
				return rollback("move /var/lib/panel-whatsapp/"+name, err)
			}
		}
	}

	if d.Vault != nil {
		vaultBak := filepath.Join(d.DataDir, fmt.Sprintf("secrets.vault.bak.%d", time.Now().Unix()))
		if err := copyFile(filepath.Join(d.DataDir, "secrets.vault"), vaultBak); err != nil && !os.IsNotExist(err) {
			return rollback("vault backup", err)
		}
		for _, k := range d.Vault.List() {
			if strings.Contains(k, ":") {
				continue
			}
			if globalKeyForMigration(k) {
				continue
			}
			val, _ := d.Vault.Get(k)
			newKey := d.Primary + ":" + k
			if err := d.Vault.Set(newKey, val); err != nil {
				return rollback("vault Set "+newKey, err)
			}
			if err := d.Vault.Delete(k); err != nil {
				return rollback("vault Delete "+k, err)
			}
		}
	}

	roomsPath := filepath.Join(d.DataDir, "videocalls", "rooms.json")
	if raw, err := os.ReadFile(roomsPath); err == nil {
		var rooms []map[string]any
		if json.Unmarshal(raw, &rooms) == nil {
			changed := false
			for i := range rooms {
				owner, _ := rooms[i]["owner"].(string)
				if owner == "admin" || owner == "" {
					rooms[i]["owner"] = d.Primary
					changed = true
				}
			}
			if changed {
				out, err := json.MarshalIndent(rooms, "", "  ")
				if err != nil {
					return rollback("marshal rooms.json", err)
				}
				if err := writeFileAtomic(roomsPath, out, 0o600); err != nil {
					return rollback("write rooms.json", err)
				}
			}
		}
	}

	if err := moveIfExists(
		filepath.Join(d.DataDir, "browser-instances.json"),
		filepath.Join(userRoot, "browser-instances.json"),
	); err != nil {
		return rollback("move browser-instances.json", err)
	}

	if _, err := exec.LookPath("systemctl"); err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		_ = exec.CommandContext(ctx, "systemctl", "disable", "--now", "panel-whatsapp.service").Run()
		cancel()
	}

	if d.Cfg.Username == "admin" {
		d.Cfg.Username = ""
		d.Cfg.PasswordHash = ""
		d.Cfg.TOTPSecret = ""
		d.Cfg.RecoveryTOTPSecret = ""
	}
	filtered := d.Cfg.Users[:0]
	for _, u := range d.Cfg.Users {
		if u.Username == "admin" {
			continue
		}
		filtered = append(filtered, u)
	}
	d.Cfg.Users = filtered

	d.Cfg.SchemaVersion = CurrentSchemaVersion
	d.Cfg.Primary = d.Primary
	if err := Save(d.Cfg, d.ConfigPath); err != nil {
		return rollback("Save config", err)
	}

	if d.Audit != nil {
		d.Audit.Append(auth.Event{
			User:   "system",
			Action: "migration.v2",
			Target: d.Primary,
		})
	}

	log.Printf("migrate v1→v2: completed; primary=%s, backup at %s", d.Primary, bakPath)
	return nil
}

func snapshotDir(src, dst string) error {
	if _, err := exec.LookPath("cp"); err == nil {
		cmd := exec.Command("cp", "-al", src, dst)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("cp -al: %w (%s)", err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	return linkTreeFallback(src, dst)
}

func linkTreeFallback(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		}
		return os.Link(path, target)
	})
}

func moveIfExists(src, dst string) error {
	if _, err := os.Stat(src); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	if err := os.Rename(src, dst); err == nil {
		return nil
	} else if !errors.Is(err, syscall.EXDEV) {
		return err
	}
	if err := copyTree(src, dst); err != nil {
		return err
	}
	return os.RemoveAll(src)
}

func copyTree(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		return copyFile(path, target)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp := path + ".new"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	if df, err := os.Open(dir); err == nil {
		_ = df.Sync()
		_ = df.Close()
	}
	return nil
}

func globalKeyForMigration(key string) bool {
	switch key {
	case "JWT_SECRET":
		return true
	}
	return false
}
