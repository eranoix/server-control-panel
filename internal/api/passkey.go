package api

// passkey.go — *Router's implementation of mobilebff.PasskeyBackend: the
// bridge between the real WebAuthn ceremonies (internal/auth/webauthn.go)
// and the pending/approved credential store
// (internal/auth/webauthn_credentials.go) on one side, and the four public
// HTTP routes of the mobile BFF (internal/mobilebff/auth_passkey.go) on the
// other.
//
// THE CENTRAL GUARANTEE: FinishPasskeyRegistration NEVER issues a token — it
// only persists the credential through WebAuthnCredentialsStore.Add, which
// ALWAYS starts in pending status. The only path in this file able to come
// out with a session JWT is FinishPasskeyLogin, and only when the matched
// credential is already approved (its status read back from the SAME store
// the ceremony consulted, never assumed from the cryptographic result).
//
// The RPID (auth.NewWebAuthnConfig) comes from Config.PublicHostname — the
// SAME hostname that /.well-known/assetlinks.json announces through
// Config.AndroidPackageName/AndroidSigningFingerprints
// (handlers_wellknown.go). A divergent value here silently breaks both the
// App Link and every passkey ceremony; see docs/RUNBOOK-auth.md.

import (
	"encoding/json"
	"fmt"
	"log"

	"github.com/go-webauthn/webauthn/webauthn"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/mobilebff"
	"server-control-panel/internal/notify"
)

// initPasskey assembles the WebAuthn Relying Party from Config.PublicHostname.
// Deliberately OFF *auth.Service (see the top comment of
// internal/auth/webauthn.go): handleChangePassword rebuilds the whole r.auth
// through auth.New(...), and anything stored there would be erased at that
// moment. r.webauthnRP survives that swap because it lives only on Router.
//
// It stays nil (passkeys inert, graceful degradation) when PublicHostname is
// empty — there is no safe RPID to guess (never "localhost", never a
// wildcard value).
func (r *Router) initPasskey() {
	if r.cfg.PublicHostname == "" {
		log.Printf("passkey: Config.PublicHostname is empty — WebAuthn ceremonies disabled")
		return
	}
	rpOrigin := "https://" + r.cfg.PublicHostname
	w, err := webauthn.New(auth.NewWebAuthnConfig(r.cfg.PublicHostname, rpOrigin, "VPS Manager"))
	if err != nil {
		log.Printf("passkey: invalid WebAuthn configuration (%v) — ceremonies disabled", err)
		return
	}
	r.webauthnRP = w
}

// credentialStore opens `username`'s credential store — always by the same
// per-user path (WebAuthnCredentialsPath), never by a global index. It mirrors
// exactly the trusted_devices.go pattern already used in password login.
func (r *Router) credentialStore(username string) *auth.WebAuthnCredentialsStore {
	return auth.NewWebAuthnCredentialsStore(auth.WebAuthnCredentialsPath(r.cfg.DataDir, username))
}

// BeginPasskeyRegistration implements mobilebff.PasskeyBackend.
func (r *Router) BeginPasskeyRegistration(regToken string) ([]byte, string, error) {
	if r.webauthnRP == nil {
		return nil, "", mobilebff.ErrPasskeyUnavailable
	}
	username, err := r.auth.VerifyWebAuthnRegToken(regToken)
	if err != nil {
		return nil, "", mobilebff.ErrPasskeyInvalidToken
	}
	approved, err := r.credentialStore(username).List()
	if err != nil {
		return nil, "", fmt.Errorf("passkey: reading approved credentials: %w", err)
	}
	creation, session, err := auth.BeginRegistrationForUser(r.webauthnRP, username, approved)
	if err != nil {
		return nil, "", fmt.Errorf("passkey: starting registration: %w", err)
	}
	optionsJSON, err := json.Marshal(creation)
	if err != nil {
		return nil, "", fmt.Errorf("passkey: serializing registration options: %w", err)
	}
	sessionJSON, err := json.Marshal(session)
	if err != nil {
		return nil, "", fmt.Errorf("passkey: serializing registration session: %w", err)
	}
	cont, err := r.auth.IssueWebAuthnRegSessionToken(username, sessionJSON)
	if err != nil {
		return nil, "", fmt.Errorf("passkey: issuing continuation token: %w", err)
	}
	return optionsJSON, cont, nil
}

