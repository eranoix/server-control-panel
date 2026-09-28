package whatsapp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"server-control-panel/internal/scope"
	"server-control-panel/internal/secrets"
)

func testManager(t *testing.T) (*Manager, string) {
	t.Helper()
	state := t.TempDir()
	t.Setenv("WAD_STATE_DIR", state)

	vault, err := secrets.Open(filepath.Join(t.TempDir(), "v.vault"), "test-password-123")
	if err != nil {
		t.Fatalf("opening the vault: %v", err)
	}
	m := &Manager{opts: ManagerOptions{Vault: vault}}
	u, err := scope.New("sam")
	if err != nil {
		t.Fatal(err)
	}
	if err := scope.NewUserVault(vault, u).Set("waha_hmac_secret", "test-secret"); err != nil {
		t.Fatalf("seeding hmac: %v", err)
	}
	return m, state
}

func TestProvisionWadDoesNotRestartWhenNothingChanges(t *testing.T) {
	m, _ := testManager(t)
	u, err := scope.New("sam")
	if err != nil {
		t.Fatal(err)
	}

	restarts := 0
	orig := restartWadDaemon
	restartWadDaemon = func() { restarts++ }
	t.Cleanup(func() { restartWadDaemon = orig })

	if err := m.provisionWad(u); err != nil {
		t.Fatalf("provision 1: %v", err)
	}
	if restarts != 1 {
		t.Fatalf("1st provision: restarts = %d, want 1", restarts)
	}

	for i := 0; i < 5; i++ {
		if err := m.provisionWad(u); err != nil {
			t.Fatalf("provision %d: %v", i+2, err)
		}
	}
	if restarts != 1 {
		t.Fatalf("after reprovisioning with no change: restarts = %d, want 1", restarts)
	}
}

func TestProvisionWadRestartsWhenStateChanges(t *testing.T) {
	m, state := testManager(t)
	u, _ := scope.New("sam")

	restarts := 0
	orig := restartWadDaemon
	restartWadDaemon = func() { restarts++ }
	t.Cleanup(func() { restartWadDaemon = orig })

	if err := m.provisionWad(u); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(state, "sam", "enabled")); err != nil {
		t.Fatal(err)
	}
	if err := m.provisionWad(u); err != nil {
		t.Fatal(err)
	}
	if restarts != 2 {
		t.Fatalf("restarts = %d, want 2 (a recreated flag is a real change)", restarts)
	}
}

func TestDaemonEnabled(t *testing.T) {
	m, state := testManager(t)
	u, _ := scope.New("sam")

	if DaemonEnabled([]scope.User{u}) {
		t.Fatal("DaemonEnabled = true before provisioning")
	}
	orig := restartWadDaemon
	restartWadDaemon = func() {}
	t.Cleanup(func() { restartWadDaemon = orig })
	if err := m.provisionWad(u); err != nil {
		t.Fatal(err)
	}
	if !DaemonEnabled([]scope.User{u}) {
		t.Fatal("DaemonEnabled = false with the `enabled` flag on disk")
	}
	_ = os.Remove(filepath.Join(state, "sam", "enabled"))
	if DaemonEnabled([]scope.User{u}) {
		t.Fatal("DaemonEnabled = true after removing the flag")
	}
}

func TestDaemonAlive(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	t.Setenv("WAD_BASE_URL", srv.URL)
	if err := DaemonAlive(context.Background()); err != nil {
		t.Fatalf("a live daemon reported dead: %v", err)
	}

	srv.Close()
	if err := DaemonAlive(context.Background()); err == nil {
		t.Fatal("a dead daemon reported alive")
	}
}

func TestDaemonAliveRejectsHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	t.Setenv("WAD_BASE_URL", srv.URL)
	if err := DaemonAlive(context.Background()); err == nil {
		t.Fatal("an HTTP 500 from the daemon accepted as alive")
	}
}
