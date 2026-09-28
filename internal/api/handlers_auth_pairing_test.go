package api

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

func TestHandleMobilePairStart_UsesServerDerivedUsername(t *testing.T) {
	r := newSmokeRouter(t)
	r.cfg.PublicHostname = "panel.example.com"
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
	if serverURL, _ := out["server_url"].(string); serverURL != "https://panel.example.com" {
		t.Fatalf("server_url = %q, expected https://panel.example.com", serverURL)
	}
	if qrPNG, _ := out["qr_png"].(string); qrPNG == "" {
		t.Fatalf("expected non-empty \"qr_png\" (base64) field: %v", out)
	}
	for _, forbidden := range []string{"token", "access_token", "refresh_token", "session"} {
		if _, present := out[forbidden]; present {
			t.Fatalf("mobile-pair start returned the forbidden key %q: %v", forbidden, out)
		}
	}

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

func TestConsumePairingTicket_ReplayFails(t *testing.T) {
	r := newSmokeRouter(t)
	r.cfg.PublicHostname = "panel.example.com"
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

func TestConsumePairingTicket_InvalidTicketFails(t *testing.T) {
	r := newSmokeRouter(t)
	if _, err := r.ConsumePairingTicket("ticket-never-issued"); err == nil {
		t.Fatal("a ticket never issued should fail")
	}
}