// FinishPasskeyRegistration implements mobilebff.PasskeyBackend. Success here
// ALWAYS ends in WebAuthnCredentialsStore.Add — which always starts pending —
// and NEVER in auth.Service.Issue. There is no token field in this function's
// return: the shape of the type already stops the caller trying to issue one.
func (r *Router) FinishPasskeyRegistration(continuationToken, label string, credentialJSON []byte) error {
	if r.webauthnRP == nil {
		return mobilebff.ErrPasskeyUnavailable
	}
	username, sessionJSON, err := r.auth.VerifyWebAuthnRegSessionToken(continuationToken)
	if err != nil {
		return mobilebff.ErrPasskeyInvalidToken
	}
	var session webauthn.SessionData
	if err := json.Unmarshal(sessionJSON, &session); err != nil {
		return fmt.Errorf("passkey: registration session corrupted: %w", err)
	}
	approved, err := r.credentialStore(username).List()
	if err != nil {
		return fmt.Errorf("passkey: reading approved credentials: %w", err)
	}
	cred, err := auth.FinishRegistrationForUser(r.webauthnRP, username, approved, session, credentialJSON)
	if err != nil {
		return mobilebff.ErrPasskeyInvalidCredential
	}
	rec, err := r.credentialStore(username).Add(*cred, label)
	if err != nil {
		return fmt.Errorf("passkey: persisting the credential: %w", err)
	}
	if r.notify != nil {
		r.notify.Dispatch(notify.Event{
			Type:     notify.TypeDevicePairingPending,
			Severity: notify.SeverityInfo,
			Source:   "passkey",
			Owner:    username,
			Title:    "New device waiting for approval",
			Body:     fmt.Sprintf("%q asked to sign in with a passkey. Approve it in the panel if that was you.", label),
			TS:       rec.CreatedAt.Unix(),
			DedupKey: "passkey-pending:" + rec.ID,
		})
	}
	return nil
}

// BeginPasskeyLogin implements mobilebff.PasskeyBackend. Deliberately without
// a user parameter — see auth.BeginDiscoverableLogin.
func (r *Router) BeginPasskeyLogin() ([]byte, string, error) {
	if r.webauthnRP == nil {
		return nil, "", mobilebff.ErrPasskeyUnavailable
	}
	assertion, session, err := auth.BeginDiscoverableLogin(r.webauthnRP)
	if err != nil {
		return nil, "", fmt.Errorf("passkey: starting login: %w", err)
	}
	optionsJSON, err := json.Marshal(assertion)
	if err != nil {
		return nil, "", fmt.Errorf("passkey: serializing login options: %w", err)
	}
	sessionJSON, err := json.Marshal(session)
	if err != nil {
		return nil, "", fmt.Errorf("passkey: serializing login session: %w", err)
	}
	cont, err := r.auth.IssueWebAuthnLoginSessionToken(sessionJSON)
	if err != nil {
		return nil, "", fmt.Errorf("passkey: issuing continuation token: %w", err)
	}
	return optionsJSON, cont, nil
}

