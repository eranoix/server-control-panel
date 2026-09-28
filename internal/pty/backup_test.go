package pty

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSnapshotDtachReadsLog(t *testing.T) {
	dir := t.TempDir()
	reg, _ := LoadRegistry(filepath.Join(dir, "reg.json"))
	InitSessionBackend(dir, reg)

	const name = "bktest"
	logPath := filepath.Join(dir, "users", "sam", "session-logs", name+".log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		t.Fatal(err)
	}
	content := "context line\nanother in \x1b[31mred\x1b[0m\n"
	if err := os.WriteFile(logPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	snap, err := SnapshotSession(name, 50)
	if err != nil {
		t.Fatalf("SnapshotSession: %v", err)
	}
	if snap.Name != name {
		t.Fatalf("snap.Name = %q, want %q", snap.Name, name)
	}
	if len(snap.Windows) != 1 || len(snap.Windows[0].Panes) != 1 {
		t.Fatalf("unexpected dtach structure: %+v", snap.Windows)
	}
	sb := snap.Windows[0].Panes[0].Scrollback
	if !strings.Contains(sb, "context line") || !strings.Contains(sb, "red") {
		t.Errorf("scrollback did not capture the log: %q", sb)
	}
	if strings.Contains(sb, "\x1b[") {
		t.Errorf("scrollback should have ANSI stripped: %q", sb)
	}

	re, err := SnapshotSession(name, 0)
	if err != nil {
		t.Fatalf("SnapshotSession(0): %v", err)
	}
	if re.Windows[0].Panes[0].Scrollback != "" {
		t.Errorf("scrollbackLines<=0 should skip the scrollback")
	}
}
