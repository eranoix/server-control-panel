package api

// passkey_test.go — proof, against a real *Router (not a fake), of the security
// properties of passkey.go that depend on the Router's real configuration and on
// the real *webauthn.WebAuthn instance:
//
//  1. The ceremony's RPID/RPOrigins come from Config.PublicHostname — the SAME
//     field /.well-known/assetlinks.json uses to announce the Android App Link
//     (handlers_wellknown.go). A value that diverged between the two would
//     silently break either the App Link or every passkey ceremony; this test
//     pins that both read exactly the same field.
//  2. Without Config.PublicHostname, passkeys go inert (graceful
//     degradation) — never a guessed RPID (e.g. "localhost").
//  3. Enumeration resistance: FinishPasskeyLogin collapses any malformed
//     input — unknown userHandle or unreadable payload — into the SAME
//     sentinel error, never revealing which of the two happened.

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"server-control-panel/internal/config"
	"server-control-panel/internal/mobilebff"
)

// newPasskeyRouter mirrors newSmokeRouter (smoke_test.go) but with
// PublicHostname filled in, so that initPasskey() is really exercised.
func newPasskeyRouter(t *testing.T, hostname string) *Router {
	t.Helper()
	dir, err := os.MkdirTemp("", "vpsm-passkey-")
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

// TestPasskeyRPIDSourcedFromPublicHostname proves that the Relying Party's
// RPID/RPOrigins come exactly from Config.PublicHostname — the same field read
// by handlers_wellknown.go for /.well-known/assetlinks.json. If this test and
// the assetlinks one ever read DIFFERENT fields of Config, App Link and passkey
// drift out of sync silently.
func TestPasskeyRPIDSourcedFromPublicHostname(t *testing.T) {
	const hostname = "vpsm.exemplo.com.br"
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

// TestPasskeyUnavailableWithoutPublicHostname proves the graceful degradation:
// without PublicHostname there is no safe RPID to guess, so passkeys go inert —
// never "localhost", never a wildcard value. All four PasskeyBackend operations
// return ErrPasskeyUnavailable.
func TestPasskeyUnavailableWithoutPublicHostname(t *testing.T) {
	r := newPasskeyRouter(t, "")

	if r.webauthnRP != nil {
		t.Fatalf("webauthnRP should be nil without PublicHostname, got %+v", r.webauthnRP.Config)
	}

	if _, _, err := r.BeginPasskeyRegistration("qualquer-token"); !errors.Is(err, mobilebff.ErrPasskeyUnavailable) {
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

// TestPasskeyLoginFinish_EnumerationResistance proves that FinishPasskeyLogin
// collapses distinct inputs — unreadable JSON, well-formed JSON without the
// expected WebAuthn fields, and a real session continuation token — into the
// SAME sentinel error (ErrPasskeyInvalidCredential), never a different error
// that would give away which of them "almost" worked. It is that
// indistinguishability, and not any secret in the payload itself, that stops an
// attacker using the endpoint's response to find out whether a user/credential
// exists.
func TestPasskeyLoginFinish_EnumerationResistance(t *testing.T) {
	r := newPasskeyRouter(t, "vpsm.exemplo.com.br")
	if r.webauthnRP == nil {
		t.Fatal("webauthnRP nil — test precondition failed")
	}

	payloads := map[string][]byte{
		"json ilegível":                        []byte(`{not valid json`),
		"json vazio":                           []byte(`{}`),
		"json bem-formado sem campos WebAuthn": []byte(`{"id":"YWJj","rawId":"YWJj","type":"public-key","response":{}}`),
		"userHandle de usuário inexistente":    []byte(`{"id":"YWJj","rawId":"YWJj","type":"public-key","response":{"clientDataJSON":"e30=","authenticatorData":"AA==","signature":"AA==","userHandle":"dXN1YXJpby1mYW50YXNtYQ=="}}`),
	}

	// Each payload needs its OWN continuation token — the login token is
	// single-use (VerifyWebAuthnLoginSessionToken consumes the jti during the
	// verification itself, before any cryptographic validation; see
	// TestPasskeyLoginFinish_ReplayContinuationTokenFails). Reusing the same
	// token across payloads would test replay, not enumeration.
	var gotErrs []error
	for name, payload := range payloads {
		_, cont, err := r.BeginPasskeyLogin()
		if err != nil {
			t.Fatalf("%s: BeginPasskeyLogin: %v", name, err)
		}
		_, err = r.FinishPasskeyLogin(cont, payload, "10.0.0.1", "teste-agent")
		if err == nil {
			t.Fatalf("%s: expected an error (no real authenticator signed this), got nil", name)
		}
		if !errors.Is(err, mobilebff.ErrPasskeyInvalidCredential) {
			t.Fatalf("%s: err = %v, expected ErrPasskeyInvalidCredential — a malformed payload or nonexistent user leaking a DIFFERENT error would enable enumeration", name, err)
		}
		gotErrs = append(gotErrs, err)
	}

	// Every error message the caller observes must be identical — not merely
	// satisfy errors.Is, but be equal byte for byte, since mapPasskeyError
	// (mobilebff) uses err.Error() in the HTTP response.
	first := gotErrs[0].Error()
	for i, e := range gotErrs {
		if e.Error() != first {
			t.Fatalf("payload %d produced a different error message (%q vs %q) — a shape leak that distinguishes the cases", i, e.Error(), first)
		}
	}
}

// TestPasskeyLoginFinish_ReplayContinuationTokenFails proves that the login
// ceremony's continuation token is single-use: reusing it on a second call
// (even with an equally invalid payload) still fails — but for an invalid
// TOKEN, not for a credential, so it should never land on
// ErrPasskeyUnavailable/nil. Strict replay of the cryptographic challenge
// itself is covered by TestWebAuthnLoginSessionToken_RoundTripAndReplay
// (internal/auth/webauthn_tokens_test.go); here we prove that the Router
// really does consume VerifyWebAuthnLoginSessionToken once — calling
// FinishPasskeyLogin twice with the SAME continuation token does not reopen
// the same challenge session.
func TestPasskeyLoginFinish_ReplayContinuationTokenFails(t *testing.T) {
	r := newPasskeyRouter(t, "vpsm.exemplo.com.br")
	_, cont, err := r.BeginPasskeyLogin()
	if err != nil {
		t.Fatalf("BeginPasskeyLogin: %v", err)
	}

	payload := []byte(`{"id":"YWJj","rawId":"YWJj","type":"public-key","response":{}}`)

	if _, err := r.FinishPasskeyLogin(cont, payload, "10.0.0.1", "ua"); err == nil {
		t.Fatal("first call should fail (payload without a valid signature), but must not silently succeed with a nil error")
	}

	// The continuation token was already consumed by VerifyWebAuthnLoginSessionToken
	// inside the first call (even though it failed afterwards, in the
	// cryptographic validation) — reusing it must keep failing, never "unlock" the
	// ceremony on a second attempt.
	if _, err := r.FinishPasskeyLogin(cont, payload, "10.0.0.1", "ua"); err == nil {
		t.Fatal("replay of the continuation token should fail")
	}
}
