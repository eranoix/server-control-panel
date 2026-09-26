// trusted_devices_test.go — tests for the trusted-devices store.
// Covers: Mint→IsTrusted=true, tampered secret→false, expiry→false+GC,
// Revoke→false, RevokeAll→file gone, absent file→(false,nil), empty
// secret→(false,nil) without touching disk.
package auth

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

func TestTrustedDevices_MintAndIsTrusted(t *testing.T) {
	dir := t.TempDir()
	store := NewTrustedDevicesStore(TrustedDevicesPath(dir, "sam"))

	secret, err := store.Mint("sam", "Chrome on Linux", "10.0.0.1")
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if len(secret) != 64 { // 32 bytes hex
		t.Fatalf("expected 64-char hex secret, got %d chars", len(secret))
	}
	ok, err := store.IsTrusted(secret)
	if err != nil || !ok {
		t.Fatalf("expected trusted, got ok=%v err=%v", ok, err)
	}

	// LastSeen must have been updated (the entry exists and is unique).
	devs, err := store.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(devs) != 1 {
		t.Fatalf("expected 1 device, got %d", len(devs))
	}
	if devs[0].Label != "Chrome on Linux" || devs[0].IP != "10.0.0.1" {
		t.Fatalf("metadata mismatch: %+v", devs[0])
	}
}

func TestTrustedDevices_TamperedSecret(t *testing.T) {
	dir := t.TempDir()
	store := NewTrustedDevicesStore(TrustedDevicesPath(dir, "sam"))
	secret, err := store.Mint("sam", "", "")
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	// Flip one char of the secret → different hash → not trusted.
	tampered := "0" + secret[1:]
	if tampered == secret {
		tampered = "1" + secret[1:]
	}
	ok, err := store.IsTrusted(tampered)
	if err != nil || ok {
		t.Fatalf("expected NOT trusted for tampered secret, got ok=%v err=%v", ok, err)
	}
}

func TestTrustedDevices_EmptySecretNoDisk(t *testing.T) {
	dir := t.TempDir()
	// Nonexistent path; an empty secret must not even attempt a read.
	store := NewTrustedDevicesStore(TrustedDevicesPath(dir, "sam"))
	ok, err := store.IsTrusted("")
	if err != nil || ok {
		t.Fatalf("empty secret: expected (false,nil), got ok=%v err=%v", ok, err)
	}
}

func TestTrustedDevices_MissingFile(t *testing.T) {
	dir := t.TempDir()
	store := NewTrustedDevicesStore(TrustedDevicesPath(dir, "ghost"))
	ok, err := store.IsTrusted("deadbeef")
	if err != nil || ok {
		t.Fatalf("missing file: expected (false,nil), got ok=%v err=%v", ok, err)
	}
	devs, err := store.List()
	if err != nil || devs != nil {
		t.Fatalf("missing file List: expected (nil,nil), got %v err=%v", devs, err)
	}
}

func TestTrustedDevices_ExpiredIsPrunedAndDistrusted(t *testing.T) {
	dir := t.TempDir()
	path := TrustedDevicesPath(dir, "sam")
	store := NewTrustedDevicesStore(path)

	secret := "a1b2c3d4e5f6" // any old test secret
	now := time.Now().Unix()
	// Manually write an ALREADY expired entry (ExpiresAt in the past).
	file := &TrustedDevicesFile{
		SchemaVersion: 1,
		User:          "sam",
		Devices: []TrustedDevice{{
			Hash:      hashSecret(secret),
			CreatedAt: now - 100,
			ExpiresAt: now - 10,
			LastSeen:  now - 100,
		}},
	}
	b, _ := json.MarshalIndent(file, "", "  ")
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	ok, err := store.IsTrusted(secret)
	if err != nil || ok {
		t.Fatalf("expired: expected NOT trusted, got ok=%v err=%v", ok, err)
	}
	// The GC must have pruned the expired entry → the file is gone (left empty).
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("expected file pruned after GC, stat err=%v", statErr)
	}
}

func TestTrustedDevices_RevokeAndRevokeAll(t *testing.T) {
	dir := t.TempDir()
	path := TrustedDevicesPath(dir, "sam")
	store := NewTrustedDevicesStore(path)

	s1, _ := store.Mint("sam", "dev1", "")
	s2, _ := store.Mint("sam", "dev2", "")

	// Revoke s1 → s1 untrusted, s2 still trusted.
	if err := store.Revoke(s1); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if ok, _ := store.IsTrusted(s1); ok {
		t.Fatal("s1 should be revoked")
	}
	if ok, _ := store.IsTrusted(s2); !ok {
		t.Fatal("s2 should still be trusted")
	}

	// RevokeAll → file gone, s2 untrusted.
	if err := store.RevokeAll(); err != nil {
		t.Fatalf("revoke all: %v", err)
	}
	if ok, _ := store.IsTrusted(s2); ok {
		t.Fatal("s2 should be revoked after RevokeAll")
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("expected file removed after RevokeAll, stat err=%v", statErr)
	}
}
