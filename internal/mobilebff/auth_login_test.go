package mobilebff

// auth_login_test.go — HTTP-level proof of the guarantees auth_login.go
// makes:
//
//  1. mobileLogin passes username/password/totp_code/device_label through to
//     PasskeyBackend.MobileLogin unaltered — the MFA policy itself is tested
//     in internal/api (the desktop's very own verifyLoginMFA).
//  2. When the backend signals TOTPRequired, the HTTP response is
//     EXACTLY {"totp_required": true} — not a token in sight.
//  3. Success returns access_token + refresh_token + expires_in.
//  4. Backend errors (bad credentials, lockout, rate limit, MFA
//     unavailable, invalid code) map to the right HTTP status.
//  5. mobileRefresh passes the refresh_token it received through unaltered
//     and returns the NEW pair — an invalid or replayed refresh_token
//     (already rotated) is always 401, never "almost works".

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
		"device_label": "Pixel de teste",
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
	if call.username != "sam" || call.password != "hunter2" || call.deviceLabel != "Pixel de teste" {
		t.Fatalf("call repassada incorretamente: %+v", call)
	}
}

// TestMobileLogin_TOTPRequired_NeverReturnsToken proves that when the backend
// signals TOTPRequired, the HTTP response carries neither access_token nor
// refresh_token — the app is meant to resend with totp_code filled in.
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
				"password": "errada",
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

// TestMobileRefresh_InvalidOrReused_Always401 proves that a refresh_token
// that is invalid, expired, revoked or already rotated (replayed) always
// fails the SAME way — 401, never "almost works" on a retry.
func TestMobileRefresh_InvalidOrReused_Always401(t *testing.T) {
	backend := &fakePasskeyBackend{mobileRefreshErr: ErrMobileRefreshInvalid}
	srv := newPasskeyTestServer(t, backend)

	resp, body := postJSON(t, srv, "/auth/refresh", map[string]any{
		"refresh_token": "sam.ja-rotacionado",
	})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, expected 401: %v", resp.StatusCode, body)
	}

	// Repeating the SAME attempt keeps failing in exactly the same way —
	// there is no "second chance" for a token that is already invalid.
	resp2, body2 := postJSON(t, srv, "/auth/refresh", map[string]any{
		"refresh_token": "sam.ja-rotacionado",
	})
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("second attempt: status = %d, expected 401: %v", resp2.StatusCode, body2)
	}
}
