package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func markAppOnly(t *testing.T, r *Router, username string) {
	t.Helper()
	r.cfgMu.Lock()
	defer r.cfgMu.Unlock()
	for i := range r.cfg.Users {
		if r.cfg.Users[i].Username == username {
			r.cfg.Users[i].AppOnly = true
			return
		}
	}
	t.Fatalf("user %q is not in the test router's config", username)
}

func TestHandleLogin_AppOnly_RejectedOnPanel(t *testing.T) {
	gt := newFakeGoTrue()
	gt.addUser(&fakeGoTrueUser{email: "app@test.local", password: testPassword})
	r := newLoginTestRouter(t, gt, "appuser", "app@test.local")
	markAppOnly(t, r, "appuser")

	w, out := doLogin(t, r, map[string]any{"username": "appuser", "password": testPassword})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, expected 401; body = %s", w.Code, w.Body.String())
	}
	if _, hasToken := out["token"]; hasToken {
		t.Fatalf("app-only account must not receive a token from the panel: %v", out)
	}

	gt2 := newFakeGoTrue()
	gt2.addUser(&fakeGoTrueUser{email: "normal@test.local", password: testPassword})
	r2 := newLoginTestRouter(t, gt2, "normal", "normal@test.local")
	wWrong, _ := doLogin(t, r2, map[string]any{"username": "normal", "password": "wrong-password"})
	if got, want := w.Body.String(), wWrong.Body.String(); got != want {
		t.Fatalf("gate response = %q, wrong password = %q — they must be identical", got, want)
	}
}

func TestHandleLogin_AppOnly_AlsoRejectedByEmail(t *testing.T) {
	gt := newFakeGoTrue()
	gt.addUser(&fakeGoTrueUser{email: "app@test.local", password: testPassword})
	r := newLoginTestRouter(t, gt, "appuser", "app@test.local")
	markAppOnly(t, r, "appuser")

	w, out := doLogin(t, r, map[string]any{"username": "App@Test.Local", "password": testPassword})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, expected 401; body = %s", w.Code, w.Body.String())
	}
	if _, hasToken := out["token"]; hasToken {
		t.Fatalf("login by email bypassed the gate: %v", out)
	}
}

func TestMobileLogin_AppOnly_EntersThroughApp(t *testing.T) {
	gt := newFakeGoTrue()
	gt.addUser(&fakeGoTrueUser{email: "app@test.local", password: testPassword})
	r := newLoginTestRouter(t, gt, "appuser", "app@test.local")
	markAppOnly(t, r, "appuser")

	req := httptest.NewRequest(http.MethodPost, "/api/mobile/auth/login", strings.NewReader("{}"))
	res, err := r.MobileLogin(req, "appuser", testPassword, "", "Test Pixel")
	if err != nil {
		t.Fatalf("MobileLogin returned an error for app-only account: %v", err)
	}
	if res.AccessToken == "" {
		t.Fatalf("app did not receive access token: %+v", res)
	}
	if res.RefreshToken == "" {
		t.Fatalf("app did not receive refresh token: %+v", res)
	}
}

func TestHandleRecoveryAuth_AppOnly_Rejected(t *testing.T) {
	gt := newFakeGoTrue()
	gt.addUser(&fakeGoTrueUser{email: "app@test.local", password: testPassword})
	r := newLoginTestRouter(t, gt, "appuser", "app@test.local")
	markAppOnly(t, r, "appuser")

	body, _ := json.Marshal(map[string]any{"username": "appuser", "password": testPassword, "totp": "123456"})
	req := httptest.NewRequest(http.MethodPost, "/recovery/auth", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, expected 401; body = %s", w.Code, w.Body.String())
	}
	if strings.Contains(strings.ToLower(w.Body.String()), "app-only") {
		t.Fatalf("the /recovery response leaked the real reason: %s", w.Body.String())
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == recoveryCookieName && c.Value != "" {
			t.Fatalf("app-only account received a recovery cookie")
		}
	}
}

func TestLogin_NormalAccount_EntersBothPaths(t *testing.T) {
	gt := newFakeGoTrue()
	gt.addUser(&fakeGoTrueUser{email: "normal@test.local", password: testPassword})
	r := newLoginTestRouter(t, gt, "normal", "normal@test.local")

	w, out := doLogin(t, r, map[string]any{"username": "normal", "password": testPassword})
	if w.Code != http.StatusOK {
		t.Fatalf("panel: status = %d, body = %s", w.Code, w.Body.String())
	}
	if tok, _ := out["token"].(string); tok == "" {
		t.Fatalf("panel: normal account did not receive token: %v", out)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/mobile/auth/login", strings.NewReader("{}"))
	res, err := r.MobileLogin(req, "normal", testPassword, "", "Test Pixel")
	if err != nil {
		t.Fatalf("app: MobileLogin returned an error for normal account: %v", err)
	}
	if res.AccessToken == "" {
		t.Fatalf("app: normal account did not receive access token: %+v", res)
	}
}
