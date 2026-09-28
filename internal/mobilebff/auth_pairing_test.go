package mobilebff

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestMobilePairConsume_Success_ReturnsOnlyRegToken(t *testing.T) {
	backend := &fakePasskeyBackend{pairingRegToken: "test-reg-tok"}
	srv := newPasskeyTestServer(t, backend)

	resp, body := postJSON(t, srv, "/auth/pair", map[string]any{"ticket": "any-ticket"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, expected 200: %v", resp.StatusCode, body)
	}
	if body["reg_token"] != "test-reg-tok" {
		t.Fatalf("body[\"reg_token\"] = %v, expected \"test-reg-tok\"", body["reg_token"])
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

func TestMobilePairConsume_InvalidTicketFails(t *testing.T) {
	backend := &fakePasskeyBackend{pairingErr: ErrPairingTicketInvalid}
	srv := newPasskeyTestServer(t, backend)

	resp, body := postJSON(t, srv, "/auth/pair", map[string]any{"ticket": "invalid-ticket"})
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("expected an HTTP error for an invalid ticket, got 200: %v", body)
	}
	if _, present := body["reg_token"]; present {
		t.Fatalf("error response leaked a reg_token: %v", body)
	}
}

func TestMobilePairConsume_UnavailableWhenBackendNil(t *testing.T) {
	srv := newPasskeyTestServer(t, nil)
	resp, body := postJSON(t, srv, "/auth/pair", map[string]any{"ticket": "x"})
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, expected 503: %v", resp.StatusCode, body)
	}
}

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
