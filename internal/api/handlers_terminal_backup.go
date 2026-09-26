package api

// handlers_terminal_backup.go — session management beyond list/kill:
// rename, detach (deactivate), and "resurrect lite"-style backup/restore
// (see internal/pty/backup.go).
//
// Tenant boundary (same as handlers_users.go):
//   - per-session operations require OwnsSession → 404 (never 403; cross-user
//     existence does not leak).
//   - backup-all / restore only touch what is visible to the user.
//
// Persistence: <DataDir>/users/<user>/session-backups/<id>.json. The id is a
// server-generated timestamp (^[0-9]+$) → path-safe; restore/delete re-validate
// the format before touching the filesystem (no traversal).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"server-control-panel/internal/auth"
	ptysvc "server-control-panel/internal/pty"
	"server-control-panel/internal/sessionbackup"
)

// backupIDRe makes sure the id is digits only — it blocks any path traversal
// (../, /, and so on) before the file path is composed.

// sessionBackupsDir returns (and creates) the user's backup directory. Same
// per-user path pattern as handlers_browser.go.
func (r *Router) backupStore() *sessionbackup.Store {
	return sessionbackup.New(r.cfg.DataDir)
}

func (r *Router) sessionBackupsDir(user string) (string, error) {
	return r.backupStore().Dir(user)
}

// handleTerminalPreview returns a short preview of what the session is doing:
// the content rendered on screen right now, cleaned up (no TUI borders, no
// blank lines). For claude sessions it picks the "recap:" line as the headline
// when there is one. Read-only; requires ownership (404 cross-user).
func (r *Router) handleTerminalPreview(w http.ResponseWriter, req *http.Request) {
	user := auth.UserFrom(req)
	if user == "" {
		writeErr(w, 401, "unauthorized")
		return
	}
	name := req.URL.Query().Get("name")
	if !ptysvc.OwnsSession(user, name, r.isPrimary(user), r.sessionOwn) {
		writeErr(w, 404, "session not found")
		return
	}
	raw := capturePaneTail(ptysvc.SafeSessionName(name), 200)
	headline, body := summarizePane(raw)
	writeJSON(w, map[string]string{"name": name, "headline": headline, "body": body})
}

// sessionSummary returns ONE very short line about what the session is about,
// taken from the scrollback saved in the backup. It prefers claude's "recap:"
// line; failing that, the last useful line. Empty when there is nothing
// readable (a clean shell, say). Used on the Backups tab to give context
// without opening anything.
func sessionSummary(s ptysvc.SessionSnapshot) string { return sessionbackup.Resumo(s) }

// summarizePane reduces the pane capture to a readable summary: it drops blank
// lines, separators and TUI box borders, takes the last ~14 useful lines (the
// bottom of the screen = the most recent) and extracts a headline from claude's
// "recap:" line when there is one.
func summarizePane(raw string) (headline, body string) {
	return sessionbackup.ResumoDePainel(raw)
}

