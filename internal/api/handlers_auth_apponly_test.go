package api

// handlers_auth_apponly_test.go — the "app-only" gate (config.User.AppOnly).
//
// What these tests prove, and why each one exists:
//
//   - A marked account is REFUSED on the web panel, with the SAME response as
//     a wrong password (byte for byte). If somebody swaps the message for a
//     specific one ("app-only account"), the test breaks — the generic message
//     is a security requirement, not a matter of style: a message of its own
//     would confirm to the attacker that the user exists.
//   - The same account GETS IN through the app (MobileLogin) — the gate must
//     not have closed the door that should stay open.
//   - /recovery/auth (the panel's other way in) also refuses — a gate with a
//     back door is worse than no gate at all.
//   - A normal account keeps getting in through BOTH paths — the gate only
//     acts on whoever carries the mark.
//
// The infrastructure (fakeGoTrue, newLoginTestRouter, doLogin) is the same as
// the characterisation suite in handlers_auth_test.go.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// marcaAppOnly turns the app-only mark on for the test router's account, under
// the same cfgMu the handlers use to read.
func marcaAppOnly(t *testing.T, r *Router, username string) {
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

// TestHandleLogin_AppOnly_RecusadoNoPainel proves the heart of the gate: a
// CORRECT password + a marked account -> 401 with no token, indistinguishable
// from a wrong password.
func TestHandleLogin_AppOnly_RecusadoNoPainel(t *testing.T) {
	gt := newFakeGoTrue()
	gt.addUser(&fakeGoTrueUser{email: "app@test.local", password: testPassword})
	r := newLoginTestRouter(t, gt, "appuser", "app@test.local")
	marcaAppOnly(t, r, "appuser")

	w, out := doLogin(t, r, map[string]any{"username": "appuser", "password": testPassword})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, expected 401; body = %s", w.Code, w.Body.String())
	}
	if _, hasToken := out["token"]; hasToken {
		t.Fatalf("app-only account must not receive a token from the panel: %v", out)
	}

	// Indistinguishability: the response has to equal the wrong-password response
	// of an account that is NOT app-only — otherwise the message becomes an
	// account-enumeration oracle.
	gt2 := newFakeGoTrue()
	gt2.addUser(&fakeGoTrueUser{email: "normal@test.local", password: testPassword})
	r2 := newLoginTestRouter(t, gt2, "normal", "normal@test.local")
	wErrado, _ := doLogin(t, r2, map[string]any{"username": "normal", "password": "senha-errada"})
	if got, want := w.Body.String(), wErrado.Body.String(); got != want {
		t.Fatalf("gate response = %q, wrong password = %q — they must be identical", got, want)
	}
}

// TestHandleLogin_AppOnly_RecusadoTambemPorEmail proves that the gate sits
// AFTER the email→canonical-username normalisation: logging in with the email
// is not a way around it.
func TestHandleLogin_AppOnly_RecusadoTambemPorEmail(t *testing.T) {
	gt := newFakeGoTrue()
	gt.addUser(&fakeGoTrueUser{email: "app@test.local", password: testPassword})
	r := newLoginTestRouter(t, gt, "appuser", "app@test.local")
	marcaAppOnly(t, r, "appuser")

	w, out := doLogin(t, r, map[string]any{"username": "App@Test.Local", "password": testPassword})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, expected 401; body = %s", w.Code, w.Body.String())
	}
	if _, hasToken := out["token"]; hasToken {
		t.Fatalf("login by email bypassed the gate: %v", out)
	}
}

// TestMobileLogin_AppOnly_EntraPeloApp proves that the gate did NOT close the
// app's door: the same account refused above authenticates through MobileLogin.
func TestMobileLogin_AppOnly_EntraPeloApp(t *testing.T) {
	gt := newFakeGoTrue()
	gt.addUser(&fakeGoTrueUser{email: "app@test.local", password: testPassword})
	r := newLoginTestRouter(t, gt, "appuser", "app@test.local")
	marcaAppOnly(t, r, "appuser")

	req := httptest.NewRequest(http.MethodPost, "/api/mobile/auth/login", strings.NewReader("{}"))
	res, err := r.MobileLogin(req, "appuser", testPassword, "", "Pixel de teste")
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

// TestHandleRecoveryAuth_AppOnly_Recusado closes the back door: /recovery is a
// panel entrance (root PTY) and has a credential check of its own.
func TestHandleRecoveryAuth_AppOnly_Recusado(t *testing.T) {
	gt := newFakeGoTrue()
	gt.addUser(&fakeGoTrueUser{email: "app@test.local", password: testPassword})
	r := newLoginTestRouter(t, gt, "appuser", "app@test.local")
	marcaAppOnly(t, r, "appuser")

	body, _ := json.Marshal(map[string]any{"username": "appuser", "password": testPassword, "totp": "123456"})
	req := httptest.NewRequest(http.MethodPost, "/recovery/auth", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, expected 401; body = %s", w.Code, w.Body.String())
	}
	if strings.Contains(strings.ToLower(w.Body.String()), "aplicativo") {
		t.Fatalf("the /recovery response leaked the real reason: %s", w.Body.String())
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == recoveryCookieName && c.Value != "" {
			t.Fatalf("app-only account received a recovery cookie")
		}
	}
}

// TestLogin_ContaNormal_EntraNosDoisCaminhos is the counterweight: without the
// mark, the old behaviour stays intact at both doors.
func TestLogin_ContaNormal_EntraNosDoisCaminhos(t *testing.T) {
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
	res, err := r.MobileLogin(req, "normal", testPassword, "", "Pixel de teste")
	if err != nil {
		t.Fatalf("app: MobileLogin returned an error for normal account: %v", err)
	}
	if res.AccessToken == "" {
		t.Fatalf("app: normal account did not receive access token: %+v", res)
	}
}
