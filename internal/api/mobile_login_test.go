package api

// mobile_login_test.go — proves that Router.MobileLogin/MobileRefresh apply
// EXACTLY the same MFA policy as handleLogin (verifyLoginMFA) — it reuses the
// SAME newLoginTestRouter/fakeGoTrue from handlers_auth_test.go, the SAME cases
// (no MFA, MFA required without a code, correct code, wrong code), only through
// the mobile path — and it proves the rotation/reuse of the mobile refresh
// token.

import (
	"net/http/httptest"
	"testing"

	"server-control-panel/internal/mobilebff"
)

// TestMobileLogin_PasswordOK_NoMFA espelha TestHandleLogin_PasswordOK_NoMFA:
// senha correta, sem factor enrolado -> tokens emitidos direto.
func TestMobileLogin_PasswordOK_NoMFA(t *testing.T) {
	gt := newFakeGoTrue()
	gt.addUser(&fakeGoTrueUser{email: "sam@test.local", password: testPassword})
	r := newLoginTestRouter(t, gt, "sam", "sam@test.local")

	req := httptest.NewRequest("POST", "/api/mobile/v1/auth/login", nil)
	res, err := r.MobileLogin(req, "sam", testPassword, "", "Pixel de teste")
	if err != nil {
		t.Fatalf("MobileLogin: %v", err)
	}
	if res.AccessToken == "" {
		t.Fatal("expected access_token, got empty")
	}
	if res.RefreshToken == "" {
		t.Fatal("expected refresh_token, got empty")
	}
	if res.TOTPRequired {
		t.Fatal("should not require TOTP — user has no enrolled factor")
	}
}

// TestMobileLogin_PasswordWrong espelha TestHandleLogin_PasswordWrong.
func TestMobileLogin_PasswordWrong(t *testing.T) {
	gt := newFakeGoTrue()
	gt.addUser(&fakeGoTrueUser{email: "sam@test.local", password: testPassword})
	r := newLoginTestRouter(t, gt, "sam", "sam@test.local")

	req := httptest.NewRequest("POST", "/api/mobile/v1/auth/login", nil)
	_, err := r.MobileLogin(req, "sam", "senha-errada", "", "")
	if err != mobilebff.ErrMobileLoginInvalidCredentials {
		t.Fatalf("err = %v, expected ErrMobileLoginInvalidCredentials", err)
	}
}

// TestMobileLogin_MFARequired_NoCode mirrors
// TestHandleLogin_MFARequired_NoTrustedDevice_NoCode: the SAME MFA policy as
// the desktop, applied to the mobile path — the app has no trusted-device
// cookie, so it ALWAYS lands on this branch when a factor is enrolled and no
// code was sent.
func TestMobileLogin_MFARequired_NoCode(t *testing.T) {
	gt := newFakeGoTrue()
	gt.addUser(&fakeGoTrueUser{
		email: "sam@test.local", password: testPassword,
		factorID: "factor-1", factorType: "totp", factorOK: "123456",
	})
	r := newLoginTestRouter(t, gt, "sam", "sam@test.local")

	req := httptest.NewRequest("POST", "/api/mobile/v1/auth/login", nil)
	res, err := r.MobileLogin(req, "sam", testPassword, "", "")
	if err != nil {
		t.Fatalf("MobileLogin: %v", err)
	}
	if !res.TOTPRequired {
		t.Fatal("expected TOTPRequired=true")
	}
	if res.AccessToken != "" || res.RefreshToken != "" {
		t.Fatal("TOTPRequired=true should not come with any token")
	}
}

// TestMobileLogin_MFACorrectCode espelha TestHandleLogin_MFACorrectCode.
func TestMobileLogin_MFACorrectCode(t *testing.T) {
	gt := newFakeGoTrue()
	gt.addUser(&fakeGoTrueUser{
		email: "sam@test.local", password: testPassword,
		factorID: "factor-1", factorType: "totp", factorOK: "123456",
	})
	r := newLoginTestRouter(t, gt, "sam", "sam@test.local")

	req := httptest.NewRequest("POST", "/api/mobile/v1/auth/login", nil)
	res, err := r.MobileLogin(req, "sam", testPassword, "123456", "")
	if err != nil {
		t.Fatalf("MobileLogin: %v", err)
	}
	if res.AccessToken == "" {
		t.Fatal("expected access_token with the correct code")
	}
}