// handleTerminalRenameSession renames a session and carries ownership with it.
func (r *Router) handleTerminalRenameSession(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	user := auth.UserFrom(req)
	if user == "" {
		writeErr(w, 401, "unauthorized")
		return
	}
	var body struct {
		Name    string `json:"name"`
		NewName string `json:"newName"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	if !ptysvc.OwnsSession(user, body.Name, r.isPrimary(user), r.sessionOwn) {
		writeErr(w, 404, "session not found")
		return
	}
	if err := ptysvc.SessionRename(body.Name, body.NewName); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	// Ownership follows the session. Re-sanitise it the way the backend does so
	// the key matches.
	if err := r.sessionOwn.Rename(body.Name, ptysvc.SafeSessionName(body.NewName)); err != nil {
		log.Printf("session ownership: rename %s->%s: %v", body.Name, body.NewName, err)
	}
	// The log path comes from the NAME: after the rename, the old recorder would
	// be feeding the file of a name that no longer exists. Stop the old one and
	// start the new one HERE — leaving it to the next attach would open a window
	// in which the renamed session had nobody recording it, which is exactly the
	// hole the recorder exists to close.
	ptysvc.PararGravador(r.cfg.DataDir, user, body.Name)
	ptysvc.GaranteGravador(r.cfg.DataDir, user, ptysvc.SafeSessionName(body.NewName), r.sessReg)
	r.auditEvent(req, user, "terminal.rename", body.Name+"->"+body.NewName)
	writeJSON(w, map[string]string{"status": "ok", "name": ptysvc.SafeSessionName(body.NewName)})
}

// handleTerminalBackup backs up ONE session ({name}) or ALL the ones visible
// to the user ({} or an empty name). Returns {id, created, count}.
func (r *Router) handleTerminalBackup(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	user := auth.UserFrom(req)
	if user == "" {
		writeErr(w, 401, "unauthorized")
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	_ = json.NewDecoder(req.Body).Decode(&body) // an empty body is valid (= all of them)

	var names []string
	if body.Name != "" {
		if !ptysvc.OwnsSession(user, body.Name, r.isPrimary(user), r.sessionOwn) {
			writeErr(w, 404, "session not found")
			return
		}
		names = []string{body.Name}
	} else {
		sessions, err := ptysvc.SessionListForUser(user, r.isPrimary(user), r.sessionOwn)
		if err != nil {
			writeErr(w, 500, "list sessions: "+err.Error())
			return
		}
		for _, s := range sessions {
			if n, _ := s["name"].(string); n != "" {
				names = append(names, n)
			}
		}
	}
	if len(names) == 0 {
		writeErr(w, 400, "no session to back up")
		return
	}

	bk := ptysvc.Backup{ID: strconv.FormatInt(time.Now().UnixNano(), 10), Created: time.Now().Unix(), Source: "manual"}
	for _, n := range names {
		snap, err := ptysvc.SnapshotSession(n, ptysvc.DefaultScrollbackLines)
		if err != nil {
			continue // the session vanished mid-backup — skip it
		}
		bk.Sessions = append(bk.Sessions, snap)
	}
	if len(bk.Sessions) == 0 {
		writeErr(w, 500, "failed to capture sessions")
		return
	}
	if err := r.writeSessionBackup(user, bk); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	r.auditEvent(req, user, "terminal.backup", bk.ID)
	writeJSON(w, map[string]any{"status": "ok", "id": bk.ID, "created": bk.Created, "count": len(bk.Sessions)})
}

// handleTerminalBackupsList lists the user's backups (metadata only, without
// the heavy scrollback), newest first.
func (r *Router) handleTerminalBackupsList(w http.ResponseWriter, req *http.Request) {
	user := auth.UserFrom(req)
	if user == "" {
		writeErr(w, 401, "unauthorized")
		return
	}
	// The shape returned here is the one the web dashboard has consumed for
	// versions; the Store returns the same fields under the same JSON names,
	// plus `source` and `bytes`, which the dashboard ignores and the app uses.
	writeJSON(w, r.backupStore().List(user))
}

// handleTerminalRestore restores every session in a backup, or only the {name}
// session from it. Skips names that are already alive (it does not overwrite).
// Respects the per-user cap.
func (r *Router) handleTerminalRestore(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	user := auth.UserFrom(req)
	if user == "" {
		writeErr(w, 401, "unauthorized")
		return
	}
	var body struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	if !sessionbackup.IDValido(body.ID) {
		writeErr(w, 400, "invalid backup id")
		return
	}
	bk, err := r.readSessionBackup(user, body.ID)
	if err != nil {
		writeErr(w, 404, "backup not found")
		return
	}
	scratch, err := r.sessionBackupsDir(user)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	scratch = filepath.Join(scratch, "scratch")
	_ = os.MkdirAll(scratch, 0o700)

	// Cap: how many sessions the user already has + the ones we are about to create.
	existing := len(r.sessionOwn.SessionsOf(user))
	restored := 0
	skipped := 0
	for _, s := range bk.Sessions {
		if body.Name != "" && s.Name != ptysvc.SafeSessionName(body.Name) {
			continue
		}
		if existing+restored >= ptysvc.MaxSessionsPerUser {
			skipped++
			continue
		}
		if err := ptysvc.RestoreSession(s, scratch); err != nil {
			skipped++
			continue
		}
		// Ownership: the recreated session belongs to whoever restored it.
		if err := r.sessionOwn.Claim(ptysvc.SafeSessionName(s.Name), user); err != nil {
			log.Printf("session ownership: claim while restoring %s: %v", s.Name, err)
		}
		restored++
	}
	r.auditEvent(req, user, "terminal.restore", body.ID)
	writeJSON(w, map[string]any{"status": "ok", "restored": restored, "skipped": skipped})
}

// handleTerminalBackupDelete removes one of the user's backups.
func (r *Router) handleTerminalBackupDelete(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	user := auth.UserFrom(req)
	if user == "" {
		writeErr(w, 401, "unauthorized")
		return
	}
	var body struct {
		ID   string `json:"id"`
		Name string `json:"name"` // optional: remove only this session from the backup
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	if !sessionbackup.IDValido(body.ID) {
		writeErr(w, 400, "invalid backup id")
		return
	}
	dir, err := r.sessionBackupsDir(user)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	_ = dir

	if err := r.backupStore().Delete(user, body.ID, body.Name); err != nil {
		if os.IsNotExist(err) {
			writeErr(w, 404, "backup not found")
			return
		}
		writeErr(w, 500, err.Error())
		return
	}
	alvo := body.ID
	if body.Name != "" {
		alvo += "#" + ptysvc.SafeSessionName(body.Name)
	}
	r.auditEvent(req, user, "terminal.backup_delete", alvo)
	writeJSON(w, map[string]string{"status": "ok"})
}

// writeSessionBackup writes the backup atomically (temp + rename).
func (r *Router) writeSessionBackup(user string, bk ptysvc.Backup) error {
	return r.backupStore().Write(user, bk)
}

// sessionBackupRetention is how many automatic backups we keep per user.
// Manual backups live in the same directory — the prune only removes the oldest
// ones beyond the limit, manual or automatic (the user can delete whatever they
// want by hand).
const sessionBackupRetention = 10

// startSessionBackupCollector runs a periodic automatic backup of ALL sessions,
// grouped by owner, and applies retention. Interval via
// VPSM_SESSION_BACKUP_INTERVAL (e.g. "6h", "30m"); default 6h; "0"/"off" turns
// it off. Same goroutine+recover+ctx pattern as startMetricsCollector.
func (r *Router) startSessionBackupCollector(ctx context.Context) {
	interval := 6 * time.Hour
	if v := strings.TrimSpace(os.Getenv("VPSM_SESSION_BACKUP_INTERVAL")); v != "" {
		if v == "0" || strings.EqualFold(v, "off") {
			log.Printf("automatic session backup: turned off via VPSM_SESSION_BACKUP_INTERVAL")
			return
		}
		if d, err := time.ParseDuration(v); err == nil && d >= time.Minute {
			interval = d
		}
	}
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("session backup: panic in the collector: %v", rec)
			}
		}()
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			r.runSessionAutoBackup()
		}
	}()
}

// runSessionAutoBackup groups the live sessions by owner (unowned → primary)
// and writes one backup per user, pruning the old ones.
func (r *Router) runSessionAutoBackup() {
	all, err := ptysvc.SessionListAll()
	if err != nil || len(all) == 0 {
		return
	}
	byUser := map[string][]string{}
	for _, s := range all {
		name, _ := s["name"].(string)
		if name == "" {
			continue
		}
		owner := r.sessionOwn.Owner(name)
		if owner == "" {
			owner = r.cfg.Primary // ownerless sessions belong to the primary
		}
		if owner == "" {
			continue
		}
		byUser[owner] = append(byUser[owner], name)
	}
	for user, names := range byUser {
		bk := ptysvc.Backup{ID: strconv.FormatInt(time.Now().UnixNano(), 10), Created: time.Now().Unix(), Source: "auto"}
		for _, n := range names {
			snap, err := ptysvc.SnapshotSession(n, ptysvc.DefaultScrollbackLines)
			if err != nil {
				continue
			}
			bk.Sessions = append(bk.Sessions, snap)
		}
		if len(bk.Sessions) == 0 {
			continue
		}
		if err := r.writeSessionBackup(user, bk); err != nil {
			log.Printf("automatic session backup: write %s: %v", user, err)
			continue
		}
		r.pruneSessionBackups(user, sessionBackupRetention)
	}
}

// runSessionBackupJob is the closure behind the schedulable session_backup
// runner (queue.SessionBackupRunner). It snapshots the owner's sessions — one
// specific session or all the ones visible to them — writes the per-user backup
// and applies retention. It reuses the same trail as the manual backup and the
// automatic collector (SnapshotSession + writeSessionBackup +
// pruneSessionBackups), so a restore done from the Backups tab sees these
// snapshots just like any other.
//
// The owner arrives already validated by the HTTP layer (pinned when the
// schedule was saved); here it only rescopes the filesystem and ownership
// operations to that user. retention<=0 falls back to the system default
// (sessionBackupRetention).
//
// EACH active session becomes a SEPARATE backup (Source="scheduled", one
// session per file) — whether the schedule targets one specific session or
// "all of them". That way each session has its own timeline of versions and its
// own retention (keeping the last N per session), and the "all" option saves
// each active one individually, as asked for. The per-session prune does not
// touch the bundles from the automatic or manual collector, and the collector's
// global prune does not touch these "scheduled" ones.
func (r *Router) runSessionBackupJob(ctx context.Context, owner, session string, retention int, logW io.Writer) error {
	if owner == "" {
		return errors.New("empty owner")
	}
	if r.sessionOwn == nil {
		return errors.New("session registry unavailable")
	}
	primary := r.isPrimary(owner)

	var names []string
	specific := false
	if s := strings.TrimSpace(session); s != "" && s != "all" {
		if !ptysvc.OwnsSession(owner, s, primary, r.sessionOwn) {
			return fmt.Errorf("session %q not found for %s", s, owner)
		}
		names = []string{s}
		specific = true
	} else {
		sessions, err := ptysvc.SessionListForUser(owner, primary, r.sessionOwn)
		if err != nil {
			return fmt.Errorf("list sessions: %w", err)
		}
		for _, sess := range sessions {
			if n, _ := sess["name"].(string); n != "" {
				names = append(names, n)
			}
		}
	}
	if len(names) == 0 {
		fmt.Fprintln(logW, "no active session to back up — nothing to do")
		return nil
	}

	keep := retention
	if keep <= 0 {
		keep = sessionBackupRetention
	}
	// Unique IDs inside the loop: UnixNano can repeat across very close
	// iterations; base+i keeps the id numeric (^[0-9]+$, which restore/delete
	// require), unique and chronologically sortable.
	base := time.Now().UnixNano()
	created := time.Now().Unix()
	saved, failed := 0, 0
	for i, n := range names {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		snap, err := ptysvc.SnapshotSession(n, ptysvc.DefaultScrollbackLines)
		if err != nil {
			fmt.Fprintf(logW, "⚠ %s: %v (skipped)\n", n, err) // the session vanished mid-run — skip it
			failed++
			continue
		}
		bk := ptysvc.Backup{
			ID:       strconv.FormatInt(base+int64(i), 10),
			Created:  created,
			Source:   "scheduled",
			Sessions: []ptysvc.SessionSnapshot{snap},
		}
		if err := r.writeSessionBackup(owner, bk); err != nil {
			fmt.Fprintf(logW, "⚠ %s: write failed: %v\n", n, err)
			failed++
			continue
		}
		r.pruneSessionBackupsForSession(owner, n, keep) // independent retention per session
		fmt.Fprintf(logW, "✓ %s → backup %s (keeping the last %d of this session)\n", n, bk.ID, keep)
		saved++
	}
	if saved == 0 {
		return errors.New("failed to capture the sessions")
	}
	scope := "all active sessions"
	if specific {
		scope = "session " + names[0]
	}
	fmt.Fprintf(logW, "✓ backup of %s — %d saved individually, %d skipped\n", scope, saved, failed)
	return nil
}

// pruneSessionBackups keeps only the `keep` most recent backups of the user,
// IGNORING the scheduler's per-session backups (Source="scheduled"). Those have
// their own per-session retention (pruneSessionBackupsForSession); if the
// automatic collector's global prune counted them, a working day with many
// scheduled sessions would silently erase the history it had just created. Old,
// manual and automatic backups (Source ""/"manual"/"auto") still go through the
// legacy global prune.
func (r *Router) pruneSessionBackups(user string, keep int) {
	r.backupStore().Prune(user, keep)
}

// pruneSessionBackupsForSession keeps only the `keep` most recent scheduled
// backups of ONE session (Source="scheduled" holding exactly that session). It
// touches neither bundles (manual/auto) nor backups of other sessions — each
// session has its own independent retention.
func (r *Router) pruneSessionBackupsForSession(user, session string, keep int) {
	r.backupStore().PruneSessao(user, session, keep)
}

// readSessionBackup reads and deserialises a backup. The id has already been
// validated by the caller on routes that accept user input; for the listing the
// name comes from the ReadDir.
func (r *Router) readSessionBackup(user, id string) (ptysvc.Backup, error) {
	return r.backupStore().Read(user, id)
}
