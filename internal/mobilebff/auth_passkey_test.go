package mobilebff

// auth_passkey_test.go — HTTP-level proof (not just ceremony-token level) of
// auth_passkey.go's two non-negotiable security guarantees:
//
//  1. register/finish NEVER returns a session token/credential — success is
//     ALWAYS {"status":"pending_approval"}, byte for byte.
//  2. login/finish tells "pending credential" apart from "success" by HTTP
//     status and by body, but NEVER issues a token in the pending case.
//
// It uses a fake PasskeyBackend (fakePasskeyBackend) — the minimal slice of
// the interface the package exposes — so as not to depend on a
// cryptographically valid WebAuthn ceremony (that is the job of
// internal/api/passkey_test.go and internal/auth/webauthn_tokens_test.go).
// What this file proves is the SHAPE of the HTTP response: given that the
// backend (of any implementation, real or fake) signalled success, this
// layer's handler is structurally incapable of leaking a token along the
// registration path.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

type fakePasskeyBackend struct {
	beginRegOpts []byte
	beginRegCont string
	beginRegErr  error

	finishRegErr error

	beginLoginOpts []byte
	beginLoginCont string
	beginLoginErr  error

	finishLoginResult MobileLoginResult
	finishLoginErr    error

	allowLogin bool

	pairingRegToken string
	pairingErr      error

	mobileLoginResult MobileLoginResult
	mobileLoginErr    error
	mobileLoginCalls  []mobileLoginCall

	mobileRefreshResult MobileLoginResult
	mobileRefreshErr    error
}

type mobileLoginCall struct {
	username, password, totpCode, deviceLabel string
}

func (f *fakePasskeyBackend) BeginPasskeyRegistration(string) ([]byte, string, error) {
	return f.beginRegOpts, f.beginRegCont, f.beginRegErr
}

func (f *fakePasskeyBackend) FinishPasskeyRegistration(string, string, []byte) error {
	return f.finishRegErr
}

func (f *fakePasskeyBackend) BeginPasskeyLogin() ([]byte, string, error) {
	return f.beginLoginOpts, f.beginLoginCont, f.beginLoginErr
}

func (f *fakePasskeyBackend) FinishPasskeyLogin(string, []byte, string, string) (MobileLoginResult, error) {
	return f.finishLoginResult, f.finishLoginErr
}

func (f *fakePasskeyBackend) AllowLoginAttempt(string) bool { return f.allowLogin }
func (f *fakePasskeyBackend) ResetLoginAttempts(string)     {}

func (f *fakePasskeyBackend) ConsumePairingTicket(string) (string, error) {
	return f.pairingRegToken, f.pairingErr
}

func (f *fakePasskeyBackend) MobileLogin(_ *http.Request, username, password, totpCode, deviceLabel string) (MobileLoginResult, error) {
	f.mobileLoginCalls = append(f.mobileLoginCalls, mobileLoginCall{username, password, totpCode, deviceLabel})
	return f.mobileLoginResult, f.mobileLoginErr
}

func (f *fakePasskeyBackend) MobileRefresh(string) (MobileLoginResult, error) {
	return f.mobileRefreshResult, f.mobileRefreshErr
}

func newPasskeyTestServer(t *testing.T, backend PasskeyBackend) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	MountPublic(mux, Deps{Passkey: backend})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func postJSON(t *testing.T, srv *httptest.Server, path string, body any) (*http.Response, map[string]any) {
	t.Helper()
	buf, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	resp, err := http.Post(srv.URL+Prefix+path, "application/json", bytes.NewReader(buf))
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var out map[string]any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("unmarshal body %q: %v", raw, err)
		}
	}
	return resp, out
}

