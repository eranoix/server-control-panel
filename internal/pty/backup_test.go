package pty

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSnapshotDtachReadsLog: with dtach, SnapshotSession captures 1 window/1
// pane and reads the scrollback from the pty LOG (a
// users/*/session-logs/<name>.log glob) — the stand-in for capture-pane. ANSI is
// stripped so the replay via cat stays readable.
func TestSnapshotDtachReadsLog(t *testing.T) {
	dir := t.TempDir()
	reg, _ := LoadRegistry(filepath.Join(dir, "reg.json"))
	InitSessionBackend(dir, reg)

	const name = "bktest"
	logPath := filepath.Join(dir, "users", "sam", "session-logs", name+".log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		t.Fatal(err)
	}
	content := "linha de contexto\noutra em \x1b[31mvermelho\x1b[0m\n"
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
	// dtach = 1 window / 1 pane (it does not multiplex the screen).
	if len(snap.Windows) != 1 || len(snap.Windows[0].Panes) != 1 {
		t.Fatalf("estrutura dtach inesperada: %+v", snap.Windows)
	}
	sb := snap.Windows[0].Panes[0].Scrollback
	if !strings.Contains(sb, "linha de contexto") || !strings.Contains(sb, "vermelho") {
		t.Errorf("scrollback did not capture the log: %q", sb)
	}
	if strings.Contains(sb, "\x1b[") {
		t.Errorf("scrollback should have ANSI stripped: %q", sb)
	}

	// scrollbackLines<=0 pula a captura de scrollback.
	re, err := SnapshotSession(name, 0)
	if err != nil {
		t.Fatalf("SnapshotSession(0): %v", err)
	}
	if re.Windows[0].Panes[0].Scrollback != "" {
		t.Errorf("scrollbackLines<=0 should skip the scrollback")
	}
}
