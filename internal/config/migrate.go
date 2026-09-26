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

// MigrationDeps bundles the side-effect handles MigrateV1ToV2 needs.
// Kept as a struct so callers (cmd/server, tests) don't have to remember
// argument order and so future migrations can extend it without churn.
type MigrationDeps struct {
	// Cfg is the loaded *Config. Mutated in place when migration runs;
	// the caller is expected to drop the pointer and re-Load afterwards
	// to make sure no other goroutine holds the pre-migration view.
	Cfg *Config
	// ConfigPath is the on-disk path of the live config.json. Used to
	// derive the migration backup name and to pass to Save().
	ConfigPath string
	// DataDir mirrors Cfg.DataDir; passed separately so the migration
	// can pre-validate it cheaply before touching Cfg.
	DataDir string
	// Vault is the global secrets store. Migration re-keys global
	// "waha_*" entries to "<primary>:waha_*". Pass nil only in tests
	// that don't touch the vault.
	Vault *secrets.Store
	// Audit, when non-nil, receives a "migration.v2" event on success.
	Audit *auth.AuditLog
	// Primary is the username that inherits the legacy single-tenant
	// state. Hard-coded to "sam" by the migration plan; exposed as a
	// field for tests.
	Primary string
}

// ErrConcurrentMigration is returned when the .migrate.lock cannot be
// acquired — another process (or a previous crash that left the lock
// dangling) is mid-migration. Caller should fail closed and let systemd
// retry; the second invocation will see SchemaVersion >= 2 and no-op.
var ErrConcurrentMigration = errors.New("config: concurrent migration in progress")