// TestPasskeyRegisterFinish_NeverReturnsToken is the required proof: even when
// the backend signals success (FinishPasskeyRegistration returns nil), the HTTP
// success body is EXACTLY {"status":"pending_approval"}
// — no "token", "credential", "session" or any other key may show up, because
// passkeyRegisterFinishOutput.Body has no field capable of carrying them (see
// the shape assertion below). This test fails the day someone adds a token
// field to that type AND the handler starts filling it in.
func TestPasskeyRegisterFinish_NeverReturnsToken(t *testing.T) {
	backend := &fakePasskeyBackend{}
	srv := newPasskeyTestServer(t, backend)

	resp, body := postJSON(t, srv, "/auth/passkey/register/finish", map[string]any{
		"continuation_token": "qualquer-coisa",
		"label":              "Pixel de teste",
		"credential":         json.RawMessage(`{"id":"abc"}`),
	})

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, expected 200 (%v)", resp.StatusCode, body)
	}
	// "$schema" is a self-discovery link huma itself injects into every
	// response body (DefaultConfig) — it is not part of passkeyRegisterFinishOutput.Body
	// and carries no credential data; it is ignored in this count.
	delete(body, "$schema")
	if len(body) != 1 {
		t.Fatalf("body has %d keys besides \"$schema\", expected exactly 1 (\"status\"): %v", len(body), body)
	}
	status, ok := body["status"].(string)
	if !ok || status != "pending_approval" {
		t.Fatalf("body[\"status\"] = %v, expected \"pending_approval\"", body["status"])
	}
	for _, forbidden := range []string{"token", "credential", "session", "jwt", "access_token", "refresh_token"} {
		if _, present := body[forbidden]; present {
			t.Fatalf("register/finish returned the forbidden key %q in the body: %v", forbidden, body)
		}
	}
	if resp.Header.Get("Set-Cookie") != "" {
		t.Fatalf("register/finish set Set-Cookie (%q) — registration must never start a session", resp.Header.Get("Set-Cookie"))
	}
}

// TestPasskeyRegisterFinishOutput_BodyShape is a second layer of the same
// proof, now at the Go TYPE level: it reflects over passkeyRegisterFinishOutput
// and fails if any field other than "status" exists in the Body. That catches a
// regression even earlier — before an HTTP request is ever made — if someone
// adds a token field to the Output struct, even when no handler fills it in
// yet.
func TestPasskeyRegisterFinishOutput_BodyShape(t *testing.T) {
	var out passkeyRegisterFinishOutput
	data, err := json.Marshal(out.Body)
	if err != nil {
		t.Fatalf("marshal zero-value Body: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(m) != 1 {
		t.Fatalf("passkeyRegisterFinishOutput.Body serializes %d fields (%v), expected exactly 1 (\"status\")", len(m), m)
	}
	if _, ok := m["status"]; !ok {
		t.Fatalf("passkeyRegisterFinishOutput.Body has no \"status\" field: %v", m)
	}
}

// TestPasskeyRegisterFinish_BackendErrorNeverLeaksToken confirms that even a
// buggy backend (one that returned an error) produces no success body — the
// error response goes through huma's standard envelope, which likewise has no
// way to carry a token.
func TestPasskeyRegisterFinish_BackendErrorNeverLeaksToken(t *testing.T) {
	backend := &fakePasskeyBackend{finishRegErr: ErrPasskeyInvalidCredential}
	srv := newPasskeyTestServer(t, backend)

	resp, body := postJSON(t, srv, "/auth/passkey/register/finish", map[string]any{
		"continuation_token": "x",
		"credential":         json.RawMessage(`{}`),
	})
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("expected an HTTP error, got 200: %v", body)
	}
	if _, present := body["token"]; present {
		t.Fatalf("error response leaked a \"token\" field: %v", body)
	}
}

