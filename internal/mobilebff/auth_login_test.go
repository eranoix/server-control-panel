package mobilebff

import (
	"net/http"
	"testing"
)

func TestMobileLogin_Success_ReturnsTokens(t *testing.T) {
	backend := &fakePasskeyBackend{
		mobileLoginResult: MobileLoginResult{
			AccessToken:  "access-123",
			RefreshToken: "sam.refresh-abc",
			ExpiresIn:    3600,
		},
	}
	srv := newPasskeyTestServer(t, backend)

	resp, body := postJSON(t, srv, "/auth/login", map[string]any{
		"username":     "sam",
		"password":     "hunter2",
		"device_label": "Test Pixel",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, expected 200: %v", resp.StatusCode, body)
	}
	if body["access_token"] != "access-123" {
		t.Fatalf("access_token = %v, expected \"access-123\"", body["access_token"])
	}
	if body["refresh_token"] != "sam.refresh-abc" {
		t.Fatalf("refresh_token = %v", body["refresh_token"])
	}
	if _, present := body["totp_required"]; present {
		t.Fatalf("success should not have totp_required: %v", body)
	}
	if len(backend.mobileLoginCalls) != 1 {
		t.Fatalf("MobileLogin called %d times, expected 1", len(backend.mobileLoginCalls))
	}
	call := backend.mobileLoginCalls[0]
	if call.username != "sam" || call.password != "hunter2" || call.deviceLabel != "Test Pixel" {
		t.Fatalf("call forwarded incorrectly: %+v", call)
	}
}

func TestMobileLogin_TOTPRequired_NeverReturnsToken(t *testing.T) {
	backend := &fakePasskeyBackend{
		mobileLoginResult: MobileLoginResult{TOTPRequired: true},
	}
	srv := newPasskeyTestServer(t, backend)

	resp, body := postJSON(t, srv, "/auth/login", map[string]any{
		"username": "sam",
		"password": "hunter2",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, expected 200: %v", resp.StatusCode, body)
	}
	if body["totp_required"] != true {
		t.Fatalf("totp_required = %v, expected true", body["totp_required"])
	}
	if _, present := body["access_token"]; present {
		t.Fatalf("totp_required=true should not have access_token: %v", body)
	}
	if _, present := body["refresh_token"]; present {
		t.Fatalf("totp_required=true should not have refresh_token: %v", body)
	}
}

func TestMobileLogin_ErrorMapping(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
	}{
		{"invalid_credentials", ErrMobileLoginInvalidCredentials, http.StatusUnauthorized},
		{"locked", ErrMobileLoginLocked, http.StatusLocked},
		{"rate_limited", ErrMobileLoginRateLimited, http.StatusTooManyRequests},
		{"mfa_unavailable", ErrMobileLoginMFAUnavailable, http.StatusServiceUnavailable},
		{"invalid_code", ErrMobileLoginInvalidCode, http.StatusUnauthorized},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			backend := &fakePasskeyBackend{mobileLoginErr: c.err}
			srv := newPasskeyTestServer(t, backend)
			resp, body := postJSON(t, srv, "/auth/login", map[string]any{
				"username": "sam",
				"password": "wrong",
			})
			if resp.StatusCode != c.status {
				t.Fatalf("status = %d, expected %d: %v", resp.StatusCode, c.status, body)
			}
		})
	}
}

func TestMobileRefresh_Success_ReturnsNewPair(t *testing.T) {
	backend := &fakePasskeyBackend{
		mobileRefreshResult: MobileLoginResult{
			AccessToken:  "access-new",
			RefreshToken: "sam.refresh-new",
			ExpiresIn:    3600,
		},
	}
	srv := newPasskeyTestServer(t, backend)

	resp, body := postJSON(t, srv, "/auth/refresh", map[string]any{
		"refresh_token": "sam.refresh-old",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, expected 200: %v", resp.StatusCode, body)
	}
	if body["access_token"] != "access-new" {
		t.Fatalf("access_token = %v", body["access_token"])
	}
	if body["refresh_token"] != "sam.refresh-new" {
		t.Fatalf("refresh_token = %v, expected the NEW token (rotation)", body["refresh_token"])
	}
}

func TestMobileRefresh_InvalidOrReused_Always401(t *testing.T) {
	backend := &fakePasskeyBackend{mobileRefreshErr: ErrMobileRefreshInvalid}
	srv := newPasskeyTestServer(t, backend)

	resp, body := postJSON(t, srv, "/auth/refresh", map[string]any{
		"refresh_token": "sam.already-rotated",
	})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, expected 401: %v", resp.StatusCode, body)
	}

	resp2, body2 := postJSON(t, srv, "/auth/refresh", map[string]any{
		"refresh_token": "sam.already-rotated",
	})
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("second attempt: status = %d, expected 401: %v", resp2.StatusCode, body2)
	}
}
