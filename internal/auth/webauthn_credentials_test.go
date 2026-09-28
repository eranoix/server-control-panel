package auth

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/go-webauthn/webauthn/webauthn"
)

func testCred(id byte) webauthn.Credential {
	return webauthn.Credential{
		ID:        []byte{id, id, id},
		PublicKey: []byte{0x01, 0x02, 0x03},
	}
}

func TestWebAuthnCredentialsStore_RoundTripAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alice.json")
	cred := testCred(1)

	store1 := NewWebAuthnCredentialsStore(path)
	rec, err := store1.Add(cred, "Pixel 8")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := store1.Approve(rec.ID); err != nil {
		t.Fatalf("Approve: %v", err)
	}

	store2 := NewWebAuthnCredentialsStore(path)
	list, err := store2.List()
	if err != nil {
		t.Fatalf("List (restart): %v", err)
	}
	if len(list) != 1 || list[0].ID != rec.ID {
		t.Fatalf("expected 1 approved credential surviving the restart, got %+v", list)
	}
	if list[0].Credential.PublicKey == nil {
		t.Fatalf("persisted credential lost the webauthn.Credential blob")
	}
}

func TestWebAuthnCredentialsStore_NoCrossUserLeak(t *testing.T) {
	dir := t.TempDir()
	storeA := NewWebAuthnCredentialsStore(filepath.Join(dir, "alice.json"))
	storeB := NewWebAuthnCredentialsStore(filepath.Join(dir, "bob.json"))

	recA, err := storeA.Add(testCred(9), "A's device")
	if err != nil {
		t.Fatalf("Add A: %v", err)
	}

	if _, err := storeB.Add(testCred(7), "B's device"); err != nil {
		t.Fatalf("Add B: %v", err)
	}

	if _, ok, err := storeB.CredentialByID(recA.ID); err != nil {
		t.Fatalf("CredentialByID on B: %v", err)
	} else if ok {
		t.Fatalf("SECURITY: store B saw A's credential (ID %s)", recA.ID)
	}

	if _, ok, err := storeA.CredentialByID(recA.ID); err != nil || !ok {
		t.Fatalf("store A should see its own credential: ok=%v err=%v", ok, err)
	}
}

func TestWebAuthnCredentialsStore_AddDefaultsPending(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alice.json")
	store := NewWebAuthnCredentialsStore(path)

	rec, err := store.Add(testCred(2), "new device")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if rec.Status != CredentialStatusPending {
		t.Fatalf("expected status pending, got %q", rec.Status)
	}

	approved, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(approved) != 0 {
		t.Fatalf("pending credential must NOT appear in List(), got %+v", approved)
	}

	all, err := store.ListAll()
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(all) != 1 || all[0].ID != rec.ID {
		t.Fatalf("pending credential should appear in ListAll(), got %+v", all)
	}
}

func TestWebAuthnCredentialsStore_ApproveRejectsUnknownWithoutMutation(t *testing.T) {
	dir := t.TempDir()
	storeA := NewWebAuthnCredentialsStore(filepath.Join(dir, "alice.json"))
	storeB := NewWebAuthnCredentialsStore(filepath.Join(dir, "bob.json"))

	recA, err := storeA.Add(testCred(3), "A's device")
	if err != nil {
		t.Fatalf("Add A: %v", err)
	}

	if err := storeB.Approve(recA.ID); err == nil {
		t.Fatalf("SECURITY: store B was able to approve A's credential")
	}
	if allB, _ := storeB.ListAll(); len(allB) != 0 {
		t.Fatalf("invalid Approve should not create/mutate anything in store B: %+v", allB)
	}

	if err := storeA.Approve("missing-id"); err == nil {
		t.Fatalf("expected an error when approving an unknown ID")
	}
	allA, err := storeA.ListAll()
	if err != nil || len(allA) != 1 || allA[0].Status != CredentialStatusPending {
		t.Fatalf("Approve with an unknown ID should not mutate the real record: %+v err=%v", allA, err)
	}

	if err := storeA.Approve(recA.ID); err != nil {
		t.Fatalf("valid Approve failed: %v", err)
	}
	allA, _ = storeA.ListAll()
	if allA[0].Status != CredentialStatusApproved || allA[0].ApprovedAt == nil {
		t.Fatalf("Approve did not persist status/ApprovedAt: %+v", allA[0])
	}
}

func TestWebAuthnCredentialsStore_RemoveRegardlessOfStatus(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alice.json")
	store := NewWebAuthnCredentialsStore(path)

	pending, err := store.Add(testCred(4), "pending")
	if err != nil {
		t.Fatalf("Add pending: %v", err)
	}
	approvedRec, err := store.Add(testCred(5), "approved")
	if err != nil {
		t.Fatalf("Add approved: %v", err)
	}
	if err := store.Approve(approvedRec.ID); err != nil {
		t.Fatalf("Approve: %v", err)
	}

	if err := store.Remove(pending.ID); err != nil {
		t.Fatalf("Remove pending: %v", err)
	}
	if err := store.Remove(approvedRec.ID); err != nil {
		t.Fatalf("Remove approved: %v", err)
	}

	all, err := store.ListAll()
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(all) != 0 {
		t.Fatalf("expected an empty store after removing both, got %+v", all)
	}

	if err := store.Remove("nonexistent"); err == nil {
		t.Fatalf("expected an error when removing a nonexistent ID")
	}
}

func TestWebAuthnCredentialsStore_UpdateCredentialPreservesStatus(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alice.json")
	store := NewWebAuthnCredentialsStore(path)

	rec, err := store.Add(testCred(6), "device")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := store.Approve(rec.ID); err != nil {
		t.Fatalf("Approve: %v", err)
	}

	updated := rec.Credential
	updated.Authenticator.SignCount = 42
	if err := store.UpdateCredential(updated); err != nil {
		t.Fatalf("UpdateCredential: %v", err)
	}

	got, ok, err := store.CredentialByID(rec.ID)
	if err != nil || !ok {
		t.Fatalf("CredentialByID after update: ok=%v err=%v", ok, err)
	}
	if got.Status != CredentialStatusApproved {
		t.Fatalf("UpdateCredential should not change status: %+v", got)
	}
	if got.Credential.Authenticator.SignCount != 42 {
		t.Fatalf("SignCount was not persisted: %+v", got.Credential.Authenticator)
	}
}

func TestWebAuthnCredentialsStore_MissingFileBehavesAsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "ghost.json")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("precondition failed: %q already exists", path)
	}
	store := NewWebAuthnCredentialsStore(path)

	approved, err := store.List()
	if err != nil {
		t.Fatalf("List on a nonexistent file returned an error (should be silent): %v", err)
	}
	if len(approved) != 0 {
		t.Fatalf("List on a nonexistent file should be empty, got %+v", approved)
	}

	all, err := store.ListAll()
	if err != nil {
		t.Fatalf("ListAll on a nonexistent file returned an error (should be silent): %v", err)
	}
	if len(all) != 0 {
		t.Fatalf("ListAll on a nonexistent file should be empty, got %+v", all)
	}

	_, ok, err := store.CredentialByID("any-id")
	if err != nil {
		t.Fatalf("CredentialByID on a nonexistent file returned an error (should be silent): %v", err)
	}
	if ok {
		t.Fatalf("CredentialByID on a nonexistent file should not find anything")
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("read queries should not create the file: %v", err)
	}
}
