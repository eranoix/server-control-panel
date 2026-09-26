package pty

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRegistryRoundTrip: Put/Get/Has/Rename/Delete + persistence across a reload.
func TestRegistryRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session-registry.json")
	reg, err := LoadRegistry(path)
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	if err := reg.Put(SessionRecord{Name: "main", Socket: "/s/main.sock", PID: 4242, Backend: "dtach"}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if !reg.Has("main") {
		t.Fatal("Has(main) = false after Put")
	}
	rec, ok := reg.Get("main")
	if !ok || rec.PID != 4242 || rec.Backend != "dtach" {
		t.Fatalf("Get(main) = %+v, ok=%v", rec, ok)
	}
	if rec.Created == 0 {
		t.Error("Created was not filled in by Put")
	}

	// Rename preserva socket/pid e re-chaveia.
	if err := reg.Rename("main", "work"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if reg.Has("main") {
		t.Error("old name still present after Rename")
	}
	rec2, ok := reg.Get("work")
	if !ok || rec2.Socket != "/s/main.sock" || rec2.PID != 4242 || rec2.Name != "work" {
		t.Fatalf("after Rename: %+v ok=%v", rec2, ok)
	}

	// Persistence: reload from disk.
	reg2, err := LoadRegistry(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got, ok := reg2.Get("work"); !ok || got.PID != 4242 {
		t.Fatalf("after reload: %+v ok=%v", got, ok)
	}

	// Delete idempotente.
	if err := reg2.Delete("work"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if reg2.Has("work") {
		t.Error("record present after Delete")
	}
	if err := reg2.Delete("ghost"); err != nil {
		t.Fatalf("Delete of a nonexistent record should be a no-op: %v", err)
	}

	// Receiver nil tolerado nos read-paths.
	var nilReg *Registry
	if nilReg.Has("x") || nilReg.List() != nil {
		t.Error("nil receiver did not degrade as expected")
	}
}

// TestNewSessionBackendIsDtach: after the migration, the engine is always dtach.
func TestNewSessionBackendIsDtach(t *testing.T) {
	if b := NewSessionBackend("/tmp/x", nil); b.Kind() != "dtach" {
		t.Errorf("NewSessionBackend Kind = %q, want dtach", b.Kind())
	}
}

// TestSessionLogTeeRotation: the writer appends, rotates by size and never fails
// (best-effort). It also checks the path helper.
func TestSessionLogTeeRotation(t *testing.T) {
	dir := t.TempDir()
	want := filepath.Join(dir, "users", "sam", "session-logs", "main.log")
	if got := sessionLogPath(dir, "sam", "main"); got != want {
		t.Fatalf("sessionLogPath = %q, want %q", got, want)
	}
	w := openSessionLog(dir, "sam", "main")
	defer w.Close()
	chunk := make([]byte, 1<<20) // 1 MiB
	for i := range chunk {
		chunk[i] = 'x'
	}
	// Write > maxSessionLogBytes (8 MiB) to force at least one rotation.
	for i := 0; i < 10; i++ {
		if n, err := w.Write(chunk); n != len(chunk) || err != nil {
			t.Fatalf("Write best-effort quebrou: n=%d err=%v", n, err)
		}
	}
	w.Close()
	if _, err := os.Stat(want + ".1"); err != nil {
		t.Errorf("rotated log (.1) does not exist: %v", err)
	}
	// The active log must exist and be smaller than the ceiling.
	fi, err := os.Stat(want)
	if err != nil {
		t.Fatalf("active log missing: %v", err)
	}
	if fi.Size() > maxSessionLogBytes {
		t.Errorf("active log %d > ceiling %d (did not rotate)", fi.Size(), int64(maxSessionLogBytes))
	}
	// Sanidade do path: fica sob users/<user>/session-logs/.
	if !strings.Contains(want, filepath.Join("users", "sam", "session-logs")) {
		t.Errorf("path inesperado: %s", want)
	}
}
