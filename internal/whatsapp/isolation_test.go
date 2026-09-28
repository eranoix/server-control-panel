package whatsapp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"server-control-panel/internal/scope"
)

func TestDaemonStateNeverPointsToProductionUnderTest(t *testing.T) {
	os.Unsetenv("WAD_STATE_DIR")
	got := wadStateDir()
	if got == "/var/lib/panel-wad" {
		t.Fatalf("wadStateDir() = %q under test — the suite would write into PRODUCTION state", got)
	}
	if !strings.HasPrefix(got, os.TempDir()) {
		t.Fatalf("wadStateDir() = %q; wanted something inside %q", got, os.TempDir())
	}
}

func TestDaemonRestartIsInertUnderTest(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran")
	restartWadDaemon()
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("unexpected side effect")
	}
	if !testing.Testing() {
		t.Fatal("testing.Testing() false inside a test — the whole guard depends on it")
	}
}

func TestProvisionWadRejectsEmptySecret(t *testing.T) {
	m, state := testManager(t)
	u := mustUser(t, "sam")
	if err := scopeVault(m, u).Set("waha_hmac_secret", ""); err != nil {
		t.Fatal(err)
	}
	orig := restartWadDaemon
	restartWadDaemon = func() { t.Fatal("it restarted the daemon despite the invalid provision") }
	t.Cleanup(func() { restartWadDaemon = orig })

	err := m.provisionWad(u)
	if err == nil {
		t.Fatal("provisionWad accepted an empty hmac_secret")
	}
	if _, e := os.Stat(filepath.Join(state, "sam", "meta.json")); e == nil {
		t.Fatal("meta.json was written even with an invalid secret")
	}
}

func mustUser(t *testing.T, name string) scope.User {
	t.Helper()
	u, err := scope.New(name)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func scopeVault(m *Manager, u scope.User) *scope.UserVault {
	return scope.NewUserVault(m.opts.Vault, u)
}
