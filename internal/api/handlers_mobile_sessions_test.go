package api

// handlers_mobile_sessions_test.go — proof, against a real *Router, of the
// security properties of the paired-device panel:
//
//  1. Isolation between users by construction: approving/denying/revoking an
//     ID that belongs to another user always returns 404, never 403, and never
//     mutates the other user's record.
//  2. Approving is the ONLY path that makes a passkey login-capable, and
//     revoking/denying makes a credential IMMEDIATELY unusable for login —
//     proved with a REAL WebAuthn ceremony (not a stub), using the official
//     W3C WebAuthn Level 3 §16 test vectors that go-webauthn itself embeds in
//     its own tests
//     (protocol/specification_vectors_e2e_test.go, case "NoneES256"/§16.2).
//  3. The edge cases asked for explicitly: approving twice (idempotent),
//     approving an already revoked credential (404 — Remove() deletes the
//     record, leaving no remnant for Approve() to revive), and revoking the
//     credential in use does not drop the current desktop session
//     (sessions.Store and WebAuthnCredentialsStore are completely separate
//     stores).

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/protocol/webauthncose"
	"github.com/go-webauthn/webauthn/webauthn"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/mobilebff"
	"server-control-panel/internal/sessions"
)

// ---------- Official W3C WebAuthn Level 3 §16.2 vectors (None Attestation, ES256) ----------
//
// The same credential in both vectors (registration and authentication) —
// replicated byte for byte from protocol/specification_vectors_e2e_test.go of
// module go-webauthn/webauthn@v0.18.0 (already a pinned dependency of this
// project), which in turn copies them from the official text of the
// specification
// (https://www.w3.org/TR/webauthn-3/#sctn-test-vectors-none-es256). RPID
// "example.org" / origin "https://example.org" are the vector's fixed values —
// not something this test made up.
const (
	specRPID   = "example.org"
	specOrigin = "https://example.org"

	specRegAttestationObjectHex = "a363666d74646e6f6e656761747453746d74a068617574684461746158a4bfabc37432958b063360d3ad6461c9c4735ae7f8edd46592a5e0f01452b2e4b559000000008446ccb9ab1db374750b2367ff6f3a1f0020f91f391db4c9b2fde0ea70189cba3fb63f579ba6122b33ad94ff3ec330084be4a5010203262001215820afefa16f97ca9b2d23eb86ccb64098d20db90856062eb249c33a9b672f26df61225820930a56b87a2fca66334b03458abf879717c12cc68ed73290af2e2664796b9220"
	specRegClientDataJSONHex    = "7b2274797065223a22776562617574686e2e637265617465222c226368616c6c656e6765223a22414d4d507434557878475453746e63647134313759447742466938767049612d7077386f4f755657345441222c226f726967696e223a2268747470733a2f2f6578616d706c652e6f7267222c2263726f73734f726967696e223a66616c73652c22657874726144617461223a22636c69656e74446174614a534f4e206d617920626520657874656e6465642077697468206164646974696f6e616c206669656c647320696e20746865206675747572652c207375636820617320746869733a20426b5165446a646354427258426941774a544c453551227d"
	specRegCredentialIDHex      = "f91f391db4c9b2fde0ea70189cba3fb63f579ba6122b33ad94ff3ec330084be4"
	specRegChallengeHex         = "00c30fb78531c464d2b6771dab8d7b603c01162f2fa486bea70f283ae556e130"

	specAuthAuthenticatorDataHex = "bfabc37432958b063360d3ad6461c9c4735ae7f8edd46592a5e0f01452b2e4b51900000000"
	specAuthClientDataJSONHex    = "7b2274797065223a22776562617574686e2e676574222c226368616c6c656e6765223a224f63446e55685158756c5455506f334a5558543049393770767a7a59425039745a63685879617630314167222c226f726967696e223a2268747470733a2f2f6578616d706c652e6f7267222c2263726f73734f726967696e223a66616c73657d"
	specAuthSignatureHex         = "3046022100f50a4e2e4409249c4a853ba361282f09841df4dd4547a13a87780218deffcd380221008480ac0f0b93538174f575bf11a1dd5d78c6e486013f937295ea13653e331e87"
	specAuthChallengeHex         = "39c0e7521417ba54d43e8dc95174f423dee9bf3cd804ff6d65c857c9abf4d408"
)

