package api

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"server-control-panel/internal/config"
	ptysvc "server-control-panel/internal/pty"
)

func writeBK(t *testing.T, r *Router, user string, id int64, source string, sessions ...string) {
	t.Helper()
	bk := ptysvc.Backup{ID: strconv.FormatInt(id, 10), Created: id, Source: source}
	for _, s := range sessions {
		bk.Sessions = append(bk.Sessions, ptysvc.SessionSnapshot{Name: s})
	}
	if err := r.writeSessionBackup(user, bk); err != nil {
		t.Fatalf("writeSessionBackup %d: %v", id, err)
	}
}

func countScheduled(t *testing.T, r *Router, user, name string) int {
	t.Helper()
	dir, _ := r.sessionBackupsDir(user)
	entries, _ := os.ReadDir(dir)
	n := 0
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		bk, err := r.readSessionBackup(user, e.Name()[:len(e.Name())-len(".json")])
		if err != nil {
			continue
		}
		if bk.Source == "scheduled" && len(bk.Sessions) == 1 && bk.Sessions[0].Name == name {
			n++
		}
	}
	return n
}

func countSource(t *testing.T, r *Router, user, source string) int {
	t.Helper()
	dir, _ := r.sessionBackupsDir(user)
	entries, _ := os.ReadDir(dir)
	n := 0
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		bk, err := r.readSessionBackup(user, e.Name()[:len(e.Name())-len(".json")])
		if err != nil {
			continue
		}
		if bk.Source == source {
			n++
		}
	}
	return n
}

func TestPruneSessionBackupsForSessionIsolated(t *testing.T) {
	r := &Router{cfg: &config.Config{DataDir: t.TempDir(), Primary: "sam"}}
	const u = "sam"
	for _, id := range []int64{100, 101, 102, 103, 104} {
		writeBK(t, r, u, id, "scheduled", "main")
	}
	for _, id := range []int64{200, 201, 202} {
		writeBK(t, r, u, id, "scheduled", "work")
	}
	writeBK(t, r, u, 300, "auto", "main", "work")

	r.pruneSessionBackupsForSession(u, "main", 2)

	if got := countScheduled(t, r, u, "main"); got != 2 {
		t.Fatalf("main after prune(2) = %d, want 2", got)
	}
	if got := countScheduled(t, r, u, "work"); got != 3 {
		t.Fatalf("work intact = %d, want 3", got)
	}
	if got := countSource(t, r, u, "auto"); got != 1 {
		t.Fatalf("auto bundle intact = %d, want 1", got)
	}
}

func TestPruneSessionBackupsGlobalSkipsScheduled(t *testing.T) {
	r := &Router{cfg: &config.Config{DataDir: t.TempDir(), Primary: "sam"}}
	const u = "sam"
	for _, id := range []int64{100, 101, 102, 103} {
		writeBK(t, r, u, id, "scheduled", "main")
	}
	for _, id := range []int64{300, 301, 302} {
		writeBK(t, r, u, id, "auto", "main", "work")
	}

	r.pruneSessionBackups(u, 1)

	if got := countSource(t, r, u, "auto"); got != 1 {
		t.Fatalf("auto after global prune(1) = %d, want 1", got)
	}
	if got := countScheduled(t, r, u, "main"); got != 4 {
		t.Fatalf("scheduled preserved = %d, want 4 (a global prune must not touch it)", got)
	}
}