// TestPasskeyLoginFinish_PendingApproval_NoToken proves the "login" half of the
// same guarantee: a credential that exists but has not been approved yet NEVER
// gets a token — only the stable error code "pending_approval".
func TestPasskeyLoginFinish_PendingApproval_NoToken(t *testing.T) {
	backend := &fakePasskeyBackend{finishLoginErr: ErrPasskeyPendingApproval, allowLogin: true}
	srv := newPasskeyTestServer(t, backend)

	resp, body := postJSON(t, srv, "/auth/passkey/login/finish", map[string]any{
		"continuation_token": "x",
		"credential":         json.RawMessage(`{}`),
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, expected 403: %v", resp.StatusCode, body)
	}
	if body["error"] != "pending_approval" {
		t.Fatalf("body[\"error\"] = %v, expected \"pending_approval\"", body["error"])
	}
	for _, forbidden := range []string{"token", "access_token", "refresh_token"} {
		if _, present := body[forbidden]; present {
			t.Fatalf("pending login/finish returned the key %q: %v", forbidden, body)
		}
	}
}

// TestPasskeyLoginFinish_Success_ReturnsAccessAndRefreshToken is the positive
// control: it proves the absence of a token in the tests above is a real
// distinction (backend signals pending vs success), not a bug that always omits
// the field — and it proves the unified response shape: access_token +
// refresh_token + expires_in, the SAME shape /auth/login (auth_login.go) uses.
// Passkey is the product's primary path and has to issue the same token pair as
// the password fallback, or the app would have to handle two different shapes
// for the same piece of data.
func TestPasskeyLoginFinish_Success_ReturnsAccessAndRefreshToken(t *testing.T) {
	backend := &fakePasskeyBackend{
		finishLoginResult: MobileLoginResult{
			AccessToken:  "jwt-de-teste",
			RefreshToken: "sam.refresh-de-teste",
			ExpiresIn:    43200,
		},
		allowLogin: true,
	}
	srv := newPasskeyTestServer(t, backend)

	resp, body := postJSON(t, srv, "/auth/passkey/login/finish", map[string]any{
		"continuation_token": "x",
		"credential":         json.RawMessage(`{}`),
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, expected 200: %v", resp.StatusCode, body)
	}
	if body["access_token"] != "jwt-de-teste" {
		t.Fatalf("body[\"access_token\"] = %v, expected \"jwt-de-teste\"", body["access_token"])
	}
	if body["refresh_token"] != "sam.refresh-de-teste" {
		t.Fatalf("body[\"refresh_token\"] = %v, expected \"sam.refresh-de-teste\"", body["refresh_token"])
	}
	if body["expires_in"] != float64(43200) {
		t.Fatalf("body[\"expires_in\"] = %v, expected 43200", body["expires_in"])
	}
	if _, present := body["token"]; present {
		t.Fatalf("unified response should not have the legacy \"token\" field: %v", body)
	}
	if _, present := body["error"]; present {
		t.Fatalf("success should not have an \"error\" field: %v", body)
	}
}

// TestPasskeyRegisterFinish_UnwrappedBackendError_NeverLeaksServerPath proves
// the wrapped-error rule on passkey's PUBLIC (unauthenticated) surface: when the
// real backend (internal/api/passkey.go) returns a wrapped error of the form
// fmt.Errorf("passkey: ...: %w", err) — none of mapPasskeyError's known
// sentinels, so it lands in the `default` branch — the absolute server path
// carried by the *PathError inside that wrap must not show up in the JSON body
// returned to the client. Before the fix, mapPasskeyError(default) called
// huma.Error400BadRequest(err.Error()), echoing the whole error.
func TestPasskeyRegisterFinish_UnwrappedBackendError_NeverLeaksServerPath(t *testing.T) {
	leakedPath := "/opt/panel/data/users/sam/webauthn/credentials.json"
	underlying := &os.PathError{Op: "open", Path: leakedPath, Err: errors.New("no such file or directory")}
	backend := &fakePasskeyBackend{
		finishRegErr: fmt.Errorf("passkey: lendo credenciais aprovadas: %w", underlying),
	}
	srv := newPasskeyTestServer(t, backend)

	resp, body := postJSON(t, srv, "/auth/passkey/register/finish", map[string]any{
		"continuation_token": "x",
		"credential":         json.RawMessage(`{}`),
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, expected 400: %v", resp.StatusCode, body)
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	bodyStr := string(raw)
	if strings.Contains(bodyStr, leakedPath) || strings.Contains(bodyStr, "no such file") {
		t.Fatalf("error response leaked an internal server path/detail: %s", bodyStr)
	}
}

// TestPasskeyLoginBegin_RateLimited confirms that login/begin honours
// AllowLoginAttempt BEFORE calling the real backend.
func TestPasskeyLoginBegin_RateLimited(t *testing.T) {
	backend := &fakePasskeyBackend{allowLogin: false}
	srv := newPasskeyTestServer(t, backend)

	resp, body := postJSON(t, srv, "/auth/passkey/login/begin", map[string]any{})
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, expected 429: %v", resp.StatusCode, body)
	}
}

// TestPasskeyEndpoints_UnavailableWhenBackendNil confirms graceful degradation
// (empty Config.PublicHostname -> nil Router.webauthnRP -> nil Deps.Passkey):
// all four routes answer 503, never a panic, never a silent 404 that would make
// the app think passkeys do not exist.
func TestPasskeyEndpoints_UnavailableWhenBackendNil(t *testing.T) {
	srv := newPasskeyTestServer(t, nil)

	cases := []struct {
		path string
		body map[string]any
	}{
		{"/auth/passkey/register/begin", map[string]any{"reg_token": "x"}},
		{"/auth/passkey/register/finish", map[string]any{"continuation_token": "x", "credential": json.RawMessage(`{}`)}},
		{"/auth/passkey/login/begin", map[string]any{}},
		{"/auth/passkey/login/finish", map[string]any{"continuation_token": "x", "credential": json.RawMessage(`{}`)}},
	}
	for _, c := range cases {
		resp, body := postJSON(t, srv, c.path, c.body)
		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("%s: status = %d, expected 503: %v", c.path, resp.StatusCode, body)
		}
	}
}
