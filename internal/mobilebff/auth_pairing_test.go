package mobilebff

// auth_pairing_test.go — HTTP-level proof that POST /auth/pair (the public
// route that consumes the QR-code pairing ticket) never returns anything but
// a reg_token — no "token", "access_token", "session" or similar field may
// appear in a success body, and an invalid/expired/already-consumed ticket
// always fails the same way. It uses the same fakePasskeyBackend as
// auth_passkey_test.go — the proof that the ticket itself is single-use lives
// in internal/auth/pairing_ticket_test.go; the proof here is about the SHAPE
// of this route's HTTP response.

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestMobilePairConsume_Success_ReturnsOnlyRegToken proves the central
// guarantee: even when the backend signals success, the only data field in the
// body is "reg_token" — never a session token.
func TestMobilePairConsume_Success_ReturnsOnlyRegToken(t *testing.T) {
	backend := &fakePasskeyBackend{pairingRegToken: "reg-tok-de-teste"}
	srv := newPasskeyTestServer(t, backend)

	resp, body := postJSON(t, srv, "/auth/pair", map[string]any{"ticket": "qualquer-ticket"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, expected 200: %v", resp.StatusCode, body)
	}
	if body["reg_token"] != "reg-tok-de-teste" {
		t.Fatalf("body[\"reg_token\"] = %v, expected \"reg-tok-de-teste\"", body["reg_token"])
	}
	for _, forbidden := range []string{"token", "access_token", "refresh_token", "session", "jwt"} {
		if _, present := body[forbidden]; present {
			t.Fatalf("auth/pair returned the forbidden key %q in the body: %v — pairing must NEVER turn into a session by itself", forbidden, body)
		}
	}
	if resp.Header.Get("Set-Cookie") != "" {
		t.Fatalf("auth/pair set Set-Cookie (%q) — it should not start any session", resp.Header.Get("Set-Cookie"))
	}
}

// TestMobilePairConsume_InvalidTicketFails proves that a ticket the backend
// rejects (expired, already consumed, or never issued — the real backend does
// not tell the three apart) never produces a success response nor a
// reg_token, not even accidentally.
func TestMobilePairConsume_InvalidTicketFails(t *testing.T) {
	backend := &fakePasskeyBackend{pairingErr: ErrPairingTicketInvalid}
	srv := newPasskeyTestServer(t, backend)

	resp, body := postJSON(t, srv, "/auth/pair", map[string]any{"ticket": "ticket-invalido"})
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("expected an HTTP error for an invalid ticket, got 200: %v", body)
	}
	if _, present := body["reg_token"]; present {
		t.Fatalf("error response leaked a reg_token: %v", body)
	}
}

// TestMobilePairConsume_UnavailableWhenBackendNil mirrors
// TestPasskeyEndpoints_UnavailableWhenBackendNil: with no Passkey backend
// (empty PublicHostname on the real Router), the route degrades to 503 — never
// a silent 404, never a panic.
func TestMobilePairConsume_UnavailableWhenBackendNil(t *testing.T) {
	srv := newPasskeyTestServer(t, nil)
	resp, body := postJSON(t, srv, "/auth/pair", map[string]any{"ticket": "x"})
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, expected 503: %v", resp.StatusCode, body)
	}
}

// TestMobilePairConsumeOutput_BodyShape reflects over pairingConsumeOutput at
// the Go type level: it fails if any field other than "reg_token" exists in
// the Body, even if no handler fills it in yet — catching the regression
// before an HTTP request has to be made at all.
func TestMobilePairConsumeOutput_BodyShape(t *testing.T) {
	var out pairingConsumeOutput
	out.Body.RegToken = "x"
	data, err := json.Marshal(out.Body)
	if err != nil {
		t.Fatalf("marshal Body: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(m) != 1 {
		t.Fatalf("pairingConsumeOutput.Body serializes %d fields (%v), expected exactly 1 (\"reg_token\")", len(m), m)
	}
	if _, ok := m["reg_token"]; !ok {
		t.Fatalf("pairingConsumeOutput.Body has no \"reg_token\" field: %v", m)
	}
}
