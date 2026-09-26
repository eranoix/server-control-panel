package api

// handlers_auth_test.go — characterisation of handleLogin.
//
// Pins the OBSERVABLE behaviour of handleLogin against a fake GoTrue (fake
// Supabase), BEFORE any code extraction (verifyLoginMFA). The same suite runs
// unchanged before and after the extraction — if any of these cases changes
// result, the extraction introduced a regression in the production login path.
//
// Covers the branches of the reference flow: password ok/wrong, MFA required
// without a trusted device, MFA skipped via a trusted device, Supabase TOTP
// correct/incorrect, backup-code fallback.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/config"
)

// fakeGoTrueUser is a user's state in the fake GoTrue.
type fakeGoTrueUser struct {
	email      string
	password   string
	factorID   string
	factorType string // "totp" when MFA is enrolled; empty when it is not
	factorOK   string // the TOTP code the verify accepts as correct
}

// fakeGoTrue emula o subconjunto do GoTrue self-hosted usado por
// VerifyDetailed + supabaseGetUserMFA + supabaseChallengeAndVerify:
// POST /auth/v1/token (password), GET /auth/v1/user, POST
// /auth/v1/factors/{id}/challenge, POST /auth/v1/factors/{id}/verify.
type fakeGoTrue struct {
	mu    sync.Mutex
	users map[string]*fakeGoTrueUser // por email
}

func newFakeGoTrue() *fakeGoTrue {
	return &fakeGoTrue{users: make(map[string]*fakeGoTrueUser)}
}

func (f *fakeGoTrue) addUser(u *fakeGoTrueUser) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.users[u.email] = u
}

// accessToken is deterministic and reversible — "tok-"+email — enough for the
// fake to resolve the user without implementing real JWT.
func accessTokenFor(email string) string { return "tok-" + email }

func emailFromAccessToken(tok string) string { return strings.TrimPrefix(tok, "tok-") }

func (f *fakeGoTrue) userByToken(tok string) (*fakeGoTrueUser, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.users[emailFromAccessToken(tok)]
	return u, ok
}