func specHexToB64URL(t *testing.T, h string) string {
	t.Helper()
	raw, err := hex.DecodeString(h)
	if err != nil {
		t.Fatalf("hex.DecodeString(%q): %v", h, err)
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

// specBuildRegistrationJSON builds the JSON body the browser would send to the
// backend at the end of navigator.credentials.create() — the same shape
// FinishRegistrationForUser (internal/auth/webauthn.go) expects to receive from
// the Android app via mobilebff.
func specBuildRegistrationJSON(t *testing.T) []byte {
	t.Helper()
	id := specHexToB64URL(t, specRegCredentialIDHex)
	body := map[string]any{
		"id": id, "rawId": id, "type": "public-key",
		"response": map[string]any{
			"attestationObject": specHexToB64URL(t, specRegAttestationObjectHex),
			"clientDataJSON":    specHexToB64URL(t, specRegClientDataJSONHex),
		},
	}
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal registration body: %v", err)
	}
	return data
}

// specBuildAssertionJSON builds the JSON body of navigator.credentials.get(),
// with userHandle set to the test username — userHandle is not part of any
// signed data (neither clientDataJSON nor authenticatorData), so assigning it
// freely here does not invalidate the official vector's signature; it is exactly
// the datum auth.FinishDiscoverableLogin uses to resolve WHICH user store to
// open (see webauthnUser.WebAuthnID in webauthn.go).
func specBuildAssertionJSON(t *testing.T, username string) []byte {
	t.Helper()
	id := specHexToB64URL(t, specRegCredentialIDHex)
	body := map[string]any{
		"id": id, "rawId": id, "type": "public-key",
		"response": map[string]any{
			"authenticatorData": specHexToB64URL(t, specAuthAuthenticatorDataHex),
			"clientDataJSON":    specHexToB64URL(t, specAuthClientDataJSONHex),
			"signature":         specHexToB64URL(t, specAuthSignatureHex),
			"userHandle":        base64.RawURLEncoding.EncodeToString([]byte(username)),
		},
	}
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal assertion body: %v", err)
	}
	return data
}

// specRegisterRealCredential runs the REAL registration ceremony (real
// cryptography, via auth.FinishRegistrationForUser) against the §16.2 vector and
// returns the resulting *webauthn.Credential, ready for
// WebAuthnCredentialsStore.Add — the same code path FinishPasskeyRegistration
// (internal/api/passkey.go) uses in production.
func specRegisterRealCredential(t *testing.T, w *webauthn.WebAuthn, username string) *webauthn.Credential {
	t.Helper()
	session := webauthn.SessionData{
		Challenge:        specHexToB64URL(t, specRegChallengeHex),
		RelyingPartyID:   specRPID,
		Origin:           specOrigin,
		UserID:           []byte(username),
		Expires:          time.Now().Add(time.Hour),
		UserVerification: protocol.VerificationPreferred,
		CredParams: []protocol.CredentialParameter{
			{Type: protocol.PublicKeyCredentialType, Algorithm: webauthncose.AlgES256},
		},
	}
	cred, err := auth.FinishRegistrationForUser(w, username, nil, session, specBuildRegistrationJSON(t))
	if err != nil {
		t.Fatalf("FinishRegistrationForUser (vector §16.2): %v — official vector should verify clean", err)
	}
	return cred
}

// specRealWebAuthnRP builds a *webauthn.WebAuthn pointing at the official
// vector's fixed RPID/origin (example.org / https://example.org) — not the test
// Router's PublicHostname, because the vector's RPID is hard-coded by the
// specification.
func specRealWebAuthnRP(t *testing.T) *webauthn.WebAuthn {
	t.Helper()
	w, err := webauthn.New(auth.NewWebAuthnConfig(specRPID, specOrigin, "VPS Manager teste"))
	if err != nil {
		t.Fatalf("webauthn.New: %v", err)
	}
	return w
}