// TestMobileLogin_MFAWrongCode_NoValidBackup espelha
// TestHandleLogin_MFAWrongCode_NoValidBackup.
func TestMobileLogin_MFAWrongCode_NoValidBackup(t *testing.T) {
	gt := newFakeGoTrue()
	gt.addUser(&fakeGoTrueUser{
		email: "sam@test.local", password: testPassword,
		factorID: "factor-1", factorType: "totp", factorOK: "123456",
	})
	r := newLoginTestRouter(t, gt, "sam", "sam@test.local")

	req := httptest.NewRequest("POST", "/api/mobile/v1/auth/login", nil)
	_, err := r.MobileLogin(req, "sam", testPassword, "000000", "")
	if err != mobilebff.ErrMobileLoginInvalidCode {
		t.Fatalf("err = %v, expected ErrMobileLoginInvalidCode", err)
	}
}

// TestMobileRefresh_RotatesAndInvalidatesOldToken proves the rotation rule: the
// refresh token returned by the login rotates successfully ONCE; reusing the
// OLD token afterwards (replay) always fails with ErrMobileRefreshInvalid —
// it never "almost works" on a second attempt.
func TestMobileRefresh_RotatesAndInvalidatesOldToken(t *testing.T) {
	gt := newFakeGoTrue()
	gt.addUser(&fakeGoTrueUser{email: "sam@test.local", password: testPassword})
	r := newLoginTestRouter(t, gt, "sam", "sam@test.local")

	req := httptest.NewRequest("POST", "/api/mobile/v1/auth/login", nil)
	login, err := r.MobileLogin(req, "sam", testPassword, "", "Pixel de teste")
	if err != nil {
		t.Fatalf("MobileLogin: %v", err)
	}

	refreshed, err := r.MobileRefresh(login.RefreshToken)
	if err != nil {
		t.Fatalf("MobileRefresh: %v", err)
	}
	if refreshed.AccessToken == "" || refreshed.RefreshToken == "" {
		t.Fatal("MobileRefresh should return a new access+refresh pair")
	}
	if refreshed.RefreshToken == login.RefreshToken {
		t.Fatal("refresh_token did not rotate — returned the same token")
	}

	// Replay of the ORIGINAL refresh_token (already rotated) — it must always fail.
	if _, err := r.MobileRefresh(login.RefreshToken); err != mobilebff.ErrMobileRefreshInvalid {
		t.Fatalf("replay of the old token: err = %v, expected ErrMobileRefreshInvalid", err)
	}
	// Again, to prove it does not "unlock" on a second attempt.
	if _, err := r.MobileRefresh(login.RefreshToken); err != mobilebff.ErrMobileRefreshInvalid {
		t.Fatalf("second replay: err = %v, expected ErrMobileRefreshInvalid", err)
	}
}

// TestMobileRefresh_UnknownToken_Invalid proves that a refresh_token that was
// never issued fails the SAME way as one already rotated — without telling the
// two cases apart for the caller (which avoids a token/user enumeration oracle).
func TestMobileRefresh_UnknownToken_Invalid(t *testing.T) {
	gt := newFakeGoTrue()
	gt.addUser(&fakeGoTrueUser{email: "sam@test.local", password: testPassword})
	r := newLoginTestRouter(t, gt, "sam", "sam@test.local")

	if _, err := r.MobileRefresh("sam.nunca-emitido"); err != mobilebff.ErrMobileRefreshInvalid {
		t.Fatalf("err = %v, expected ErrMobileRefreshInvalid", err)
	}
	if _, err := r.MobileRefresh(""); err != mobilebff.ErrMobileRefreshInvalid {
		t.Fatalf("empty token: err = %v, expected ErrMobileRefreshInvalid", err)
	}
}