func (f *fakeGoTrue) server(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/v1/token", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		u, ok := f.users[body.Email]
		f.mu.Unlock()
		if !ok || u.password != body.Password {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error_code": "invalid_credentials"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  accessTokenFor(u.email),
			"refresh_token": "refresh-" + u.email,
			"expires_in":    3600,
		})
	})
	mux.HandleFunc("/auth/v1/user", func(w http.ResponseWriter, r *http.Request) {
		tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		u, ok := f.userByToken(tok)
		if !ok {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		factors := []map[string]string{}
		if u.factorType != "" {
			factors = append(factors, map[string]string{
				"id":            u.factorID,
				"factor_type":   u.factorType,
				"status":        "verified",
				"friendly_name": "totp",
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      u.email,
			"email":   u.email,
			"factors": factors,
		})
	})
	mux.HandleFunc("/auth/v1/factors/", func(w http.ResponseWriter, r *http.Request) {
		// paths: /auth/v1/factors/{id}/challenge ou /verify
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/auth/v1/factors/"), "/")
		if len(parts) != 2 {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		factorID, action := parts[0], parts[1]
		tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		u, ok := f.userByToken(tok)
		if !ok || u.factorID != factorID {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch action {
		case "challenge":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "chal-" + factorID, "expires_at": 9999999999})
		case "verify":
			var body struct {
				ChallengeID string `json:"challenge_id"`
				Code        string `json:"code"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Code == u.factorOK {
				_ = json.NewEncoder(w).Encode(map[string]any{"access_token": accessTokenFor(u.email)})
				return
			}
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error_code": "invalid_credentials"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// newLoginTestRouter builds a real *Router with a Supabase backend pointing at
// the fake GoTrue — the only way to exercise handleLogin's MFA branch (which
// requires vres.Session.AccessToken != "" && SupabaseClient() != nil) without
// talking to a real GoTrue.
func newLoginTestRouter(t *testing.T, gt *fakeGoTrue, username, email string) *Router {
	t.Helper()
	dir, err := os.MkdirTemp("", "vpsm-login-test-")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	if err := os.MkdirAll(filepath.Join(dir, "users", username), 0o755); err != nil {
		t.Fatalf("mkdir users: %v", err)
	}

	srv := gt.server(t)

	uuidMap := fmt.Sprintf(`{"schema_version":1,"mappings":{%q:{"email":%q,"supabase_uuid":"11111111-1111-1111-1111-111111111111"}}}`, username, email)
	if err := os.WriteFile(filepath.Join(dir, "migration-uuid-map.json"), []byte(uuidMap), 0o600); err != nil {
		t.Fatalf("write uuid map: %v", err)
	}

	cfg := &config.Config{
		SchemaVersion:   2,
		Primary:         username,
		Listen:          ":0",
		DataDir:         dir,
		JWTSecret:       "login-char-test-secret-not-real-32c",
		SupabaseURL:     srv.URL,
		SupabaseAnonKey: "anon-test-key",
		Users: []config.User{
			{Username: username, PasswordHash: ""},
		},
	}
	r, err := NewRouter(cfg)
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	t.Cleanup(func() {
		defer func() { _ = recover() }()
		r.Shutdown(nil)
	})
	if r.auth.SupabaseClient() == nil {
		t.Fatal("SupabaseClient nil — test precondition failed (backend is not Supabase)")
	}
	return r
}

func doLogin(t *testing.T, r *Router, body map[string]any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings_NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w, out
}

// strings_NewReader avoids a redundant import — just a local alias for
// strings.NewReader, keeping the import block lean.
func strings_NewReader(s string) *strings.Reader { return strings.NewReader(s) }

const testPassword = "correct-horse-battery-staple"

// TestHandleLogin_PasswordOK_NoMFA proves: correct password + a user with no
// TOTP factor enrolled -> session issued straight away, no code demanded.
func TestHandleLogin_PasswordOK_NoMFA(t *testing.T) {
	gt := newFakeGoTrue()
	gt.addUser(&fakeGoTrueUser{email: "sam@test.local", password: testPassword})
	r := newLoginTestRouter(t, gt, "sam", "sam@test.local")

	w, out := doLogin(t, r, map[string]any{"username": "sam", "password": testPassword})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if tok, _ := out["token"].(string); tok == "" {
		t.Fatalf("expected a token in the body, got %v", out)
	}
	if out["user"] != "sam" {
		t.Fatalf("user = %v, expected sam", out["user"])
	}
}

// TestHandleLogin_PasswordWrong proves: wrong password -> 401, no token.
func TestHandleLogin_PasswordWrong(t *testing.T) {
	gt := newFakeGoTrue()
	gt.addUser(&fakeGoTrueUser{email: "sam@test.local", password: testPassword})
	r := newLoginTestRouter(t, gt, "sam", "sam@test.local")

	w, out := doLogin(t, r, map[string]any{"username": "sam", "password": "senha-errada"})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if _, hasToken := out["token"]; hasToken {
		t.Fatalf("there should be no token on a wrong password, got %v", out)
	}
}

// TestHandleLogin_MFARequired_NoTrustedDevice_NoCode proves: MFA enrolled, no
// trusted device, no code sent -> {"totp_required": true}, no
// token.
func TestHandleLogin_MFARequired_NoTrustedDevice_NoCode(t *testing.T) {
	gt := newFakeGoTrue()
	gt.addUser(&fakeGoTrueUser{
		email: "sam@test.local", password: testPassword,
		factorID: "factor-1", factorType: "totp", factorOK: "123456",
	})
	r := newLoginTestRouter(t, gt, "sam", "sam@test.local")

	w, out := doLogin(t, r, map[string]any{"username": "sam", "password": testPassword})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if out["totp_required"] != true {
		t.Fatalf("expected totp_required=true, got %v", out)
	}
	if _, hasToken := out["token"]; hasToken {
		t.Fatalf("should not issue a token without the second factor, got %v", out)
	}
}

// TestHandleLogin_MFASkippedViaTrustedDevice proves: MFA enrolled, device
// already trusted (valid trust cookie) -> session issued WITHOUT demanding a code.
func TestHandleLogin_MFASkippedViaTrustedDevice(t *testing.T) {
	gt := newFakeGoTrue()
	gt.addUser(&fakeGoTrueUser{
		email: "sam@test.local", password: testPassword,
		factorID: "factor-1", factorType: "totp", factorOK: "123456",
	})
	r := newLoginTestRouter(t, gt, "sam", "sam@test.local")

	secret, err := r.trustedDeviceStoreFor("sam").Mint("sam", "test-agent", "10.0.0.1")
	if err != nil {
		t.Fatalf("mint trusted device: %v", err)
	}

	b, _ := json.Marshal(map[string]any{"username": "sam", "password": testPassword})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "vpsm_device", Value: secret})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if tok, _ := out["token"].(string); tok == "" {
		t.Fatalf("expected a token via device trust, got %v", out)
	}
}

// TestHandleLogin_MFACorrectCode proves: MFA enrolled + correct Supabase code
// -> session issued.
func TestHandleLogin_MFACorrectCode(t *testing.T) {
	gt := newFakeGoTrue()
	gt.addUser(&fakeGoTrueUser{
		email: "sam@test.local", password: testPassword,
		factorID: "factor-1", factorType: "totp", factorOK: "123456",
	})
	r := newLoginTestRouter(t, gt, "sam", "sam@test.local")

	w, out := doLogin(t, r, map[string]any{"username": "sam", "password": testPassword, "totp": "123456"})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if tok, _ := out["token"].(string); tok == "" {
		t.Fatalf("expected a token with the correct code, got %v", out)
	}
}

// TestHandleLogin_MFAWrongCode_BackupCodeFallback proves: wrong Supabase code,
// but a valid backup code present -> session issued via
// backupCodeStoreFor(...).TryConsume, and the code is consumed (one-shot).
func TestHandleLogin_MFAWrongCode_BackupCodeFallback(t *testing.T) {
	gt := newFakeGoTrue()
	gt.addUser(&fakeGoTrueUser{
		email: "sam@test.local", password: testPassword,
		factorID: "factor-1", factorType: "totp", factorOK: "123456",
	})
	r := newLoginTestRouter(t, gt, "sam", "sam@test.local")

	codes, file, err := auth.GenerateBackupCodes("sam", 1)
	if err != nil {
		t.Fatalf("generate backup codes: %v", err)
	}
	store := auth.NewBackupCodesStore(auth.BackupCodesPath(r.cfg.DataDir, "sam"))
	if err := store.Save(file); err != nil {
		t.Fatalf("salvar backup codes: %v", err)
	}
	backupCode := codes[0]

	w, out := doLogin(t, r, map[string]any{"username": "sam", "password": testPassword, "totp": backupCode})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if tok, _ := out["token"].(string); tok == "" {
		t.Fatalf("expected a token via backup code, got %v", out)
	}

	unused, err := store.CountUnused()
	if err != nil {
		t.Fatalf("count unused: %v", err)
	}
	if unused != 0 {
		t.Fatalf("backup code should have been consumed (one-shot), unused = %d", unused)
	}
}

// TestHandleLogin_MFAWrongCode_NoValidBackup proves: wrong Supabase code and no
// valid backup code -> 401, no token.
func TestHandleLogin_MFAWrongCode_NoValidBackup(t *testing.T) {
	gt := newFakeGoTrue()
	gt.addUser(&fakeGoTrueUser{
		email: "sam@test.local", password: testPassword,
		factorID: "factor-1", factorType: "totp", factorOK: "123456",
	})
	r := newLoginTestRouter(t, gt, "sam", "sam@test.local")

	w, out := doLogin(t, r, map[string]any{"username": "sam", "password": testPassword, "totp": "000000"})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if _, hasToken := out["token"]; hasToken {
		t.Fatalf("there should be no token, got %v", out)
	}
}
