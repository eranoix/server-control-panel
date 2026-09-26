package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// vaultPath returns a throwaway vault file inside the test's temp dir.
func vaultPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "secrets.vault")
}

// readFileFormat decodes the on-disk envelope so tests can assert about the
// salt/nonce without going through the decrypt path.
func readFileFormat(t *testing.T, path string) fileFormat {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read vault file: %v", err)
	}
	var ff fileFormat
	if err := json.Unmarshal(raw, &ff); err != nil {
		t.Fatalf("unmarshal vault file: %v", err)
	}
	return ff
}

// writeLegacyVault writes a vault the way vps-manager < 2026-06-09 did:
// plaintext is a plain map[string]string, salt derived deterministically
// from the passphrase, and NO `salt` field in the JSON. This is the exact
// shape Open() must keep reading.
func writeLegacyVault(t *testing.T, path, passphrase string, data map[string]string) {
	t.Helper()
	pp := []byte(passphrase)
	salt := legacySalt(pp)
	key, err := deriveKey(pp, salt)
	if err != nil {
		t.Fatalf("deriveKey: %v", err)
	}
	pt, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal plaintext: %v", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("aes: %v", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("gcm: %v", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		t.Fatalf("nonce: %v", err)
	}
	ct := gcm.Seal(nil, nonce, pt, nil)
	ff := fileFormat{
		// Salt intentionally empty: marks this as a legacy vault.
		Nonce:      hex.EncodeToString(nonce),
		Ciphertext: hex.EncodeToString(ct),
	}
	raw, err := json.Marshal(ff)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write vault: %v", err)
	}
}

