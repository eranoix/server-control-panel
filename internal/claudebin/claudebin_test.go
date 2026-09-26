package claudebin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func reset() {
	mu.Lock()
	cached = ""
	mu.Unlock()
}

// writeExec creates a fake executable "binary" in dir.
func writeExec(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// The "Work now" bug: with a PATH that does NOT contain claude (systemd's PATH),
// resolution has to find the binary anyway.
func TestPathResolvesOutsidePATH(t *testing.T) {
	reset()
	t.Cleanup(reset)
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".local", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	want := writeExec(t, filepath.Join(home, ".local", "bin"), "claude")

	t.Setenv("HOME", home)
	t.Setenv("PATH", "/usr/sbin:/usr/bin") // no ~/.local/bin, as in the service
	t.Setenv(EnvOverride, "")

	if got := Path(); got != want {
		t.Fatalf("Path() = %q, want %q", got, want)
	}
	if got, want := Dir(), filepath.Join(home, ".local", "bin"); got != want {
		t.Fatalf("Dir() = %q, want %q", got, want)
	}
}

func TestPathPrefersOverride(t *testing.T) {
	reset()
	t.Cleanup(reset)
	dir := t.TempDir()
	want := writeExec(t, dir, "claude-custom")
	t.Setenv(EnvOverride, want)
	if got := Path(); got != want {
		t.Fatalf("Path() = %q, want the override %q", got, want)
	}
}

// A non-existent override must not hijack resolution — it falls back to the normal flow.
func TestPathIgnoresBrokenOverride(t *testing.T) {
	reset()
	t.Cleanup(reset)
	t.Setenv(EnvOverride, filepath.Join(t.TempDir(), "nao-existe"))
	if got := Path(); strings.HasSuffix(got, "nao-existe") {
		t.Fatalf("a broken override was used: %q", got)
	}
}

func TestPathEnvPrependsAndDedupes(t *testing.T) {
	reset()
	t.Cleanup(reset)
	dir := t.TempDir()
	t.Setenv(EnvOverride, writeExec(t, dir, "claude"))

	t.Setenv("PATH", "/usr/bin")
	if got, want := PathEnv(), dir+string(os.PathListSeparator)+"/usr/bin"; got != want {
		t.Fatalf("PathEnv() = %q, want %q", got, want)
	}

	// Already present → nothing to inject.
	t.Setenv("PATH", "/usr/bin"+string(os.PathListSeparator)+dir)
	if got := PathEnv(); got != "" {
		t.Fatalf("PathEnv() = %q, wanted empty (dir already on PATH)", got)
	}
}

// Fallback: with no binary anywhere, it returns a bare "claude" (the old
// behavior) instead of "" — and PathEnv stays quiet, injecting no useless PATH.
func TestPathFallsBackToBareName(t *testing.T) {
	reset()
	t.Cleanup(reset)
	empty := t.TempDir()
	t.Setenv("HOME", empty)
	t.Setenv("PATH", empty)
	t.Setenv(EnvOverride, "")
	if _, err := os.Stat("/root/.local/bin/claude"); err == nil {
		t.Skip("the host has /root/.local/bin/claude — the absolute candidate wins")
	}
	if got := Path(); got != "claude" {
		t.Fatalf("Path() = %q, want the fallback \"claude\"", got)
	}
	if got := PathEnv(); got != "" {
		t.Fatalf("PathEnv() = %q, wanted empty on the fallback", got)
	}
}