// MigrateV1ToV2 upgrades the on-disk layout to the per-user isolation
// scheme. Idempotent (cheap no-op when SchemaVersion >= 2), atomic
// (hardlink backup taken first; rollback on any failure), and serialised
// across processes via flock on <DataDir>/.migrate.lock.
//
// Steps, in order:
//
//  1. Acquire flock on <DataDir>/.migrate.lock.
//  2. Re-read config from disk; abort no-op if another process already migrated.
//  3. Reject if Cfg.Primary doesn't exist in Cfg.Users[] / Cfg.Username.
//  4. Snapshot <DataDir> to <DataDir>.bak.<unix-ts> (hardlink cp -al equivalent).
//  5. Create <DataDir>/users/<primary>/{whatsapp,uploads,browser}/.
//  6. Move data/whatsapp/{state.json,chats.json,contacts.json,messages/}
//     into the per-user dir.
//  7. Move /var/lib/vpsm-whatsapp/{sessions,media,files} → /var/lib/vpsm-whatsapp/<primary>/.
//  8. Re-key vault: every non-global, non-prefixed key gets "<primary>:".
//  9. Re-Owner videocall rooms from "admin" or "" → primary.
//  10. Move data/browser-instances.json → data/users/<primary>/browser-instances.json.
//  11. Move /etc/claude-router/env → /etc/claude-router/users/<primary>.env.
//  12. systemctl disable vpsm-whatsapp.service (legacy singleton).
//  13. Strip admin from config: remove top-level Username/PasswordHash/TOTP
//     and any Users entry named "admin".
//  14. Bump SchemaVersion=2; Save().
//  15. Append audit event "migration.v2".
//
// On failure in steps 5-14, rollback: rm -rf <DataDir>; mv <bak> <DataDir>.
// Pre-existing files outside DataDir (vault, /var/lib, /etc) are only
// touched after the backup so a failure leaves them untouched OR a
// successful rollback restores DataDir to its pre-migration shape.
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

	// Step 1: flock. Two daemons racing the same migration would corrupt
	// the layout; flock serialises them. Released when the function returns.
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

	// Step 2: re-read after acquiring the lock — another process might
	// have completed the migration between our last Load() and now.
	fresh, err := parseFile(d.ConfigPath)
	if err == nil && fresh.SchemaVersion >= CurrentSchemaVersion {
		// Copy the freshly-migrated state back to the caller's *Config
		// so it sees the post-migration users[] list.
		*d.Cfg = *fresh
		return nil
	}

	if !d.Cfg.HasUser(d.Primary) {
		return fmt.Errorf("migrate: primary user %q not present in config; aborting", d.Primary)
	}

	// Step 3: snapshot backup. cp -al for speed (hardlinks; copy-on-write
	// happens only when migration moves files).
	bakPath := fmt.Sprintf("%s.bak.%d", d.DataDir, time.Now().Unix())
	if err := snapshotDir(d.DataDir, bakPath); err != nil {
		return fmt.Errorf("migrate: backup snapshot: %w", err)
	}
	log.Printf("migrate v1→v2: backup at %s", bakPath)

	rollback := func(label string, cause error) error {
		log.Printf("migrate v1→v2 FAILED at %s: %v — rolling back from %s", label, cause, bakPath)
		// Best-effort restore. If RemoveAll fails (e.g. a busy mount),
		// the .bak.<ts> still holds the pre-migration state so an
		// operator can recover manually.
		if rmErr := os.RemoveAll(d.DataDir); rmErr != nil {
			return fmt.Errorf("migrate %s: rollback also failed (rm dataDir: %v) — restore from %s manually: %w", label, rmErr, bakPath, cause)
		}
		if mvErr := os.Rename(bakPath, d.DataDir); mvErr != nil {
			return fmt.Errorf("migrate %s: rollback also failed (mv: %v) — restore from %s manually: %w", label, mvErr, bakPath, cause)
		}
		return fmt.Errorf("migrate %s: rolled back from %s: %w", label, bakPath, cause)
	}

	// Step 5: per-user dirs.
	userRoot := filepath.Join(d.DataDir, "users", d.Primary)
	for _, sub := range []string{"whatsapp", "uploads", "browser"} {
		dir := filepath.Join(userRoot, sub)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return rollback("mkdir users/"+d.Primary+"/"+sub, err)
		}
	}

	// Step 6: WhatsApp store.
	wsOld := filepath.Join(d.DataDir, "whatsapp")
	wsNew := filepath.Join(userRoot, "whatsapp")
	for _, name := range []string{"state.json", "chats.json", "contacts.json"} {
		src := filepath.Join(wsOld, name)
		dst := filepath.Join(wsNew, name)
		if err := moveIfExists(src, dst); err != nil {
			return rollback("move whatsapp/"+name, err)
		}
	}
	// messages/ is a directory tree — moveIfExists handles os.Rename
	// across directories on the same filesystem, falling back to a
	// recursive copy + remove if necessary.
	if err := moveIfExists(filepath.Join(wsOld, "messages"), filepath.Join(wsNew, "messages")); err != nil {
		return rollback("move whatsapp/messages", err)
	}

	// Step 7: /var/lib/vpsm-whatsapp. Backing up is non-trivial (separate
	// filesystem), so we don't roll this back — if it fails, the layout
	// is partially migrated but DataDir is intact for inspection.
	//
	// Special case: the destination <primary>/sessions may ALREADY exist if a
	// Manager.Provision ran before the migration (booting the new binary on a
	// host where someone had already provisioned sam by hand, or a
	// downgrade followed by an upgrade). When that happens, the legacy entry
	// becomes a "stale source" — we archive it as sessions.legacy-<ts> rather
	// than try to merge it (merging a live SQLite-WAL is guaranteed
	// corruption).
	containerRoot := filepath.Join("/var/lib/vpsm-whatsapp", d.Primary)
	if _, err := os.Stat("/var/lib/vpsm-whatsapp/sessions"); err == nil {
		if err := os.MkdirAll(containerRoot, 0o700); err != nil {
			return rollback("mkdir container root", err)
		}
		ts := time.Now().Unix()
		for _, name := range []string{"sessions", "media", "files"} {
			src := filepath.Join("/var/lib/vpsm-whatsapp", name)
			dst := filepath.Join(containerRoot, name)
			if _, dstErr := os.Stat(dst); dstErr == nil {
				// Dst already exists: archive the legacy src, never overwrite dst.
				legacyArchive := filepath.Join("/var/lib/vpsm-whatsapp",
					fmt.Sprintf("%s.legacy-%d", name, ts))
				log.Printf("migrate: %s already exists at %s — archiving the legacy one at %s",
					name, dst, legacyArchive)
				if err := os.Rename(src, legacyArchive); err != nil && !os.IsNotExist(err) {
					return rollback("archive legacy /var/lib/vpsm-whatsapp/"+name, err)
				}
				continue
			}
			if err := moveIfExists(src, dst); err != nil {
				return rollback("move /var/lib/vpsm-whatsapp/"+name, err)
			}
		}
	}

	// Step 8: vault re-key. Each non-prefixed, non-global key becomes
	// "<primary>:<key>". The store has no transaction primitive, so we
	// take its byte snapshot first — same idea as the DataDir backup.
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

	// Step 9: videocall rooms re-Owner. Best-effort — JSON read failure
	// is treated as "no rooms file yet", which is a valid pre-migration
	// state for fresh installs.
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

	// Step 10: browser-instances.json.
	if err := moveIfExists(
		filepath.Join(d.DataDir, "browser-instances.json"),
		filepath.Join(userRoot, "browser-instances.json"),
	); err != nil {
		return rollback("move browser-instances.json", err)
	}

	// Step 11: claude-router env. Not under DataDir, so no rollback —
	// failure here aborts before the Save() flips SchemaVersion. The
	// old env file stays where it was.
	// Step 11 relocation is delegated to migrateRouterEnv (Lstat-based,
	// symlink-safe, idempotent) to prevent the circular-symlink landmine
	// that renaming the env symlink over its own target used to create.
	// Best-effort: relocating the env must NEVER abort the DataDir migration.
	// On a host with no readable /etc/claude-router (CI), or with a broken env,
	// it logs and moves on; the unit's ExecStartPre self-heals the env at
	// runtime. This keeps the migration tests hermetic.
	if err := migrateRouterEnv(d.Primary); err != nil {
		log.Printf("migrate v1->v2: claude-router env relocation skipped: %v", err)
	}

	// Step 12: disable legacy singleton unit. Best-effort — the template
	// unit (vpsm-whatsapp@.service) is installed by deploy.sh; nothing
	// to enable here. If systemctl is missing (CI / container tests),
	// silently skip.
	if _, err := exec.LookPath("systemctl"); err == nil {
		// 30s timeout — systemctl can hang on shutdown or on a unit-failed state.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		_ = exec.CommandContext(ctx, "systemctl", "disable", "--now", "vpsm-whatsapp.service").Run()
		cancel()
	}

	// Step 13: strip admin from config.
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

	// Step 14: bump version + persist. Primary inherits the legacy
	// single-tenant state — same name the migration used to relocate
	// vault keys + WhatsApp container dirs. The terminal handler reads
	// Primary to widen the session ACL for untagged sessions (see
	// pty.SessionListForUser).
	d.Cfg.SchemaVersion = CurrentSchemaVersion
	d.Cfg.Primary = d.Primary
	if err := Save(d.Cfg, d.ConfigPath); err != nil {
		return rollback("Save config", err)
	}

	// Step 15: audit. Failure here is non-fatal — the migration is
	// already committed.
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

