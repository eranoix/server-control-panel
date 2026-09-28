package api

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"server-control-panel/internal/config"
	"server-control-panel/internal/mobilebff"
)

func newPasskeyRouter(t *testing.T, hostname string) *Router {
	t.Helper()
	dir, err := os.MkdirTemp("", "panel-passkey-")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	cfg := &config.Config{
		SchemaVersion:  2,
		Primary:        "sam",
		Listen:         ":0",
		DataDir:        dir,
		JWTSecret:      "passkey-test-secret-not-real-32ch",
		PublicHostname: hostname,
		Users: []config.User{
			{Username: "sam", PasswordHash: ""},
		},
	}
	if err := os.MkdirAll(filepath.Join(dir, "users", "sam"), 0o755); err != nil {
		t.Fatalf("mkdir users: %v", err)
	}

	r, err := NewRouter(cfg)
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	t.Cleanup(func() {
		defer func() { _ = recover() }()
		r.Shutdown(nil)
	})
	return r
}

func TestPasskeyRPIDSourcedFromPublicHostname(t *testing.T) {
	const hostname = "panel.example.com"
	r := newPasskeyRouter(t, hostname)

	if r.webauthnRP == nil {
		t.Fatal("webauthnRP is nil with PublicHostname set — initPasskey did not run")
	}
	if r.webauthnRP.Config.RPID != hostname {
		t.Fatalf("RPID = %q, expected %q (Config.PublicHostname)", r.webauthnRP.Config.RPID, hostname)
	}
	wantOrigin := "https://" + hostname
	if len(r.webauthnRP.Config.RPOrigins) != 1 || r.webauthnRP.Config.RPOrigins[0] != wantOrigin {
		t.Fatalf("RPOrigins = %v, expected [%q]", r.webauthnRP.Config.RPOrigins, wantOrigin)
	}
	if r.cfg.PublicHostname != hostname {
		t.Fatalf("Config.PublicHostname = %q — this is the same field handlers_wellknown.go uses for assetlinks.json; it must match the RPID above", r.cfg.PublicHostname)
	}
}

func TestPasskeyUnavailableWithoutPublicHostname(t *testing.T) {
	r := newPasskeyRouter(t, "")

	if r.webauthnRP != nil {
		t.Fatalf("webauthnRP should be nil without PublicHostname, got %+v", r.webauthnRP.Config)
	}

	if _, _, err := r.BeginPasskeyRegistration("any-token"); !errors.Is(err, mobilebff.ErrPasskeyUnavailable) {
		t.Fatalf("BeginPasskeyRegistration: err = %v, expected ErrPasskeyUnavailable", err)
	}
	if err := r.FinishPasskeyRegistration("cont", "label", []byte(`{}`)); !errors.Is(err, mobilebff.ErrPasskeyUnavailable) {
		t.Fatalf("FinishPasskeyRegistration: err = %v, expected ErrPasskeyUnavailable", err)
	}
	if _, _, err := r.BeginPasskeyLogin(); !errors.Is(err, mobilebff.ErrPasskeyUnavailable) {
		t.Fatalf("BeginPasskeyLogin: err = %v, expected ErrPasskeyUnavailable", err)
	}
	if _, err := r.FinishPasskeyLogin("cont", []byte(`{}`), "1.2.3.4", "ua"); !errors.Is(err, mobilebff.ErrPasskeyUnavailable) {
		t.Fatalf("FinishPasskeyLogin: err = %v, expected ErrPasskeyUnavailable", err)
	}
}

func TestPasskeyLoginFinish_EnumerationResistance(t *testing.T) {
	r := newPasskeyRouter(t, "panel.example.com")
	if r.webauthnRP == nil {
		t.Fatal("webauthnRP nil — test precondition failed")
	}

	payloads := map[string][]byte{
		"unreadable json": []byte(`{not valid json`),
		"empty json":      []byte(`{}`),
		"well-formed json without WebAuthn fields": []byte(`{"id":"YWJj","rawId":"YWJj","type":"public-key","response":{}}`),
		"userHandle of a nonexistent user":         []byte(`{"id":"YWJj","rawId":"YWJj","type":"public-key","response":{"clientDataJSON":"e30=","authenticatorData":"AA==","signature":"AA==","userHandle":"dXN1YXJpby1mYW50YXNtYQ=="}}`),
	}

	var gotErrs []error
	for name, payload := range payloads {
		_, cont, err := r.BeginPasskeyLogin()
		if err != nil {
			t.Fatalf("%s: BeginPasskeyLogin: %v", name, err)
		}
		_, err = r.FinishPasskeyLogin(cont, payload, "10.0.0.1", "test-agent")
		if err == nil {
			t.Fatalf("%s: expected an error (no real authenticator signed this), got nil", name)
		}
		if !errors.Is(err, mobilebff.ErrPasskeyInvalidCredential) {
			t.Fatalf("%s: err = %v, expected ErrPasskeyInvalidCredential — a malformed payload or nonexistent user leaking a DIFFERENT error would enable enumeration", name, err)
		}
		gotErrs = append(gotErrs, err)
	}

	first := gotErrs[0].Error()
	for i, e := range gotErrs {
		if e.Error() != first {
			t.Fatalf("payload %d produced a different error message (%q vs %q) — a shape leak that distinguishes the cases", i, e.Error(), first)
		}
	}
}

func TestPasskeyLoginFinish_ReplayContinuationTokenFails(t *testing.T) {
	r := newPasskeyRouter(t, "panel.example.com")
	_, cont, err := r.BeginPasskeyLogin()
	if err != nil {
		t.Fatalf("BeginPasskeyLogin: %v", err)
	}

	payload := []byte(`{"id":"YWJj","rawId":"YWJj","type":"public-key","response":{}}`)

	if _, err := r.FinishPasskeyLogin(cont, payload, "10.0.0.1", "ua"); err == nil {
		t.Fatal("first call should fail (payload without a valid signature), but must not silently succeed with a nil error")
	}

	if _, err := r.FinishPasskeyLogin(cont, payload, "10.0.0.1", "ua"); err == nil {
		t.Fatal("replay of the continuation token should fail")
	}
}
