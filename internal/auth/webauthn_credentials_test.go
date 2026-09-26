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

// 1. Round-trip through a "restart" of the store: written with one instance,
// read back with another pointing at the same file.
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
		t.Fatalf("credencial persistida perdeu o blob webauthn.Credential")
	}
}

// 2. CredentialByID never leaks between users — each user has their own
// file/store, and looking up A's ID in B's store finds nothing.
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

// 3. Add always starts out "pending": excluded from List(), present in ListAll().
func TestWebAuthnCredentialsStore_AddDefaultsPending(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alice.json")
	store := NewWebAuthnCredentialsStore(path)

	rec, err := store.Add(testCred(2), "novo dispositivo")
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

// 4. Approve flips the status; rejects an unknown/cross-user ID without
// mutating the file.
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

	if err := storeA.Approve("id-que-nao-existe"); err == nil {
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

// 5. Remove deletes regardless of status (pending or approved).
func TestWebAuthnCredentialsStore_RemoveRegardlessOfStatus(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alice.json")
	store := NewWebAuthnCredentialsStore(path)

	pending, err := store.Add(testCred(4), "pendente")
	if err != nil {
		t.Fatalf("Add pending: %v", err)
	}
	approvedRec, err := store.Add(testCred(5), "aprovado")
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

	if err := store.Remove("inexistente"); err == nil {
		t.Fatalf("expected an error when removing a nonexistent ID")
	}
}

// 6. UpdateCredential refreshes the blob (e.g. SignCount) while preserving status.
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

// 7. List/ListAll/CredentialByID on a file that never existed return
// empty/not-found WITHOUT an error — never an os.ErrNotExist handed back to the
// caller. This is the property that underpins user-enumeration resistance in
// FinishPasskeyLogin (internal/api/passkey.go): an unknown userHandle opens a
// nonexistent file and lands on exactly the same path ("no credentials") as a
// real user with no approved credentials — there is no distinct error behind
// the two cases for an attacker to observe.
func TestWebAuthnCredentialsStore_MissingFileBehavesAsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nao-existe", "fantasma.json")
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

	_, ok, err := store.CredentialByID("qualquer-id")
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