func TestRoundTrip(t *testing.T) {
	path := vaultPath(t)
	s, err := Open(path, "correct horse battery staple")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.Set("api_key", "s3cr3t"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if v, ok := s.Get("api_key"); !ok || v != "s3cr3t" {
		t.Fatalf("Get = (%q,%v), want (s3cr3t,true)", v, ok)
	}

	// Reopen from disk: data must survive a process restart.
	s2, err := Open(path, "correct horse battery staple")
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if v, ok := s2.Get("api_key"); !ok || v != "s3cr3t" {
		t.Fatalf("after reopen Get = (%q,%v), want (s3cr3t,true)", v, ok)
	}

	// Delete removes it and persists.
	if err := s2.Delete("api_key"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok := s2.Get("api_key"); ok {
		t.Fatal("key still present after Delete")
	}
	s3, err := Open(path, "correct horse battery staple")
	if err != nil {
		t.Fatalf("reopen after delete: %v", err)
	}
	if _, ok := s3.Get("api_key"); ok {
		t.Fatal("deleted key resurrected after reopen")
	}
}

func TestWrongPassphraseFails(t *testing.T) {
	path := vaultPath(t)
	s, err := Open(path, "right-passphrase")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.Set("k", "v"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if _, err := Open(path, "wrong-passphrase"); err == nil {
		t.Fatal("Open succeeded with wrong passphrase; want decrypt error")
	}
}

// TestLegacyVaultMigratesSalt is the cross-binary guarantee: a vault written
// by an old binary (plain map, no salt field) must decrypt, and the first
// save() must transparently mint a random salt without losing data.
func TestLegacyVaultMigratesSalt(t *testing.T) {
	path := vaultPath(t)
	const pp = "legacy-pass"
	writeLegacyVault(t, path, pp, map[string]string{"a": "1", "b": "2"})

	// Legacy file has no salt field.
	if ff := readFileFormat(t, path); ff.Salt != "" {
		t.Fatalf("precondition: legacy vault should have empty salt, got %q", ff.Salt)
	}

	s, err := Open(path, pp)
	if err != nil {
		t.Fatalf("Open legacy: %v", err)
	}
	if !s.needsResalt {
		t.Fatal("legacy vault should be flagged needsResalt")
	}
	if v, _ := s.Get("a"); v != "1" {
		t.Fatalf("legacy Get a = %q, want 1", v)
	}

	// First write migrates: a random salt must now be persisted.
	if err := s.Set("c", "3"); err != nil {
		t.Fatalf("Set (triggers migration): %v", err)
	}
	if ff := readFileFormat(t, path); ff.Salt == "" {
		t.Fatal("after save, salt field still empty — migration did not run")
	}

	// All values (old + new) survive, and reopen works with the new salt.
	s2, err := Open(path, pp)
	if err != nil {
		t.Fatalf("reopen post-migration: %v", err)
	}
	for k, want := range map[string]string{"a": "1", "b": "2", "c": "3"} {
		if v, ok := s2.Get(k); !ok || v != want {
			t.Fatalf("post-migration Get %s = (%q,%v), want %q", k, v, ok, want)
		}
	}
}

// TestSaveUsesFreshNonce guards GCM semantic security: writing the same
// plaintext twice must not produce an identical ciphertext/nonce.
func TestSaveUsesFreshNonce(t *testing.T) {
	path := vaultPath(t)
	s, err := Open(path, "pp")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.Set("k", "same-value"); err != nil {
		t.Fatalf("Set 1: %v", err)
	}
	first := readFileFormat(t, path)

	if err := s.Set("k", "same-value"); err != nil {
		t.Fatalf("Set 2: %v", err)
	}
	second := readFileFormat(t, path)

	if first.Nonce == second.Nonce {
		t.Fatal("nonce reused across saves — breaks GCM semantic security")
	}
	if first.Ciphertext == second.Ciphertext {
		t.Fatal("ciphertext identical across saves of same plaintext")
	}
	// Salt is stable for a non-legacy vault (only nonce rolls).
	if first.Salt != second.Salt {
		t.Fatalf("salt changed unexpectedly across plain saves: %q -> %q", first.Salt, second.Salt)
	}
}

func TestRotatePreservesValues(t *testing.T) {
	path := vaultPath(t)
	s, err := Open(path, "old-pass")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	want := map[string]string{"x": "10", "y": "20", "z": "30"}
	for k, v := range want {
		if err := s.Set(k, v); err != nil {
			t.Fatalf("Set %s: %v", k, err)
		}
	}

	if err := s.Rotate("new-pass"); err != nil {
		t.Fatalf("Rotate: %v", err)
	}

	// Old passphrase must no longer open the vault.
	if _, err := Open(path, "old-pass"); err == nil {
		t.Fatal("old passphrase still opens vault after Rotate")
	}
	// New passphrase opens it with every value intact.
	s2, err := Open(path, "new-pass")
	if err != nil {
		t.Fatalf("Open with new pass: %v", err)
	}
	for k, v := range want {
		if got, ok := s2.Get(k); !ok || got != v {
			t.Fatalf("post-rotate Get %s = (%q,%v), want %q", k, got, ok, v)
		}
	}
}

func TestLargeValueRoundTrips(t *testing.T) {
	path := vaultPath(t)
	s, err := Open(path, "pp")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	big := strings.Repeat("A", 256*1024) // 256 KiB, e.g. a PEM bundle
	if err := s.Set("cert", big); err != nil {
		t.Fatalf("Set big: %v", err)
	}
	s2, err := Open(path, "pp")
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if got, _ := s2.Get("cert"); got != big {
		t.Fatalf("big value corrupted: len got %d, want %d", len(got), len(big))
	}
}

// 🔴 TestReloadIfChanged pins a defect MEASURED in production: Get() reads an
// in-memory map loaded once at Open, so a secret written by ANOTHER
// process (`vpsmctl secrets set`, `bin/pve-credencial --apply`) stayed
// invisible to the panel until the next restart. The revocation drill
// recreated a node's token and the panel kept saying "revoked" with the key already
// back in the vault and on the hypervisor.
func TestReloadIfChanged(t *testing.T) {
	path := vaultPath(t)
	const pp = "senha-de-teste"

	painel, err := Open(path, pp)
	if err != nil {
		t.Fatal(err)
	}
	if err := painel.Set("k", "v1"); err != nil {
		t.Fatal(err)
	}

	// No external change: nothing to do, and NO re-read (scrypt is expensive).
	if mudou, err := painel.ReloadIfChanged(); err != nil || mudou {
		t.Fatalf("ReloadIfChanged with no change = (%v, %v), want (false, nil)", mudou, err)
	}

	// Another process writes to the SAME file.
	outro, err := Open(path, pp)
	if err != nil {
		t.Fatal(err)
	}
	if err := outro.Set("externo", "vindo-de-fora"); err != nil {
		t.Fatal(err)
	}

	// The defect: without a reload, the panel cannot see it.
	if _, ok := painel.Get("externo"); ok {
		t.Fatal("the test does not reproduce the defect — the key showed up with no reload")
	}

	mudou, err := painel.ReloadIfChanged()
	if err != nil {
		t.Fatalf("ReloadIfChanged: %v", err)
	}
	if !mudou {
		t.Fatal("an external change was not detected")
	}
	if v, ok := painel.Get("externo"); !ok || v != "vindo-de-fora" {
		t.Fatalf("after the reload: (%q, %v), want the external key", v, ok)
	}
	// And what was already there is not lost.
	if v, ok := painel.Get("k"); !ok || v != "v1" {
		t.Fatalf("the reload lost the local key: (%q, %v)", v, ok)
	}
}

// TestReloadIfChangedNaoRelePorEscritaPropria: our own write changes the
// mtime; if that counted as an external change, every Set would pay a scrypt on the
// next tick, for no gain at all.
func TestReloadIfChangedNaoRelePorEscritaPropria(t *testing.T) {
	path := vaultPath(t)
	s, err := Open(path, "p")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := s.Set("k", "v"); err != nil {
			t.Fatal(err)
		}
		if mudou, err := s.ReloadIfChanged(); err != nil || mudou {
			t.Fatalf("its own write turned into a reload (%v, %v)", mudou, err)
		}
	}
}
