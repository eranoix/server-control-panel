package auth

// mobile_sessions_test.go — proves the guarantees of MobileRefreshStore:
// Mint/Rotate/Revoke do a correct round trip, a
// swapped (rotated) token is IMMEDIATELY invalid — never "almost
// works" on a second attempt — and Revoke/RevokeAll really do revoke.

import (
	"path/filepath"
	"testing"
)

func TestMobileSession_MintRotateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store := NewMobileRefreshStore(filepath.Join(dir, "mobile-sessions-sam.json"))

	tok, err := store.Mint("sam", "Pixel de teste")
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if tok == "" {
		t.Fatal("Mint returned an empty token")
	}

	newTok, user, ok, err := store.Rotate(tok)
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if !ok {
		t.Fatal("Rotate should have succeeded for the freshly issued token")
	}
	if user != "sam" {
		t.Fatalf("username = %q, expected \"sam\"", user)
	}
	if newTok == "" || newTok == tok {
		t.Fatalf("new token invalid or same as the old one: %q", newTok)
	}
}

// TestMobileSession_RotatedAwaySecretInvalid proves it: once rotated, the OLD
// token never works again — not for a second Rotate, and not by repeating the
// same attempt.
func TestMobileSession_RotatedAwaySecretInvalid(t *testing.T) {
	dir := t.TempDir()
	store := NewMobileRefreshStore(filepath.Join(dir, "mobile-sessions-sam.json"))

	oldTok, err := store.Mint("sam", "Pixel de teste")
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if _, _, ok, err := store.Rotate(oldTok); err != nil || !ok {
		t.Fatalf("first Rotate should have succeeded: ok=%v err=%v", ok, err)
	}

	// Replaying the old (already rotated) token — must always fail.
	if _, _, ok, err := store.Rotate(oldTok); err != nil {
		t.Fatalf("Rotate of the old token should not be an IO error: %v", err)
	} else if ok {
		t.Fatal("Rotate of the already-rotated (old) token should have failed, but it succeeded")
	}
}

func TestMobileSession_RotateUnknownTokenFails(t *testing.T) {
	dir := t.TempDir()
	store := NewMobileRefreshStore(filepath.Join(dir, "mobile-sessions-sam.json"))

	if _, _, ok, err := store.Rotate("sam.nunca-emitido-token-falso"); err != nil {
		t.Fatalf("unexpected IO error: %v", err)
	} else if ok {
		t.Fatal("a token that was never issued should not rotate successfully")
	}
	if _, _, ok, _ := store.Rotate(""); ok {
		t.Fatal("an empty token should not rotate successfully")
	}
	if _, _, ok, _ := store.Rotate("sem-ponto-nenhum"); ok {
		t.Fatal("a malformed token (without a username prefix) should not rotate successfully")
	}
}

func TestMobileSession_RevokeInvalidatesToken(t *testing.T) {
	dir := t.TempDir()
	store := NewMobileRefreshStore(filepath.Join(dir, "mobile-sessions-sam.json"))

	tok, err := store.Mint("sam", "Pixel de teste")
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if err := store.Revoke(tok); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if _, _, ok, err := store.Rotate(tok); err != nil {
		t.Fatalf("unexpected IO error: %v", err)
	} else if ok {
		t.Fatal("a revoked token still rotates successfully")
	}
}

func TestMobileSession_RevokeAllRemovesEverySession(t *testing.T) {
	dir := t.TempDir()
	store := NewMobileRefreshStore(filepath.Join(dir, "mobile-sessions-sam.json"))

	tok1, _ := store.Mint("sam", "Pixel")
	tok2, _ := store.Mint("sam", "iPhone")

	if err := store.RevokeAll(); err != nil {
		t.Fatalf("RevokeAll: %v", err)
	}
	for _, tok := range []string{tok1, tok2} {
		if _, _, ok, _ := store.Rotate(tok); ok {
			t.Fatalf("token %q still works after RevokeAll", tok)
		}
	}
	sessions, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("List returned %d sessions after RevokeAll, expected 0", len(sessions))
	}
}

func TestMobileSession_ListReturnsActiveSessions(t *testing.T) {
	dir := t.TempDir()
	store := NewMobileRefreshStore(filepath.Join(dir, "mobile-sessions-sam.json"))

	if _, err := store.Mint("sam", "Pixel de teste"); err != nil {
		t.Fatalf("Mint: %v", err)
	}
	sessions, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("List returned %d sessions, expected 1", len(sessions))
	}
	if sessions[0].DeviceLabel != "Pixel de teste" {
		t.Fatalf("DeviceLabel = %q, expected \"Pixel de teste\"", sessions[0].DeviceLabel)
	}
	if sessions[0].Hash == "" {
		t.Fatal("Hash empty — the secret should not be stored in the clear, nor be absent")
	}
}

func TestParseMobileRefreshUsername(t *testing.T) {
	cases := []struct {
		token    string
		wantUser string
		wantOK   bool
	}{
		{"sam.deadbeef", "sam", true},
		{"", "", false},
		{"semponto", "", false},
		{".semusuario", "", false},
		{"sam.", "", false},
	}
	for _, c := range cases {
		user, ok := ParseMobileRefreshUsername(c.token)
		if ok != c.wantOK || (ok && user != c.wantUser) {
			t.Errorf("ParseMobileRefreshUsername(%q) = (%q, %v), expected (%q, %v)", c.token, user, ok, c.wantUser, c.wantOK)
		}
	}
}
