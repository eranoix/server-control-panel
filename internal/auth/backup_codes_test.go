// backup_codes_test.go — tests for the MFA backup-code generator/consumer.
// Covers: generation with a valid count, consistent hashing, single-use
// enforcement, counting unused, idempotent delete.
package auth

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateBackupCodes_CountAndUniqueness(t *testing.T) {
	codes, file, err := GenerateBackupCodes("sam", 10)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(codes) != 10 {
		t.Fatalf("expected 10 codes, got %d", len(codes))
	}
	if len(file.Codes) != 10 {
		t.Fatalf("expected 10 hashed entries, got %d", len(file.Codes))
	}
	if file.User != "sam" {
		t.Fatalf("expected user=sam, got %q", file.User)
	}
	// Each code must be unique.
	seen := map[string]bool{}
	for _, c := range codes {
		if seen[c] {
			t.Fatalf("duplicate code generated: %s", c)
		}
		seen[c] = true
		// Codes must have a sane shape (non-empty, length > 6).
		if len(strings.TrimSpace(c)) < 6 {
			t.Fatalf("code suspiciously short: %q", c)
		}
	}
	// Hashes in the file must be distinct.
	hseen := map[string]bool{}
	for _, h := range file.Codes {
		if hseen[h.Hash] {
			t.Fatalf("duplicate hash: %s", h.Hash)
		}
		hseen[h.Hash] = true
		if h.Used {
			t.Fatal("newly generated code marked as used")
		}
	}
}

func TestBackupCodesStore_SaveLoadConsume(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mfa-backup-codes-sam.json")
	store := NewBackupCodesStore(path)

	codes, file, err := GenerateBackupCodes("sam", 5)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if err := store.Save(file); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Load round-trip.
	loaded, err := store.Load()
	if err != nil || loaded == nil {
		t.Fatalf("load: file=%v err=%v", loaded, err)
	}
	if loaded.User != "sam" || len(loaded.Codes) != 5 {
		t.Fatalf("loaded mismatch: %+v", loaded)
	}

	// CountUnused == 5 before consuming.
	if n, err := store.CountUnused(); err != nil || n != 5 {
		t.Fatalf("CountUnused: %d err=%v (expected 5)", n, err)
	}

	// Consume one.
	ok, err := store.TryConsume(codes[0])
	if err != nil {
		t.Fatalf("TryConsume: %v", err)
	}
	if !ok {
		t.Fatal("TryConsume returned false for a valid code")
	}

	// The same call again must fail (single-use).
	ok2, err := store.TryConsume(codes[0])
	if err != nil {
		t.Fatalf("TryConsume(replay): %v", err)
	}
	if ok2 {
		t.Fatal("TryConsume accepted an already-used code — replay bug")
	}

	// CountUnused == 4 now.
	if n, _ := store.CountUnused(); n != 4 {
		t.Fatalf("CountUnused after consume: %d (expected 4)", n)
	}

	// An invalid code fails.
	ok3, _ := store.TryConsume("XXXX-INVALID")
	if ok3 {
		t.Fatal("TryConsume accepted a random code — validation bug")
	}

	// Delete is idempotent.
	if err := store.Delete(); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := store.Delete(); err != nil {
		t.Fatalf("delete again should be idempotent, got: %v", err)
	}
	// Load after delete returns nil/nil.
	loaded2, err := store.Load()
	if err != nil {
		t.Fatalf("load after delete: %v", err)
	}
	if loaded2 != nil {
		t.Fatalf("expected nil after delete, got %+v", loaded2)
	}
}

func TestBackupCodesPath_Format(t *testing.T) {
	got := BackupCodesPath("/opt/panel/data", "sam")
	want := "/opt/panel/data/mfa-backup-codes-sam.json"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
