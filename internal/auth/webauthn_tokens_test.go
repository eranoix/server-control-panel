package auth

import (
	"encoding/base64"
	"strings"
	"testing"
)

func newTestService() *Service {
	return New("test-secret-do-not-use-in-production", nil)
}

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

func TestWebAuthnRegToken_TamperRejected(t *testing.T) {
	s := newTestService()
	tok, err := s.IssueWebAuthnRegToken("alice")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	tampered := flipSignatureBit(t, tok)
	if _, err := s.VerifyWebAuthnRegToken(tampered); err == nil {
		t.Fatalf("SECURITY: tampered token was accepted")
	}
}

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

func flipSignatureBit(t *testing.T, tok string) string {
	t.Helper()
	dot := strings.LastIndexByte(tok, '.')
	if dot < 0 {
		t.Fatalf("token has no signature segment: %q", tok)
	}
	sig, err := base64.RawURLEncoding.DecodeString(tok[dot+1:])
	if err != nil || len(sig) == 0 {
		t.Fatalf("decode signature: %v", err)
	}
	sig[0] ^= 0x01
	return tok[:dot+1] + base64.RawURLEncoding.EncodeToString(sig)
}
