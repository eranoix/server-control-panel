package queue

// runners_session.go — the schedulable session-backup runner.
//
// Why here and not a global collector: the auto-backup collector
// (api.startSessionBackupCollector) saves ALL sessions on a single interval for
// the whole host. This runner is the per-user/per-session scheduling path: each
// person schedules, in the Schedules tab, a backup of THEIR OWN sessions (one
// specific session or all of them) on whatever cron interval they like. The
// logic that touches the filesystem and the ownership registry lives in the
// Router (the Backup closure) — Run() does not receive the job's owner, so it
// arrives through the args (the same pattern as jira_ai_analysis), and HTTP
// pins that owner server-side on save (it never trusts the client).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// SessionBackupArgs are the args of a scheduled session backup.
type SessionBackupArgs struct {
	Owner     string `json:"owner"`               // owner of the sessions; injected server-side on save
	Session   string `json:"session,omitempty"`   // session name; "" or "all" = every session of the owner
	Retention int    `json:"retention,omitempty"` // keep the last N backups of the owner (<=0 = system default)
}

// SessionBackupRunner snapshots the sessions (structure + each pane's cwd +
// the foreground command + recent scrollback, in "resurrect lite" style;
// see internal/pty/backup.go). It does not resurrect live processes — restoring
// recreates the structure with the same cwds and pastes the history back as context.
type SessionBackupRunner struct {
	// Backup captures+writes the owner's backup and applies retention. session==""
	// or "all" => every session visible to the owner; retention<=0 => the system
	// default. It writes a readable summary to logW. Injected by the HTTP layer
	// (which reaches the ownership registry + the per-user DataDir).
	Backup func(ctx context.Context, owner, session string, retention int, logW io.Writer) error
}

func (SessionBackupRunner) Kind() string { return "session_backup" }

// AuthorizedFor: open to any authenticated user. The owner is pinned
// server-side on save, so each account only backs up its OWN sessions —
// there is no host-wide scope here (unlike backup_now/db_backup, primary-only).
func (SessionBackupRunner) AuthorizedFor(string, bool) bool { return true }

func (t SessionBackupRunner) Run(ctx context.Context, args json.RawMessage, logW io.Writer, progress func(int), step func(string)) error {
	var a SessionBackupArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return fmt.Errorf("args: %w", err)
	}
	if a.Owner == "" {
		return errors.New("owner missing from the schedule (recreate the session backup)")
	}
	if t.Backup == nil {
		return errors.New("session backup unavailable")
	}
	label := a.Session
	if label == "" || label == "all" {
		label = "all your sessions"
	}
	step("backing up " + label)
	progress(10)
	if err := t.Backup(ctx, a.Owner, a.Session, a.Retention, logW); err != nil {
		return err
	}
	progress(100)
	step("done")
	return nil
}
