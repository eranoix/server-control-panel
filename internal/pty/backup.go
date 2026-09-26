// backup.go — session snapshot/restore, in the spirit of a "resurrect lite":
// it saves the STRUCTURE and the context, not the live processes.
//
// What can be captured: the STRUCTURE (windows/panes), each pane's working
// directory (pane_current_path), the command that was in the foreground
// (pane_current_command) and the scrollback (rendered text). What cannot: the
// live state of the processes — restoring recreates the structure with the same
// cwds and pastes the scrollback as context into a new shell. That is a limit of
// the model, with no way around it (no "resurrect" tool resurrects process
// memory).
//
// Everything goes through the session's dedicated socket (see backend_dtach.go).
package pty

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// PaneSnapshot is one pane: its cwd, foreground command and scrollback.
type PaneSnapshot struct {
	Index      int    `json:"index"`
	CWD        string `json:"cwd"`
	Cmd        string `json:"cmd"`
	Scrollback string `json:"scrollback,omitempty"`
}

// WindowSnapshot is one window: name, layout (an opaque string that
// select-layout reapplies) and its panes.
type WindowSnapshot struct {
	Index  int            `json:"index"`
	Name   string         `json:"name"`
	Layout string         `json:"layout"`
	Panes  []PaneSnapshot `json:"panes"`
}

// SessionSnapshot is a whole session.
type SessionSnapshot struct {
	Name    string           `json:"name"`
	Windows []WindowSnapshot `json:"windows"`
}

// Backup is the persisted file: one or more sessions at a single instant.
//
// Source labels the backup's ORIGIN, which keeps the retention domains apart so
// that one trail never deletes another's:
//   - ""           old/manual backups (the legacy global prune)
//   - "manual"     triggered by the user from the backup button
//   - "auto"       the periodic automatic collector (ALL sessions in one bundle)
//   - "scheduled"  a schedule from the Schedules tab (one backup PER session;
//     pruned per session, out of reach of the collector's global prune)
type Backup struct {
	ID       string            `json:"id"`
	Created  int64             `json:"created"`
	Source   string            `json:"source,omitempty"`
	Sessions []SessionSnapshot `json:"sessions"`
}

// DefaultScrollbackLines is how much scrollback we capture per pane. Generous
// enough for useful context without bloating the JSON (each line ~80-200 bytes).
const DefaultScrollbackLines = 2000

// SnapshotSession captures a dtach session: 1 window/1 pane (dtach does not
// multiplex the screen) with the scrollback from the pty log. scrollbackLines<=0
// skips the scrollback (a structure-only backup). The SessionSnapshot shape
// (windows/panes) is preserved for compatibility with the 634 backups already
// recorded — restoring an old (multi-pane) backup uses the main pane.
func SnapshotSession(name string, scrollbackLines int) (SessionSnapshot, error) {
	name = safeSessionName(name)
	return snapshotDtachSession(name, scrollbackLines), nil
}

// RestoreSession recreates the dtach session from the snapshot (1 pane; dtach
// does not multiplex the screen). scratchDir holds the scrollback for replay via
// `cat` (""= skip). A no-op if the name is already alive. It accepts old
// (multi-pane) snapshots: it uses the main pane.
func RestoreSession(s SessionSnapshot, scratchDir string) error {
	name := safeSessionName(s.Name)
	if name == "" || len(s.Windows) == 0 {
		return nil
	}
	return restoreDtachSession(name, s, scratchDir)
}

// shellQuote wraps s in single quotes, escaping any internal single quotes.
// Enough for our app-controlled path (no exotic characters), but defensive on
// principle.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// ── dtach: backup = a log snapshot; restore = recreate + replay ────────────

// snapshotDtachSession captures 1 window/1 pane (dtach does not multiplex the
// screen). The scrollback comes from the pty log (the single source). cwd/cmd are
// left best-effort empty (dtach does not expose pane_current_path; the restore
// falls back to the default cwd). Better to deliver the CONTEXT (the scrollback)
// than to fail because of the cwd.
func snapshotDtachSession(name string, scrollbackLines int) SessionSnapshot {
	// Capture the cwd (session-cwd.json) so the restore recreates the session in the
	// right directory — the restore used to always land in $HOME (working context lost).
	pane := PaneSnapshot{Index: 0, Cmd: "dtach", CWD: resolveSessionCWD(name)}
	if scrollbackLines > 0 {
		pane.Scrollback = dtachSessionScrollback(name, scrollbackLines)
	}
	return SessionSnapshot{
		Name:    name,
		Windows: []WindowSnapshot{{Index: 0, Name: name, Panes: []PaneSnapshot{pane}}},
	}
}

// dtachSessionScrollback reads the tail of the session's pty log (plain text,
// ANSI stripped so the replay via cat stays readable). The log is per user; since
// SnapshotSession does not have the user, it globs users/*/session-logs/<name>.log.
func dtachSessionScrollback(name string, lines int) string {
	name = safeSessionName(name)
	// Owner resolved → read THEIR log (plain text). This avoids the cross-user leak
	// of reading matches[0] of a users/* glob when two users have a session with the
	// same name (e.g. "main"). tailSessionLog already includes the .1 generation.
	if user := activeOwner(name); user != "" {
		return tailSessionLog(activeDD(), user, name, lines, false)
	}
	// No known owner: glob. It is only safe with ONE single match — several owners
	// with the same name are ambiguous, and reading any one of them would leak the
	// wrong log.
	matches, _ := filepath.Glob(filepath.Join(activeDD(), "users", "*", "session-logs", name+".log"))
	if len(matches) != 1 {
		return ""
	}
	data := readLogRotated(matches[0])
	if len(data) == 0 {
		return ""
	}
	rows := strings.Split(stripANSI(string(data)), "\n")
	if lines > 0 && len(rows) > lines {
		rows = rows[len(rows)-lines:]
	}
	return strings.Join(rows, "\n")
}

// restoreDtachSession recreates the dtach session (1 pane, a login shell) and
// injects the saved scrollback as context (clear; cat <file>). A no-op if it is
// already alive.
func restoreDtachSession(name string, s SessionSnapshot, scratchDir string) error {
	if alive, _ := SessionHas(name); alive {
		return nil
	}
	var scrollback, cmd, cwd string
	if len(s.Windows) > 0 && len(s.Windows[0].Panes) > 0 {
		p := s.Windows[0].Panes[0]
		scrollback, cmd, cwd = p.Scrollback, p.Cmd, p.CWD
	}
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash"
	}
	// Recreate the session in the saved cwd (a real restore, not just the scrollback
	// in $HOME).
	if err := SessionCreateDetached(name, []string{shell, "-l"}, nil, cwd); err != nil {
		return err
	}
	if scratchDir == "" || strings.TrimSpace(scrollback) == "" {
		return nil
	}
	fname := filepath.Join(scratchDir, safeSessionName(name)+".log")
	header := "===== scrollback restored (" + cmd + ") =====\n"
	if err := os.WriteFile(fname, []byte(header+scrollback), 0o600); err != nil {
		return nil // replay is cosmetic — it does not abort the restore
	}
	// Wait for the shell to come up and inject the cat (app-controlled path → safe).
	time.Sleep(400 * time.Millisecond)
	_ = SessionPasteAndEnter(name, "clear; cat "+shellQuote(fname))
	return nil
}
