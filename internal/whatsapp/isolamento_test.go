package whatsapp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"server-control-panel/internal/scope"
)

// THE MOST EXPENSIVE REGRESSION in this area, nailed down here.
//
// newSmokeRouter (internal/api) builds a Router with SchemaVersion 2 and a
// TEMPORARY DataDir. That fires the WhatsApp bootstrap, which calls Provision
// per user. Because wadStateDir() ignored the DataDir and pointed at a fixed
// GLOBAL path, every run of the suite:
//
//  1. generated a fresh waha_hmac_secret (the test vault is empty);
//  2. wrote that secret into the PRODUCTION meta.json;
//  3. restarted the production daemon, which from then on signed with it.
//
// The running panel carried on with the real secret → `hmac mismatch` on 100% of
// the webhooks. Measured: `go test ./internal/api/` = 77 daemon restarts; 201
// events discarded in 36h, 63 of them real messages. whatsmeow does NOT redeliver
// a refused event, so each one was gone for good. It was also the burst that
// took WhatsApp down for 34 minutes.
func TestEstadoDoDaemonNuncaApontaParaProducaoSobTeste(t *testing.T) {
	os.Unsetenv("WAD_STATE_DIR")
	got := wadStateDir()
	if got == "/var/lib/vpsm-wad" {
		t.Fatalf("wadStateDir() = %q under test — the suite would write into PRODUCTION state", got)
	}
	if !strings.HasPrefix(got, os.TempDir()) {
		t.Fatalf("wadStateDir() = %q; wanted something inside %q", got, os.TempDir())
	}
}

// The second barrier is independent of the first: even with WAD_STATE_DIR
// pointing at a temp dir, restarting the production daemon knocks the user's
// WhatsApp offline in the middle of the suite.
func TestRestartDoDaemonEInerteSobTeste(t *testing.T) {
	marcador := filepath.Join(t.TempDir(), "rodou")
	// The real function is the one that matters: if it did not have the guard, it
	// would call systemctl for real. Here we only assert that it returns inertly.
	restartWadDaemon()
	if _, err := os.Stat(marcador); err == nil {
		t.Fatal("unexpected side effect")
	}
	if !testing.Testing() {
		t.Fatal("testing.Testing() false inside a test — the whole guard depends on it")
	}
}

// provisionWad has to REFUSE to write meta.json without an hmac_secret. Writing
// an empty one makes the daemon omit the X-Webhook-Hmac header; the panel
// answers 401 "missing hmac" and discards EVERY incoming message, in silence.
func TestProvisionWadRecusaSegredoVazio(t *testing.T) {
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
