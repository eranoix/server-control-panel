package api

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

func specRealWebAuthnRP(t *testing.T) *webauthn.WebAuthn {
	t.Helper()
	w, err := webauthn.New(auth.NewWebAuthnConfig(specRPID, specOrigin, "Server Control Panel test"))
	if err != nil {
		t.Fatalf("webauthn.New: %v", err)
	}
	return w
}

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

func TestMobileSessions_CrossUserApproveRevoke404NeverResurrects(t *testing.T) {
	r := newPasskeyRouter(t, "panel.example.com")

	victimStore := r.credentialStore("sam")
	rec, err := victimStore.Add(webauthn.Credential{ID: []byte("victim-cred")}, "victim's phone")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	respApprove := doMobileSessionsRequest(t, r.handleApproveMobileCredential, "intruder", http.MethodPost, map[string]string{"id": rec.ID})
	if respApprove.Code != 404 {
		t.Fatalf("approve of someone else's credential: status = %d, expected 404 (never 403 — would leak existence)", respApprove.Code)
	}
	respRevoke := doMobileSessionsRequest(t, r.handleRevokeMobileSession, "intruder", http.MethodPost, map[string]string{"id": rec.ID})
	if respRevoke.Code != 404 {
		t.Fatalf("revoke of someone else's credential: status = %d, expected 404", respRevoke.Code)
	}

	got, found, err := victimStore.CredentialByID(rec.ID)
	if err != nil || !found {
		t.Fatalf("victim's credential disappeared after the other user's attempt: found=%v err=%v", found, err)
	}
	if got.Status != auth.CredentialStatusPending {
		t.Fatalf("victim's credential status = %q, expected to remain %q (attacker should not be able to approve)", got.Status, auth.CredentialStatusPending)
	}
}