// doMobileSessionsRequest injects the authenticated user into the context (the
// way auth.Middleware would after validating the JWT) and dispatches straight
// into the handler, without going through the mux — the same pattern as handlers_terminal_assign_test.go.
func doMobileSessionsRequest(t *testing.T, handler http.HandlerFunc, user, method string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(b)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, "/api/auth/mobile-sessions", reader)
	req = req.WithContext(auth.WithUser(req.Context(), user))
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec
}

// TestMobileSessions_CrossUserApproveRevoke404NeverResurrects proves the
// cross-user isolation rule: approving or revoking ANOTHER user's ID always
// returns 404 (never 403 — that would confirm the ID exists to somebody who
// does not own it), and never mutates the real owner's record.
func TestMobileSessions_CrossUserApproveRevoke404NeverResurrects(t *testing.T) {
	r := newPasskeyRouter(t, "vpsm.exemplo.com.br")

	victimStore := r.credentialStore("sam")
	rec, err := victimStore.Add(webauthn.Credential{ID: []byte("victim-cred")}, "celular da vítima")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	// "invasor" does not exist in Config.Users — but the isolation is by FILE
	// path (WebAuthnCredentialsPath), not by a valid-user check, so even an
	// arbitrary username cannot reach the other user's record.
	respApprove := doMobileSessionsRequest(t, r.handleApproveMobileCredential, "invasor", http.MethodPost, map[string]string{"id": rec.ID})
	if respApprove.Code != 404 {
		t.Fatalf("approve of someone else's credential: status = %d, expected 404 (never 403 — would leak existence)", respApprove.Code)
	}
	respRevoke := doMobileSessionsRequest(t, r.handleRevokeMobileSession, "invasor", http.MethodPost, map[string]string{"id": rec.ID})
	if respRevoke.Code != 404 {
		t.Fatalf("revoke of someone else's credential: status = %d, expected 404", respRevoke.Code)
	}

	// The victim's record must not have been touched by either of the two
	// attempts.
	got, found, err := victimStore.CredentialByID(rec.ID)
	if err != nil || !found {
		t.Fatalf("victim's credential disappeared after the other user's attempt: found=%v err=%v", found, err)
	}
	if got.Status != auth.CredentialStatusPending {
		t.Fatalf("victim's credential status = %q, expected to remain %q (attacker should not be able to approve)", got.Status, auth.CredentialStatusPending)
	}
}

