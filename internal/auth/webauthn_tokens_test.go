package auth

import (
	"testing"
)

func newTestService() *Service {
	return New("test-secret-nao-usar-em-producao", nil)
}

// Round-trip: IssueWebAuthnRegToken -> VerifyWebAuthnRegToken returns the
// same username.
func TestWebAuthnRegToken_RoundTrip(t *testing.T) {
	s := newTestService()
	tok, err := s.IssueWebAuthnRegToken("alice")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	got, err := s.VerifyWebAuthnRegToken(tok)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got != "alice" {
		t.Fatalf("username: got %q, want alice", got)
	}
}

// Replay: the SAME registration token used twice must fail the second time
// (single-use / challenge anti-replay — a security requirement).
func TestWebAuthnRegToken_ReplayFails(t *testing.T) {
	s := newTestService()
	tok, err := s.IssueWebAuthnRegToken("alice")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if _, err := s.VerifyWebAuthnRegToken(tok); err != nil {
		t.Fatalf("first verification should pass: %v", err)
	}
	if _, err := s.VerifyWebAuthnRegToken(tok); err == nil {
		t.Fatalf("SECURITY: replay of the same registration token was accepted")
	}
}

// Tamper: any altered bit in the signature invalidates the token.
func TestWebAuthnRegToken_TamperRejected(t *testing.T) {
	s := newTestService()
	tok, err := s.IssueWebAuthnRegToken("alice")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	tampered := tok[:len(tok)-1] + flipLastChar(tok[len(tok)-1:])
	if _, err := s.VerifyWebAuthnRegToken(tampered); err == nil {
		t.Fatalf("SECURITY: tampered token was accepted")
	}
}

// Wrong kind: a token from ANOTHER ceremony (setup) must never be accepted
// as webauthn_reg, even though it is signed with the same secret.
func TestWebAuthnRegToken_WrongKindRejected(t *testing.T) {
	s := newTestService()
	setupTok, err := s.IssueSetupToken("alice", webAuthnCeremonyTTL)
	if err != nil {
		t.Fatalf("IssueSetupToken: %v", err)
	}
	if _, err := s.VerifyWebAuthnRegToken(setupTok); err == nil {
		t.Fatalf("SECURITY: token kind=setup was accepted as webauthn_reg")
	}
}

// Round-trip with payload: IssueWebAuthnRegSessionToken carries username +
// the serialised SessionData; Verify returns both intact.
func TestWebAuthnRegSessionToken_RoundTripWithPayload(t *testing.T) {
	s := newTestService()
	payload := []byte(`{"challenge":"abc123"}`)
	tok, err := s.IssueWebAuthnRegSessionToken("bob", payload)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	username, got, err := s.VerifyWebAuthnRegSessionToken(tok)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if username != "bob" {
		t.Fatalf("username: got %q, want bob", username)
	}
	if string(got) != string(payload) {
		t.Fatalf("payload: got %q, want %q", got, payload)
	}
}

// Replaying the registration session token must fail too.
func TestWebAuthnRegSessionToken_ReplayFails(t *testing.T) {
	s := newTestService()
	tok, err := s.IssueWebAuthnRegSessionToken("bob", []byte("x"))
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if _, _, err := s.VerifyWebAuthnRegSessionToken(tok); err != nil {
		t.Fatalf("first verification should pass: %v", err)
	}
	if _, _, err := s.VerifyWebAuthnRegSessionToken(tok); err == nil {
		t.Fatalf("SECURITY: replay of the registration session token was accepted")
	}
}

// Round-trip + replay for the LOGIN session token (no username).
func TestWebAuthnLoginSessionToken_RoundTripAndReplay(t *testing.T) {
	s := newTestService()
	payload := []byte(`{"challenge":"xyz789"}`)
	tok, err := s.IssueWebAuthnLoginSessionToken(payload)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	got, err := s.VerifyWebAuthnLoginSessionToken(tok)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("payload: got %q, want %q", got, payload)
	}
	if _, err := s.VerifyWebAuthnLoginSessionToken(tok); err == nil {
		t.Fatalf("SECURITY: replay of the login session token was accepted")
	}
}

// Ceremony tokens can never be accepted by ParseWithJTI (the check
// auth.Middleware uses) — kind != "session" is rejected.
func TestWebAuthnCeremonyTokens_NeverAcceptedBySessionParser(t *testing.T) {
	s := newTestService()
	regTok, _ := s.IssueWebAuthnRegToken("alice")
	if _, _, err := s.ParseWithJTI(regTok); err == nil {
		t.Fatalf("SECURITY: webauthn_reg token was accepted as a valid session by ParseWithJTI")
	}
	loginSessTok, _ := s.IssueWebAuthnLoginSessionToken([]byte("x"))
	if _, _, err := s.ParseWithJTI(loginSessTok); err == nil {
		t.Fatalf("SECURITY: webauthn_login_session token was accepted as a valid session by ParseWithJTI")
	}
}

func flipLastChar(c string) string {
	if c == "A" {
		return "B"
	}
	return "A"
}