// FinishPasskeyLogin implements mobilebff.PasskeyBackend.
//
// Enumeration resistance (a non-negotiable requirement): a userHandle that
// corresponds to nobody (credentialStore opens a file that does not exist,
// ListAll returns empty) and a bad signature from a real user fall into the
// SAME error branch below — auth.FinishDiscoverableLogin fails in both cases,
// and the only handling here is ErrPasskeyInvalidCredential, never revealing
// which of the two happened.
func (r *Router) FinishPasskeyLogin(continuationToken string, credentialJSON []byte, ip, userAgent string) (mobilebff.MobileLoginResult, error) {
	if r.webauthnRP == nil {
		return mobilebff.MobileLoginResult{}, mobilebff.ErrPasskeyUnavailable
	}
	sessionJSON, err := r.auth.VerifyWebAuthnLoginSessionToken(continuationToken)
	if err != nil {
		return mobilebff.MobileLoginResult{}, mobilebff.ErrPasskeyInvalidToken
	}
	var session webauthn.SessionData
	if err := json.Unmarshal(sessionJSON, &session); err != nil {
		return mobilebff.MobileLoginResult{}, fmt.Errorf("passkey: login session corrupted: %w", err)
	}
	openStore := func(username string) (*auth.WebAuthnCredentialsStore, error) {
		return r.credentialStore(username), nil
	}
	username, cred, err := auth.FinishDiscoverableLogin(r.webauthnRP, openStore, session, credentialJSON)
	if err != nil {
		return mobilebff.MobileLoginResult{}, mobilebff.ErrPasskeyInvalidCredential
	}
	store := r.credentialStore(username)
	rec, found, err := store.CredentialByID(auth.CredentialRecordID(*cred))
	if err != nil {
		return mobilebff.MobileLoginResult{}, fmt.Errorf("passkey: reading credential status: %w", err)
	}
	if !found {
		// Defensive: FinishDiscoverableLogin only returns a credential that came
		// from the same store — reaching here would indicate a bug, not an attack.
		return mobilebff.MobileLoginResult{}, mobilebff.ErrPasskeyInvalidCredential
	}
	// Persist the updated SignCount regardless of status: even a pending
	// credential has to keep the defence against authenticator cloning working
	// while it waits for approval.
	if err := store.UpdateCredential(*cred); err != nil {
		return mobilebff.MobileLoginResult{}, fmt.Errorf("passkey: updating the signature counter: %w", err)
	}
	if rec.Status != auth.CredentialStatusApproved {
		return mobilebff.MobileLoginResult{}, mobilebff.ErrPasskeyPendingApproval
	}
	token, _, err := r.auth.Issue(username, &auth.IssueMeta{IP: ip, UserAgent: userAgent})
	if err != nil {
		return mobilebff.MobileLoginResult{}, fmt.Errorf("passkey: issuing session: %w", err)
	}
	// The passkey is the product's primary path — it needs the SAME rotating
	// refresh token password login already got, otherwise the main flow logs the
	// admin out when the access token expires while the password fallback goes on
	// renewing silently. The device label comes from the credential itself (set at
	// register/finish), not from a new field in the login body — the passkey
	// already knows who it is.
	refreshTok, err := r.mobileRefreshStoreFor(username).Mint(username, rec.Label)
	if err != nil {
		return mobilebff.MobileLoginResult{}, fmt.Errorf("passkey: issuing refresh token: %w", err)
	}
	return mobilebff.MobileLoginResult{
		AccessToken:  token,
		RefreshToken: refreshTok,
		ExpiresIn:    int(auth.TokenTTL.Seconds()),
	}, nil
}

// ConsumePairingTicket implements mobilebff.PasskeyBackend. It consumes
// (one-shot) a QR-code pairing ticket issued by handleMobilePairStart and, if
// it is valid, mints a webauthn_reg token for the SAME username that produced
// the ticket — never a username supplied by the pairing client, and never a
// session. The reg_token returned follows exactly the path register/begin
// already expects (auth.VerifyWebAuthnRegToken).
func (r *Router) ConsumePairingTicket(ticket string) (string, error) {
	username, ok := auth.ConsumePairingTicket(ticket)
	if !ok {
		return "", mobilebff.ErrPairingTicketInvalid
	}
	regToken, err := r.auth.IssueWebAuthnRegToken(username)
	if err != nil {
		return "", fmt.Errorf("pairing: issuing reg_token: %w", err)
	}
	return regToken, nil
}

// AllowLoginAttempt/ResetLoginAttempts implement mobilebff.PasskeyBackend by
// reusing the SAME limiter as password login — a passkey does not get an
// attempt budget of its own.
func (r *Router) AllowLoginAttempt(ip string) bool {
	if r.limiter == nil {
		return true
	}
	return r.limiter.Allow(ip)
}

func (r *Router) ResetLoginAttempts(ip string) {
	if r.limiter == nil {
		return
	}
	r.limiter.Reset(ip)
}

// Compile-time guarantee: *Router satisfies mobilebff.PasskeyBackend.
var _ mobilebff.PasskeyBackend = (*Router)(nil)
