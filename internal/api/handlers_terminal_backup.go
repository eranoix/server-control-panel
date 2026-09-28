package api

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

func (r *Router) backupStore() *sessionbackup.Store {
	return sessionbackup.New(r.cfg.DataDir)
}

func (r *Router) sessionBackupsDir(user string) (string, error) {
	return r.backupStore().Dir(user)
}

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

func sessionSummary(s ptysvc.SessionSnapshot) string { return sessionbackup.Summary(s) }

func summarizePane(raw string) (headline, body string) {
	return sessionbackup.PanelSummary(raw)
}

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
	if err := r.sessionOwn.Rename(body.Name, ptysvc.SafeSessionName(body.NewName)); err != nil {
		log.Printf("session ownership: rename %s->%s: %v", body.Name, body.NewName, err)
	}
	ptysvc.StopRecorder(r.cfg.DataDir, user, body.Name)
	ptysvc.EnsureRecorder(r.cfg.DataDir, user, ptysvc.SafeSessionName(body.NewName), r.sessReg)
	r.auditEvent(req, user, "terminal.rename", body.Name+"->"+body.NewName)
	writeJSON(w, map[string]string{"status": "ok", "name": ptysvc.SafeSessionName(body.NewName)})
}

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
	_ = json.NewDecoder(req.Body).Decode(&body)

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
			continue
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

func (r *Router) handleTerminalBackupsList(w http.ResponseWriter, req *http.Request) {
	user := auth.UserFrom(req)
	if user == "" {
		writeErr(w, 401, "unauthorized")
		return
	}
	writeJSON(w, r.backupStore().List(user))
}

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
	if !sessionbackup.ValidID(body.ID) {
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
		if err := r.sessionOwn.Claim(ptysvc.SafeSessionName(s.Name), user); err != nil {
			log.Printf("session ownership: claim while restoring %s: %v", s.Name, err)
		}
		restored++
	}
	r.auditEvent(req, user, "terminal.restore", body.ID)
	writeJSON(w, map[string]any{"status": "ok", "restored": restored, "skipped": skipped})
}

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
		Name string `json:"name"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	if !sessionbackup.ValidID(body.ID) {
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
	target := body.ID
	if body.Name != "" {
		target += "#" + ptysvc.SafeSessionName(body.Name)
	}
	r.auditEvent(req, user, "terminal.backup_delete", target)
	writeJSON(w, map[string]string{"status": "ok"})
}

func (r *Router) writeSessionBackup(user string, bk ptysvc.Backup) error {
	return r.backupStore().Write(user, bk)
}

const sessionBackupRetention = 10

func (r *Router) startSessionBackupCollector(ctx context.Context) {
	interval := 6 * time.Hour
	if v := strings.TrimSpace(os.Getenv("PANEL_SESSION_BACKUP_INTERVAL")); v != "" {
		if v == "0" || strings.EqualFold(v, "off") {
			log.Printf("automatic session backup: turned off via PANEL_SESSION_BACKUP_INTERVAL")
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
			owner = r.cfg.Primary
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
			fmt.Fprintf(logW, "⚠ %s: %v (skipped)\n", n, err)
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
		r.pruneSessionBackupsForSession(owner, n, keep)
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

func (r *Router) pruneSessionBackups(user string, keep int) {
	r.backupStore().Prune(user, keep)
}

func (r *Router) pruneSessionBackupsForSession(user, session string, keep int) {
	r.backupStore().PruneSession(user, session, keep)
}

func (r *Router) readSessionBackup(user, id string) (ptysvc.Backup, error) {
	return r.backupStore().Read(user, id)
}
