package main

// Backup/restore of sessions from the CLI, reusing EXACTLY the same code
// (internal/pty) and the same file format/location the backend and the web UI
// use (<DataDir>/users/<user>/session-backups/<id>.json). That way a backup
// taken here (by the code-server vpsm-sessoes extension) is 100% interchangeable
// with the site's sessions page — no duplicated logic, no risk of divergence.

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"

	"server-control-panel/internal/config"
	ptysvc "server-control-panel/internal/pty"
)

// id = UnixNano (digits only); user = a safe identifier. Both end up inside
// file paths — validating them cuts off path traversal (../).
var sessionBackupIDRe = regexp.MustCompile(`^[0-9]{1,25}$`)
var usuarioValidoRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func sessionBackupsDir(dataDir, user string) string {
	return filepath.Join(dataDir, "users", user, "session-backups")
}

// cmdSessionBackup: vpsmctl session-backup <session> [--user U] [--no-scrollback]
// Snapshot of the session -> writes a Backup{Source:"manual"}. Prints the id on stdout.
func cmdSessionBackup(args []string) error {
	fs := flag.NewFlagSet("session-backup", flag.ContinueOnError)
	user := fs.String("user", "sam", "user who owns the backup")
	noSB := fs.Bool("no-scrollback", false, "do not capture scrollback")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) < 1 {
		return fmt.Errorf("usage: vpsmctl session-backup <session> [--user U] [--no-scrollback]")
	}
	if !usuarioValidoRe.MatchString(*user) {
		return fmt.Errorf("invalid user")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	sbLines := ptysvc.DefaultScrollbackLines
	if *noSB {
		sbLines = 0
	}
	snap, err := ptysvc.SnapshotSession(rest[0], sbLines)
	if err != nil {
		return fmt.Errorf("snapshot of %q: %w", rest[0], err)
	}
	if len(snap.Windows) == 0 {
		return fmt.Errorf("session %q does not exist or is empty", rest[0])
	}
	bk := ptysvc.Backup{
		ID:       strconv.FormatInt(time.Now().UnixNano(), 10),
		Created:  time.Now().Unix(),
		Source:   "manual",
		Sessions: []ptysvc.SessionSnapshot{snap},
	}
	dir := sessionBackupsDir(cfg.DataDir, *user)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(bk)
	if err != nil {
		return err
	}
	// atomic write (temp+rename) — the same pattern as the backend writeSessionBackup.
	path := filepath.Join(dir, bk.ID+".json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	fmt.Println(bk.ID)
	return nil
}

// cmdSessionRestore: vpsmctl session-restore <id> [--user U] [--as NAME]
// Reads the backup and recreates the sessions (RestoreSession = the same logic
// as the site: layout+cwd+scrollback replay, via systemd-run --scope = it
// survives a restart). With --as, it restores the 1st session under another
// name. It records ownership.
func cmdSessionRestore(args []string) error {
	fs := flag.NewFlagSet("session-restore", flag.ContinueOnError)
	user := fs.String("user", "sam", "user who owns the restored session")
	as := fs.String("as", "", "restore under this name (renames)")
	sess := fs.String("session", "", "restore only this session from the backup (multi-session)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) < 1 {
		return fmt.Errorf("usage: vpsmctl session-restore <id> [--user U] [--as NAME] [--session NAME]")
	}
	id := rest[0]
	if !sessionBackupIDRe.MatchString(id) {
		return fmt.Errorf("invalid backup id")
	}
	if !usuarioValidoRe.MatchString(*user) {
		return fmt.Errorf("invalid user")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	dir := sessionBackupsDir(cfg.DataDir, *user)
	data, err := os.ReadFile(filepath.Join(dir, id+".json"))
	if err != nil {
		return fmt.Errorf("backup %s not found: %w", id, err)
	}
	var bk ptysvc.Backup
	if err := json.Unmarshal(data, &bk); err != nil {
		return fmt.Errorf("backup corrupted: %w", err)
	}
	scratch := filepath.Join(dir, "scratch")
	_ = os.MkdirAll(scratch, 0o700)
	own, _ := ptysvc.LoadOwnership(filepath.Join(cfg.DataDir, "session-ownership.json"))

	restored := 0
	for _, s := range bk.Sessions {
		// --session filters: restores only that session out of a multi-session
		// backup (the same behaviour as handleTerminalRestore's body.Name filter).
		if *sess != "" && ptysvc.SafeSessionName(s.Name) != ptysvc.SafeSessionName(*sess) {
			continue
		}
		if *as != "" {
			s.Name = ptysvc.SafeSessionName(*as)
		}
		if err := ptysvc.RestoreSession(s, scratch); err != nil {
			fmt.Fprintf(os.Stderr, "restore %q: %v\n", s.Name, err)
			continue
		}
		if own != nil {
			_ = own.Claim(ptysvc.SafeSessionName(s.Name), *user)
		}
		fmt.Println(ptysvc.SafeSessionName(s.Name))
		restored++
	}
	if restored == 0 {
		return fmt.Errorf("nothing restored (session already exists? empty backup?)")
	}
	return nil
}
