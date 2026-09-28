package api

import (
	"net/http/httptest"
	"testing"

	"server-control-panel/internal/mobilebff"
)

func TestMobileLogin_PasswordOK_NoMFA(t *testing.T) {
	gt := newFakeGoTrue()
	gt.addUser(&fakeGoTrueUser{email: "sam@test.local", password: testPassword})
	r := newLoginTestRouter(t, gt, "sam", "sam@test.local")

	req := httptest.NewRequest("POST", "/api/mobile/v1/auth/login", nil)
	res, err := r.MobileLogin(req, "sam", testPassword, "", "Test Pixel")
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

func TestMobileLogin_PasswordWrong(t *testing.T) {
	gt := newFakeGoTrue()
	gt.addUser(&fakeGoTrueUser{email: "sam@test.local", password: testPassword})
	r := newLoginTestRouter(t, gt, "sam", "sam@test.local")

	req := httptest.NewRequest("POST", "/api/mobile/v1/auth/login", nil)
	_, err := r.MobileLogin(req, "sam", "wrong-password", "", "")
	if err != mobilebff.ErrMobileLoginInvalidCredentials {
		t.Fatalf("err = %v, expected ErrMobileLoginInvalidCredentials", err)
	}
}

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

func TestMobileRefresh_RotatesAndInvalidatesOldToken(t *testing.T) {
	gt := newFakeGoTrue()
	gt.addUser(&fakeGoTrueUser{email: "sam@test.local", password: testPassword})
	r := newLoginTestRouter(t, gt, "sam", "sam@test.local")

	req := httptest.NewRequest("POST", "/api/mobile/v1/auth/login", nil)
	login, err := r.MobileLogin(req, "sam", testPassword, "", "Test Pixel")
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

	if _, err := r.MobileRefresh(login.RefreshToken); err != mobilebff.ErrMobileRefreshInvalid {
		t.Fatalf("replay of the old token: err = %v, expected ErrMobileRefreshInvalid", err)
	}
	if _, err := r.MobileRefresh(login.RefreshToken); err != mobilebff.ErrMobileRefreshInvalid {
		t.Fatalf("second replay: err = %v, expected ErrMobileRefreshInvalid", err)
	}
}

func TestMobileRefresh_UnknownToken_Invalid(t *testing.T) {
	gt := newFakeGoTrue()
	gt.addUser(&fakeGoTrueUser{email: "sam@test.local", password: testPassword})
	r := newLoginTestRouter(t, gt, "sam", "sam@test.local")

	if _, err := r.MobileRefresh("sam.never-issued"); err != mobilebff.ErrMobileRefreshInvalid {
		t.Fatalf("err = %v, expected ErrMobileRefreshInvalid", err)
	}
	if _, err := r.MobileRefresh(""); err != mobilebff.ErrMobileRefreshInvalid {
		t.Fatalf("empty token: err = %v, expected ErrMobileRefreshInvalid", err)
	}
}
