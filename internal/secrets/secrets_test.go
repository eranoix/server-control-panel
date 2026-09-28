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

func vaultPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "secrets.vault")
}

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

	s2, err := Open(path, "correct horse battery staple")
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if v, ok := s2.Get("api_key"); !ok || v != "s3cr3t" {
		t.Fatalf("after reopen Get = (%q,%v), want (s3cr3t,true)", v, ok)
	}

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

func TestLegacyVaultMigratesSalt(t *testing.T) {
	path := vaultPath(t)
	const pp = "legacy-pass"
	writeLegacyVault(t, path, pp, map[string]string{"a": "1", "b": "2"})

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

	if err := s.Set("c", "3"); err != nil {
		t.Fatalf("Set (triggers migration): %v", err)
	}
	if ff := readFileFormat(t, path); ff.Salt == "" {
		t.Fatal("after save, salt field still empty — migration did not run")
	}

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

	if _, err := Open(path, "old-pass"); err == nil {
		t.Fatal("old passphrase still opens vault after Rotate")
	}
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
	big := strings.Repeat("A", 256*1024)
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

func TestReloadIfChanged(t *testing.T) {
	path := vaultPath(t)
	const pp = "test-password"

	panel, err := Open(path, pp)
	if err != nil {
		t.Fatal(err)
	}
	if err := panel.Set("k", "v1"); err != nil {
		t.Fatal(err)
	}

	if changed, err := panel.ReloadIfChanged(); err != nil || changed {
		t.Fatalf("ReloadIfChanged with no change = (%v, %v), want (false, nil)", changed, err)
	}

	other, err := Open(path, pp)
	if err != nil {
		t.Fatal(err)
	}
	if err := other.Set("external", "from-outside"); err != nil {
		t.Fatal(err)
	}

	if _, ok := panel.Get("external"); ok {
		t.Fatal("the test does not reproduce the defect — the key showed up with no reload")
	}

	changed, err := panel.ReloadIfChanged()
	if err != nil {
		t.Fatalf("ReloadIfChanged: %v", err)
	}
	if !changed {
		t.Fatal("an external change was not detected")
	}
	if v, ok := panel.Get("external"); !ok || v != "from-outside" {
		t.Fatalf("after the reload: (%q, %v), want the external key", v, ok)
	}
	if v, ok := panel.Get("k"); !ok || v != "v1" {
		t.Fatalf("the reload lost the local key: (%q, %v)", v, ok)
	}
}

func TestReloadIfChangedSkipsOwnWrite(t *testing.T) {
	path := vaultPath(t)
	s, err := Open(path, "p")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := s.Set("k", "v"); err != nil {
			t.Fatal(err)
		}
		if changed, err := s.ReloadIfChanged(); err != nil || changed {
			t.Fatalf("its own write turned into a reload (%v, %v)", changed, err)
		}
	}
}
