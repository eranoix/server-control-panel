package mobilebff

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

func TestPasskeyRegisterFinish_NeverReturnsToken(t *testing.T) {
	backend := &fakePasskeyBackend{}
	srv := newPasskeyTestServer(t, backend)

	resp, body := postJSON(t, srv, "/auth/passkey/register/finish", map[string]any{
		"continuation_token": "anything",
		"label":              "Test Pixel",
		"credential":         json.RawMessage(`{"id":"abc"}`),
	})

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, expected 200 (%v)", resp.StatusCode, body)
	}
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

func TestPasskeyLoginFinish_Success_ReturnsAccessAndRefreshToken(t *testing.T) {
	backend := &fakePasskeyBackend{
		finishLoginResult: MobileLoginResult{
			AccessToken:  "test-jwt",
			RefreshToken: "sam.test-refresh",
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
	if body["access_token"] != "test-jwt" {
		t.Fatalf("body[\"access_token\"] = %v, expected \"test-jwt\"", body["access_token"])
	}
	if body["refresh_token"] != "sam.test-refresh" {
		t.Fatalf("body[\"refresh_token\"] = %v, expected \"sam.test-refresh\"", body["refresh_token"])
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

func TestPasskeyRegisterFinish_UnwrappedBackendError_NeverLeaksServerPath(t *testing.T) {
	leakedPath := "/opt/panel/data/users/sam/webauthn/credentials.json"
	underlying := &os.PathError{Op: "open", Path: leakedPath, Err: errors.New("no such file or directory")}
	backend := &fakePasskeyBackend{
		finishRegErr: fmt.Errorf("passkey: reading approved credentials: %w", underlying),
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

func TestPasskeyLoginBegin_RateLimited(t *testing.T) {
	backend := &fakePasskeyBackend{allowLogin: false}
	srv := newPasskeyTestServer(t, backend)

	resp, body := postJSON(t, srv, "/auth/passkey/login/begin", map[string]any{})
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, expected 429: %v", resp.StatusCode, body)
	}
}

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
