package pty

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDtachMasterArgs: the master's argv carries the user.slice scope (survival),
// the --setenv flags, and dtach's options (-n -E -z), with the cmd at the end.
// It mirrors the previous engine's test (it tests construction, not spawning).
// The -r winch does NOT belong in the master (it is the client's).
func TestDtachMasterArgs(t *testing.T) {
	args := dtachMasterArgs("/usr/bin/systemd-run", "/usr/bin/dtach",
		"/data/session-sox/main.sock",
		[]string{"/bin/bash", "-l"},
		[]string{"CLAUDE_CONFIG_DIR=/x", "TERM=xterm-256color"},
		"/home/sam")
	joined := strings.Join(args, " ")

	for _, must := range []string{
		"--scope", "--slice=user.slice",
		"--setenv=CLAUDE_CONFIG_DIR=/x", "--setenv=TERM=xterm-256color",
		"--working-directory=/home/sam",
		"/usr/bin/dtach", "-n", "/data/session-sox/main.sock",
		"-E", "-z", "/bin/bash -l",
	} {
		if !strings.Contains(joined, must) {
			t.Errorf("argv does not contain %q:\n%s", must, joined)
		}
	}
	if strings.Contains(joined, "-r winch") {
		t.Errorf("master should not have -r winch (that belongs to the client):\n%s", joined)
	}
	if args[0] != "--quiet" {
		t.Errorf("argv[0] = %q, want --quiet (systemd-run)", args[0])
	}
	if !strings.Contains(joined, "-- /usr/bin/dtach") {
		t.Errorf("the `-- <dtach>` separator is missing:\n%s", joined)
	}

	// Fallback sem systemd-run: dtach cru na frente.
	raw := dtachMasterArgs("", "/usr/bin/dtach", "/s/x.sock", []string{"/bin/bash", "-l"}, nil, "")
	if raw[0] != "/usr/bin/dtach" || raw[1] != "-n" {
		t.Errorf("fallback cru inesperado: %v", raw)
	}
}

// TestDtachClientArgs: the client attaches (`-a`) with the transparent options,
// without going through the scope (it lives in the caller's cgroup).
func TestDtachClientArgs(t *testing.T) {
	args := dtachClientArgs("/usr/bin/dtach", "/s/main.sock")
	want := "/usr/bin/dtach -a /s/main.sock -E -z -r winch"
	if strings.Join(args, " ") != want {
		t.Errorf("client argv = %q, want %q", strings.Join(args, " "), want)
	}
}

// TestSocketPathResolve: the default is derived from the name; after registration,
// the registry wins (name↔socket decoupling, for the rename).
func TestSocketPathResolve(t *testing.T) {
	dir := t.TempDir()
	reg, _ := LoadRegistry(filepath.Join(dir, "reg.json"))
	b := newDtachBackend(dir, reg)

	if got := socketPathFor(dir, "main"); got != filepath.Join(dir, "session-sox", "main.sock") {
		t.Fatalf("socketPathFor = %q", got)
	}
	// Sem entry no registry → default.
	if got := b.resolveSocket("main"); got != socketPathFor(dir, "main") {
		t.Errorf("resolveSocket without registry = %q", got)
	}
	// With an entry (a custom socket, e.g. after a rename) → the registry wins.
	_ = reg.Put(SessionRecord{Name: "renamed", Socket: "/custom/orig.sock", Backend: "dtach"})
	if got := b.resolveSocket("renamed"); got != "/custom/orig.sock" {
		t.Errorf("resolveSocket with registry = %q, want /custom/orig.sock", got)
	}
}

// TestSocketAlive: an orphaned socket file (no listener) = dead; a real listener = alive.
func TestSocketAlive(t *testing.T) {
	dir := t.TempDir()
	if socketAlive("") || socketAlive(filepath.Join(dir, "nao-existe.sock")) {
		t.Error("a missing socket should be dead")
	}
	// An ordinary file (not a socket) exists but does not listen → dead.
	orphan := filepath.Join(dir, "orphan.sock")
	if err := os.WriteFile(orphan, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if socketAlive(orphan) {
		t.Error("an orphaned socket (no listener) should be dead")
	}
	// Listener unix real → vivo.
	live := filepath.Join(dir, "live.sock")
	ln, err := net.Listen("unix", live)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if !socketAlive(live) {
		t.Error("a socket with a listener should be alive")
	}
}

// TestDtachRename: rename re-keys the registry; a collision with a live/registered
// name is refused.
func TestDtachRename(t *testing.T) {
	dir := t.TempDir()
	reg, _ := LoadRegistry(filepath.Join(dir, "reg.json"))
	b := newDtachBackend(dir, reg)

	_ = reg.Put(SessionRecord{Name: "a", Socket: "/s/a.sock", Backend: "dtach"})
	if err := b.Rename("a", "b"); err != nil {
		t.Fatalf("Rename a→b: %v", err)
	}
	if reg.Has("a") || !reg.Has("b") {
		t.Error("rename did not re-key the registry")
	}
	rec, _ := reg.Get("b")
	if rec.Socket != "/s/a.sock" {
		t.Errorf("socket not preserved across the rename: %q", rec.Socket)
	}
	// Collision: renaming to an already registered name → error.
	_ = reg.Put(SessionRecord{Name: "c", Socket: "/s/c.sock", Backend: "dtach"})
	if err := b.Rename("b", "c"); err == nil {
		t.Error("rename to an existing name should fail")
	}
}

// TestNewSessionBackendDtach: with the flag, NewSessionBackend returns dtach.
func TestNewSessionBackendDtach(t *testing.T) {
	t.Setenv("VPSM_SESSION_BACKEND", "dtach")
	b := NewSessionBackend(t.TempDir(), nil)
	if b.Kind() != "dtach" {
		t.Fatalf("Kind = %q, want dtach", b.Kind())
	}
}
