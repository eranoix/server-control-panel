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

	vault, err := secrets.Open(filepath.Join(t.TempDir(), "v.vault"), "senha-de-teste-123")
	if err != nil {
		t.Fatalf("opening the vault: %v", err)
	}
	m := &Manager{opts: ManagerOptions{Vault: vault}}
	// provisionWad REQUIRES the hmac_secret: without it the daemon would sign
	// nothing and the panel would discard every incoming message (401). The test
	// needs the secret for the same reason production does.
	u, err := scope.New("sam")
	if err != nil {
		t.Fatal(err)
	}
	if err := scope.NewUserVault(vault, u).Set("waha_hmac_secret", "segredo-de-teste"); err != nil {
		t.Fatalf("seeding hmac: %v", err)
	}
	return m, state
}

// The regression: Provision runs in a LOOP per user at bootstrap; if every call
// restarts the daemon, N users = N restarts within milliseconds, systemd cuts
// it off with start-limit-hit and the daemon ends up DEAD. Reprovisioning with
// nothing changed has to be a no-op.
func TestProvisionWadNaoReiniciaQuandoNadaMuda(t *testing.T) {
	m, _ := testManager(t)
	u, err := scope.New("sam")
	if err != nil {
		t.Fatal(err)
	}

	restarts := 0
	orig := restartWadDaemon
	restartWadDaemon = func() { restarts++ }
	t.Cleanup(func() { restartWadDaemon = orig })

	// 1st time: new user → a restart is needed for the daemon to see them.
	if err := m.provisionWad(u); err != nil {
		t.Fatalf("provision 1: %v", err)
	}
	if restarts != 1 {
		t.Fatalf("1st provision: restarts = %d, want 1", restarts)
	}

	// Reprovisioning 5x with nothing changed (the boot loop) → no new restart.
	for i := 0; i < 5; i++ {
		if err := m.provisionWad(u); err != nil {
			t.Fatalf("provision %d: %v", i+2, err)
		}
	}
	if restarts != 1 {
		t.Fatalf("after reprovisioning with no change: restarts = %d, want 1", restarts)
	}
}

// A real change, on the other hand (the `enabled` flag removed = the user went
// back to WAHA and is being re-migrated), still demands the restart.
func TestProvisionWadReiniciaQuandoEstadoMuda(t *testing.T) {
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

// The point of the probe: a daemon that is down has to become an ERROR, not
// silence — it was the Store cache saying "connected" that hid 34 minutes of
// downtime.
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

func TestDaemonAliveRejeitaHTTPErro(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	t.Setenv("WAD_BASE_URL", srv.URL)
	if err := DaemonAlive(context.Background()); err == nil {
		t.Fatal("an HTTP 500 from the daemon accepted as alive")
	}
}
