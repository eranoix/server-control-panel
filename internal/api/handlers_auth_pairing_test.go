package api

// handlers_auth_pairing_test.go — proves handleMobilePairStart (the protected
// mint of the pairing ticket, called from an already authenticated desktop
// session) and Router.ConsumePairingTicket (the real implementation the mobile
// BFF calls via mobilebff.PasskeyBackend.ConsumePairingTicket).
//
// Properties under test:
//  1. handleMobilePairStart 401s without auth.UserFrom filled in.
//  2. The ticket's username ALWAYS comes from auth.UserFrom(req) — never from
//     a field in the request body (this route does not even accept a body).
//  3. ConsumePairingTicket, end to end from the ticket issued by
//     handleMobilePairStart, returns a usable reg_token (accepted by
//     VerifyWebAuthnRegToken) for the SAME username — and no session token
//     appears anywhere in the chain.
//  4. Replay of the same ticket (a second call to ConsumePairingTicket) always
//     fails — it never "almost works" on a second attempt.

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"server-control-panel/internal/auth"
)

func TestHandleMobilePairStart_RequiresAuth(t *testing.T) {
	r := newSmokeRouter(t)
	req := httptest.NewRequest("POST", "/api/auth/mobile-pair", nil)
	w := httptest.NewRecorder()
	r.handleMobilePairStart(w, req)
	if w.Code != 401 {
		t.Fatalf("status = %d, expected 401 without auth.UserFrom", w.Code)
	}
}

// TestHandleMobilePairStart_RequiresPublicHostname proves that QR pairing fails
// explicitly (503) instead of minting an envelope with an empty
// server_url when the server has no PublicHostname configured —
// a mute QR (with no destination server) would be worse than no QR at all.
func TestHandleMobilePairStart_RequiresPublicHostname(t *testing.T) {
	r := newSmokeRouter(t)
	req := httptest.NewRequest("POST", "/api/auth/mobile-pair", nil)
	req = req.WithContext(auth.WithUser(req.Context(), "sam"))
	w := httptest.NewRecorder()
	r.handleMobilePairStart(w, req)
	if w.Code != 503 {
		t.Fatalf("status = %d, expected 503 without PublicHostname configured: %s", w.Code, w.Body.String())
	}
}

// TestHandleMobilePairStart_UsesServerDerivedUsername proves that the minted
// ticket carries the SESSION's username (auth.UserFrom), never a value the
// client can inject — this route reads no body at all, so there is not even
// a "username" field for a malicious client to try minting a ticket for
// ANOTHER user.
func TestHandleMobilePairStart_UsesServerDerivedUsername(t *testing.T) {
	r := newSmokeRouter(t)
	r.cfg.PublicHostname = "vpsm.example.com"
	req := httptest.NewRequest("POST", "/api/auth/mobile-pair", nil)
	req = req.WithContext(auth.WithUser(req.Context(), "sam"))
	w := httptest.NewRecorder()
	r.handleMobilePairStart(w, req)
	if w.Code != 200 {
		t.Fatalf("status = %d, expected 200: %s", w.Code, w.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	ticket, _ := out["ticket"].(string)
	if ticket == "" {
		t.Fatalf("expected non-empty \"ticket\" field: %v", out)
	}
	if serverURL, _ := out["server_url"].(string); serverURL != "https://vpsm.example.com" {
		t.Fatalf("server_url = %q, expected https://vpsm.example.com", serverURL)
	}
	if qrPNG, _ := out["qr_png"].(string); qrPNG == "" {
		t.Fatalf("expected non-empty \"qr_png\" (base64) field: %v", out)
	}
	for _, forbidden := range []string{"token", "access_token", "refresh_token", "session"} {
		if _, present := out[forbidden]; present {
			t.Fatalf("mobile-pair start returned the forbidden key %q: %v", forbidden, out)
		}
	}

	// End to end: the ticket issued for "sam" can only become a reg_token
	// bound to "sam" — never to another user, and never to a session.
	regToken, err := r.ConsumePairingTicket(ticket)
	if err != nil {
		t.Fatalf("ConsumePairingTicket: %v", err)
	}
	if regToken == "" {
		t.Fatal("reg_token empty")
	}
	gotUser, verr := r.auth.VerifyWebAuthnRegToken(regToken)
	if verr != nil {
		t.Fatalf("returned reg_token is not a valid webauthn_reg token: %v", verr)
	}
	if gotUser != "sam" {
		t.Fatalf("reg_token is bound to %q, expected \"sam\"", gotUser)
	}
}

// TestConsumePairingTicket_ReplayFails proves that the ticket cannot be
// exchanged for a SECOND reg_token — not even inside the validity window.
func TestConsumePairingTicket_ReplayFails(t *testing.T) {
	r := newSmokeRouter(t)
	r.cfg.PublicHostname = "vpsm.example.com"
	req := httptest.NewRequest("POST", "/api/auth/mobile-pair", nil)
	req = req.WithContext(auth.WithUser(req.Context(), "sam"))
	w := httptest.NewRecorder()
	r.handleMobilePairStart(w, req)

	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	ticket := out["ticket"].(string)

	if _, err := r.ConsumePairingTicket(ticket); err != nil {
		t.Fatalf("first consume should have succeeded: %v", err)
	}
	if _, err := r.ConsumePairingTicket(ticket); err == nil {
		t.Fatal("replay of an already-consumed ticket should fail")
	}
}

// TestConsumePairingTicket_InvalidTicketFails proves that a ticket that was
// never issued (e.g. guessed, or leaked from another session) always fails — it
// never returns a reg_token.
func TestConsumePairingTicket_InvalidTicketFails(t *testing.T) {
	r := newSmokeRouter(t)
	if _, err := r.ConsumePairingTicket("ticket-nunca-emitido"); err == nil {
		t.Fatal("a ticket never issued should fail")
	}
}