// TestMobileSessions_ListSelfScoped proves that handleListMobileSessions only
// sees the caller's OWN credentials — another user in the same DataDir does not
// show up in the list.
func TestMobileSessions_ListSelfScoped(t *testing.T) {
	r := newPasskeyRouter(t, "vpsm.exemplo.com.br")

	if _, err := r.credentialStore("sam").Add(webauthn.Credential{ID: []byte("cred-sam")}, "celular do sam"); err != nil {
		t.Fatalf("Add sam: %v", err)
	}
	if _, err := r.credentialStore("outro-usuario").Add(webauthn.Credential{ID: []byte("cred-outro")}, "celular do outro"); err != nil {
		t.Fatalf("Add outro: %v", err)
	}

	rec := doMobileSessionsRequest(t, r.handleListMobileSessions, "sam", http.MethodGet, nil)
	if rec.Code != 200 {
		t.Fatalf("list: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var out []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("len(out) = %d, expected 1 (only sam's credential) — payload: %s", len(out), rec.Body.String())
	}
	if out[0]["label"] != "celular do sam" {
		t.Fatalf("label = %v, expected sam's credential, not outro-usuario's", out[0]["label"])
	}
}

// TestMobileSessions_ApproveTwiceIsIdempotent proves the edge case asked for
// explicitly: approving an already approved credential is not an error.
func TestMobileSessions_ApproveTwiceIsIdempotent(t *testing.T) {
	r := newPasskeyRouter(t, "vpsm.exemplo.com.br")
	store := r.credentialStore("sam")
	rec, err := store.Add(webauthn.Credential{ID: []byte("cred-dupla-aprovacao")}, "celular")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	first := doMobileSessionsRequest(t, r.handleApproveMobileCredential, "sam", http.MethodPost, map[string]string{"id": rec.ID})
	if first.Code != 200 {
		t.Fatalf("first approval: status = %d", first.Code)
	}
	second := doMobileSessionsRequest(t, r.handleApproveMobileCredential, "sam", http.MethodPost, map[string]string{"id": rec.ID})
	if second.Code != 200 {
		t.Fatalf("second approval (idempotent): status = %d, expected 200", second.Code)
	}
	got, found, err := store.CredentialByID(rec.ID)
	if err != nil || !found || got.Status != auth.CredentialStatusApproved {
		t.Fatalf("unexpected final state: found=%v status=%q err=%v", found, got.Status, err)
	}
}

// TestMobileSessions_ApproveAfterRevokeFails proves the edge case asked for
// explicitly: approving an already revoked credential fails (404) — Remove()
// deletes the record entirely, leaving no remnant for Approve() to revive.
func TestMobileSessions_ApproveAfterRevokeFails(t *testing.T) {
	r := newPasskeyRouter(t, "vpsm.exemplo.com.br")
	store := r.credentialStore("sam")
	rec, err := store.Add(webauthn.Credential{ID: []byte("cred-revogada")}, "celular")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if resp := doMobileSessionsRequest(t, r.handleRevokeMobileSession, "sam", http.MethodPost, map[string]string{"id": rec.ID}); resp.Code != 200 {
		t.Fatalf("revoke: status = %d", resp.Code)
	}
	if resp := doMobileSessionsRequest(t, r.handleApproveMobileCredential, "sam", http.MethodPost, map[string]string{"id": rec.ID}); resp.Code != 404 {
		t.Fatalf("approve after revoke: status = %d, expected 404 (must not resurrect a removed credential)", resp.Code)
	}
}

// TestMobileSessions_DenyReusesRevokeSemantics proves that denying a PENDING
// pairing uses the same removal operation as revoking an approved one —
// Remove() does not look at status.
func TestMobileSessions_DenyReusesRevokeSemantics(t *testing.T) {
	r := newPasskeyRouter(t, "vpsm.exemplo.com.br")
	store := r.credentialStore("sam")
	rec, err := store.Add(webauthn.Credential{ID: []byte("cred-pendente-negada")}, "celular")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if resp := doMobileSessionsRequest(t, r.handleDenyMobileCredential, "sam", http.MethodPost, map[string]string{"id": rec.ID}); resp.Code != 200 {
		t.Fatalf("deny: status = %d", resp.Code)
	}
	if _, found, _ := store.CredentialByID(rec.ID); found {
		t.Fatal("denied credential still exists in the store — deny should remove it, not just hide it")
	}
}

// TestMobileSessions_RevokeCurrentCredentialDoesNotTouchDesktopSession proves
// the edge case asked for explicitly: revoking the credential in use does NOT
// drop the current desktop session — sessions.Store (JWT/jti) and
// WebAuthnCredentialsStore (passkey) are separate stores.
func TestMobileSessions_RevokeCurrentCredentialDoesNotTouchDesktopSession(t *testing.T) {
	r := newPasskeyRouter(t, "vpsm.exemplo.com.br")
	sessStore := r.auth.Sessions()
	if sessStore == nil {
		t.Fatal("sessions store unavailable — test precondition failed")
	}
	sessStore.Add(sessions.Session{
		JTI:       "desktop-jti-corrente",
		User:      "sam",
		IssuedAt:  time.Now().Unix(),
		LastSeen:  time.Now().Unix(),
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
	})

	credStore := r.credentialStore("sam")
	rec, err := credStore.Add(webauthn.Credential{ID: []byte("cred-em-uso")}, "celular em uso")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if resp := doMobileSessionsRequest(t, r.handleRevokeMobileSession, "sam", http.MethodPost, map[string]string{"id": rec.ID}); resp.Code != 200 {
		t.Fatalf("revoke: status = %d", resp.Code)
	}
	if !sessStore.Has("desktop-jti-corrente") {
		t.Fatal("revoking the passkey brought down the current desktop session — they should be independent stores")
	}
}

// TestPasskeyLogin_ApproveIsSoleGateAndRevokeKillsLoginImmediately is the
// central proof: using a REAL WebAuthn ceremony (official W3C §16.2 vectors,
// see the spec* constants above — no cryptography in this test is
// hand-rolled), it confirms that:
//
//  1. A freshly registered (pending) credential CANNOT log in even with a
//     cryptographically valid signature — not through the pairing ticket, not
//     from the device itself: FinishPasskeyLogin is the only way to try, and
//     it checks the status in the SAME store.
//  2. Approve() is what unlocks it — the SAME signature, with nothing else
//     changed, starts issuing a token.
//  3. Revoking makes the credential IMMEDIATELY unusable: the same signature,
//     in the same desktop session, now fails (the credential no longer exists
//     in the store, so it lands on ErrPasskeyInvalidCredential — never on
//     "pending", which would reveal the difference between "never existed" and
//     "existed and was revoked").
func TestPasskeyLogin_ApproveIsSoleGateAndRevokeKillsLoginImmediately(t *testing.T) {
	const username = "sam"
	r := newPasskeyRouter(t, "vpsm.exemplo.com.br")

	// The ceremony itself runs against the official vector's fixed RPID
	// (example.org), not against the Router's test hostname — it swaps the RP
	// for this proof only, exactly as initPasskey would swap it if
	// PublicHostname were "example.org".
	specRP := specRealWebAuthnRP(t)
	r.webauthnRP = specRP

	cred := specRegisterRealCredential(t, specRP, username)
	store := r.credentialStore(username)
	credRec, err := store.Add(*cred, "dispositivo do vetor §16.2")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if credRec.Status != auth.CredentialStatusPending {
		t.Fatalf("newly registered credential was born with status %q, expected %q", credRec.Status, auth.CredentialStatusPending)
	}

	beginLoginSession := func() string {
		sessionJSON, err := json.Marshal(webauthn.SessionData{
			Challenge: specHexToB64URL(t, specAuthChallengeHex),
			Expires:   time.Now().Add(time.Hour),
		})
		if err != nil {
			t.Fatalf("marshal session: %v", err)
		}
		cont, err := r.auth.IssueWebAuthnLoginSessionToken(sessionJSON)
		if err != nil {
			t.Fatalf("IssueWebAuthnLoginSessionToken: %v", err)
		}
		return cont
	}

	// 1. PENDING: a valid signature, but the credential has not yet been
	// approved by any desktop session — only the (public) login continuation
	// token was used, never the pairing ticket nor a session from the device
	// itself. It must fail with "pending", never issue a token.
	if _, err := r.FinishPasskeyLogin(beginLoginSession(), specBuildAssertionJSON(t, username), "10.0.0.1", "vetor-teste"); err == nil {
		t.Fatal("login with a pending credential should fail, but returned success (nil error)")
	} else if err != mobilebff.ErrPasskeyPendingApproval {
		t.Fatalf("login with a pending credential: err = %v, expected ErrPasskeyPendingApproval", err)
	}

	// 2. APROVA — via handler HTTP real, exatamente como o painel desktop
	// autenticado chamaria.
	if resp := doMobileSessionsRequest(t, r.handleApproveMobileCredential, username, http.MethodPost, map[string]string{"id": credRec.ID}); resp.Code != 200 {
		t.Fatalf("approve: status = %d, body = %s", resp.Code, resp.Body.String())
	}

	// 3. APPROVED: the SAME signature now issues the access+refresh pair —
	// passkey is the product's primary path and needs the SAME silent renewal
	// that password login already has (never the access token alone).
	result, err := r.FinishPasskeyLogin(beginLoginSession(), specBuildAssertionJSON(t, username), "10.0.0.1", "vetor-teste")
	if err != nil {
		t.Fatalf("login with an approved credential should work, err = %v", err)
	}
	if result.AccessToken == "" {
		t.Fatal("approved login returned an empty access_token")
	}
	if result.RefreshToken == "" {
		t.Fatal("approved login returned an empty refresh_token — passkey would silently stop renewing the session")
	}

	// 4. REVOGA.
	if resp := doMobileSessionsRequest(t, r.handleRevokeMobileSession, username, http.MethodPost, map[string]string{"id": credRec.ID}); resp.Code != 200 {
		t.Fatalf("revoke: status = %d, body = %s", resp.Code, resp.Body.String())
	}

	// 5. REVOKED: the SAME signature now fails — the credential no longer
	// exists, and the error is the invalid-credential sentinel (not "pending",
	// which would tell "never existed" apart from "was revoked" for anyone
	// trying to log in with a stolen/cloned credential).
	if _, err := r.FinishPasskeyLogin(beginLoginSession(), specBuildAssertionJSON(t, username), "10.0.0.1", "vetor-teste"); err == nil {
		t.Fatal("login with a revoked credential should fail, but returned success")
	} else if err != mobilebff.ErrPasskeyInvalidCredential {
		t.Fatalf("login with a revoked credential: err = %v, expected ErrPasskeyInvalidCredential", err)
	}
}

// TestPasskeyLogin_RefreshTokenRotatesAndInvalidatesOldToken proves that the
// refresh token issued by an approved passkey login goes through the SAME
// auth.MobileRefreshStore as password login: it rotates successfully once, and
// reusing the OLD token afterwards (replay) always fails with
// ErrMobileRefreshInvalid — it never "almost works" on a second attempt.
// Without this, passkey — the product's primary path — would have worse session
// continuity than the password fallback.
func TestPasskeyLogin_RefreshTokenRotatesAndInvalidatesOldToken(t *testing.T) {
	const username = "sam"
	r := newPasskeyRouter(t, "vpsm.exemplo.com.br")

	specRP := specRealWebAuthnRP(t)
	r.webauthnRP = specRP

	cred := specRegisterRealCredential(t, specRP, username)
	store := r.credentialStore(username)
	credRec, err := store.Add(*cred, "dispositivo do vetor §16.2")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if resp := doMobileSessionsRequest(t, r.handleApproveMobileCredential, username, http.MethodPost, map[string]string{"id": credRec.ID}); resp.Code != 200 {
		t.Fatalf("approve: status = %d, body = %s", resp.Code, resp.Body.String())
	}

	sessionJSON, err := json.Marshal(webauthn.SessionData{
		Challenge: specHexToB64URL(t, specAuthChallengeHex),
		Expires:   time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("marshal session: %v", err)
	}
	cont, err := r.auth.IssueWebAuthnLoginSessionToken(sessionJSON)
	if err != nil {
		t.Fatalf("IssueWebAuthnLoginSessionToken: %v", err)
	}

	login, err := r.FinishPasskeyLogin(cont, specBuildAssertionJSON(t, username), "10.0.0.1", "vetor-teste")
	if err != nil {
		t.Fatalf("FinishPasskeyLogin: %v", err)
	}
	if login.RefreshToken == "" {
		t.Fatal("passkey login should return a refresh_token")
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

	// Replay of the ORIGINAL refresh_token (issued by the passkey login,
	// already rotated) — it must always fail, never "unlock" on a second
	// attempt.
	if _, err := r.MobileRefresh(login.RefreshToken); err != mobilebff.ErrMobileRefreshInvalid {
		t.Fatalf("replay of the old token: err = %v, expected ErrMobileRefreshInvalid", err)
	}
	if _, err := r.MobileRefresh(login.RefreshToken); err != mobilebff.ErrMobileRefreshInvalid {
		t.Fatalf("second replay: err = %v, expected ErrMobileRefreshInvalid", err)
	}
}