func TestMobileSessions_ListSelfScoped(t *testing.T) {
	r := newPasskeyRouter(t, "panel.example.com")

	if _, err := r.credentialStore("sam").Add(webauthn.Credential{ID: []byte("cred-sam")}, "sam's phone"); err != nil {
		t.Fatalf("Add sam: %v", err)
	}
	if _, err := r.credentialStore("other-user").Add(webauthn.Credential{ID: []byte("cred-other")}, "other user's phone"); err != nil {
		t.Fatalf("Add other: %v", err)
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
	if out[0]["label"] != "sam's phone" {
		t.Fatalf("label = %v, expected sam's credential, not other-user's", out[0]["label"])
	}
}

func TestMobileSessions_ApproveTwiceIsIdempotent(t *testing.T) {
	r := newPasskeyRouter(t, "panel.example.com")
	store := r.credentialStore("sam")
	rec, err := store.Add(webauthn.Credential{ID: []byte("cred-double-approval")}, "phone")
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

func TestMobileSessions_ApproveAfterRevokeFails(t *testing.T) {
	r := newPasskeyRouter(t, "panel.example.com")
	store := r.credentialStore("sam")
	rec, err := store.Add(webauthn.Credential{ID: []byte("cred-revoked")}, "phone")
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

func TestMobileSessions_DenyReusesRevokeSemantics(t *testing.T) {
	r := newPasskeyRouter(t, "panel.example.com")
	store := r.credentialStore("sam")
	rec, err := store.Add(webauthn.Credential{ID: []byte("cred-pending-denied")}, "phone")
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

func TestMobileSessions_RevokeCurrentCredentialDoesNotTouchDesktopSession(t *testing.T) {
	r := newPasskeyRouter(t, "panel.example.com")
	sessStore := r.auth.Sessions()
	if sessStore == nil {
		t.Fatal("sessions store unavailable — test precondition failed")
	}
	sessStore.Add(sessions.Session{
		JTI:       "desktop-jti-current",
		User:      "sam",
		IssuedAt:  time.Now().Unix(),
		LastSeen:  time.Now().Unix(),
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
	})

	credStore := r.credentialStore("sam")
	rec, err := credStore.Add(webauthn.Credential{ID: []byte("cred-in-use")}, "phone in use")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if resp := doMobileSessionsRequest(t, r.handleRevokeMobileSession, "sam", http.MethodPost, map[string]string{"id": rec.ID}); resp.Code != 200 {
		t.Fatalf("revoke: status = %d", resp.Code)
	}
	if !sessStore.Has("desktop-jti-current") {
		t.Fatal("revoking the passkey brought down the current desktop session — they should be independent stores")
	}
}

func TestPasskeyLogin_ApproveIsSoleGateAndRevokeKillsLoginImmediately(t *testing.T) {
	const username = "sam"
	r := newPasskeyRouter(t, "panel.example.com")

	specRP := specRealWebAuthnRP(t)
	r.webauthnRP = specRP

	cred := specRegisterRealCredential(t, specRP, username)
	store := r.credentialStore(username)
	credRec, err := store.Add(*cred, "vector device §16.2")
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

	if _, err := r.FinishPasskeyLogin(beginLoginSession(), specBuildAssertionJSON(t, username), "10.0.0.1", "vector-test"); err == nil {
		t.Fatal("login with a pending credential should fail, but returned success (nil error)")
	} else if err != mobilebff.ErrPasskeyPendingApproval {
		t.Fatalf("login with a pending credential: err = %v, expected ErrPasskeyPendingApproval", err)
	}

	if resp := doMobileSessionsRequest(t, r.handleApproveMobileCredential, username, http.MethodPost, map[string]string{"id": credRec.ID}); resp.Code != 200 {
		t.Fatalf("approve: status = %d, body = %s", resp.Code, resp.Body.String())
	}

	result, err := r.FinishPasskeyLogin(beginLoginSession(), specBuildAssertionJSON(t, username), "10.0.0.1", "vector-test")
	if err != nil {
		t.Fatalf("login with an approved credential should work, err = %v", err)
	}
	if result.AccessToken == "" {
		t.Fatal("approved login returned an empty access_token")
	}
	if result.RefreshToken == "" {
		t.Fatal("approved login returned an empty refresh_token — passkey would silently stop renewing the session")
	}

	if resp := doMobileSessionsRequest(t, r.handleRevokeMobileSession, username, http.MethodPost, map[string]string{"id": credRec.ID}); resp.Code != 200 {
		t.Fatalf("revoke: status = %d, body = %s", resp.Code, resp.Body.String())
	}

	if _, err := r.FinishPasskeyLogin(beginLoginSession(), specBuildAssertionJSON(t, username), "10.0.0.1", "vector-test"); err == nil {
		t.Fatal("login with a revoked credential should fail, but returned success")
	} else if err != mobilebff.ErrPasskeyInvalidCredential {
		t.Fatalf("login with a revoked credential: err = %v, expected ErrPasskeyInvalidCredential", err)
	}
}

func TestPasskeyLogin_RefreshTokenRotatesAndInvalidatesOldToken(t *testing.T) {
	const username = "sam"
	r := newPasskeyRouter(t, "panel.example.com")

	specRP := specRealWebAuthnRP(t)
	r.webauthnRP = specRP

	cred := specRegisterRealCredential(t, specRP, username)
	store := r.credentialStore(username)
	credRec, err := store.Add(*cred, "vector device §16.2")
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

	login, err := r.FinishPasskeyLogin(cont, specBuildAssertionJSON(t, username), "10.0.0.1", "vector-test")
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

	if _, err := r.MobileRefresh(login.RefreshToken); err != mobilebff.ErrMobileRefreshInvalid {
		t.Fatalf("replay of the old token: err = %v, expected ErrMobileRefreshInvalid", err)
	}
	if _, err := r.MobileRefresh(login.RefreshToken); err != mobilebff.ErrMobileRefreshInvalid {
		t.Fatalf("second replay: err = %v, expected ErrMobileRefreshInvalid", err)
	}
}