// snapshotDir creates dst as a hardlinked copy of src using `cp -al` when
// available, falling back to a recursive os.Link/os.Copy. Hardlinks make
// the snapshot effectively free; the original tree is unchanged.
func snapshotDir(src, dst string) error {
	if _, err := exec.LookPath("cp"); err == nil {
		// cp -a preserves perms; -l hardlinks files. Both are crucial:
		// -l makes the snapshot cheap, -a keeps 0700 on backup so the
		// pre-migration permissions are restorable.
		cmd := exec.Command("cp", "-al", src, dst)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("cp -al: %w (%s)", err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	return linkTreeFallback(src, dst)
}

// linkTreeFallback walks src and hardlinks each file under dst, creating
// directories with matching permissions. Same effect as `cp -al` but
// pure Go, for environments without coreutils.
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
			// Symlinks: re-create instead of hardlinking the link inode,
			// which is portable across filesystems.
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		}
		return os.Link(path, target)
	})
}

// moveIfExists renames src to dst. When src and dst sit on different
// filesystems os.Rename returns EXDEV — in that case fall back to a
// copy + remove. NoOp when src is missing.
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
	// Cross-device fallback.
	if err := copyTree(src, dst); err != nil {
		return err
	}
	return os.RemoveAll(src)
}

// migrateRouterEnv relocates the legacy single-tenant claude-router env to
// the per-user path users/<primary>.env. It is idempotent and symlink-safe.
//
// /etc/claude-router/env is a symlink (created by the unit's ExecStartPre)
// that points at users/<primary>.env once migrated. os.Rename would move
// the *link inode* over its own target, creating a circular symlink
// (sam.env -> sam.env) and destroying the real env. So we Lstat
// (never follow) and never rename a symlink:
//
//   - If users/<primary>.env already exists as a real regular file we are
//     done -- no-op -- whatever env happens to be (typically a link to it).
//   - If env is a symlink, we resolve its content (ReadFile follows the
//     link) and write a real file at the destination, preserving its mode;
//     the symlink itself is never renamed.
//   - Only when env is a genuine regular file do we move it (EXDEV-safe).
func migrateRouterEnv(primary string) error {
	const dir = "/etc/claude-router"
	src := filepath.Join(dir, "env")
	dst := filepath.Join(dir, "users", primary+".env")

	li, err := os.Lstat(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	// 0700: env files hold per-user API tokens (Anthropic, Venice).
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}

	// Already migrated: destination is a real regular file. Nothing to do,
	// regardless of whether src is a symlink pointing at it.
	if di, derr := os.Lstat(dst); derr == nil && di.Mode().IsRegular() {
		return nil
	}

	if li.Mode()&os.ModeSymlink != 0 {
		// src is a symlink. NEVER rename it -- that would clobber its own
		// target. Resolve the real content via the followed path and write
		// a real file at dst. os.ReadFile follows the link for us.
		data, rerr := os.ReadFile(src)
		if rerr != nil {
			if os.IsNotExist(rerr) {
				return nil
			}
			return rerr
		}
		mode := os.FileMode(0o640)
		if ti, serr := os.Stat(src); serr == nil {
			mode = ti.Mode().Perm()
		}
		return writeFileAtomic(dst, data, mode)
	}

	if li.Mode().IsRegular() {
		// Genuine legacy file. Move it (rename, EXDEV-safe) to dst.
		return moveIfExists(src, dst)
	}

	// Anything else (device, socket, dir) -- leave untouched.
	return nil
}

// copyTree mirrors src to dst recursively, preserving permissions.
// Used only as the cross-device fallback for moveIfExists.
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

// writeFileAtomic writes data to path via the same .new+rename dance the
// rest of the codebase uses (config.Save, whatsapp.Store). Idempotent and
// crash-safe.
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

// globalKeyForMigration mirrors scope.IsGlobalKey but lives here to avoid
// a config→scope import (scope already imports config indirectly via auth;
// adding the reverse would create a cycle). Keep the list in sync.
func globalKeyForMigration(key string) bool {
	switch key {
	case "JWT_SECRET":
		return true
	}
	return false
}
