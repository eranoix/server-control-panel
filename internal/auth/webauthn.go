package auth

// webauthn.go — WebAuthn/FIDO2 (passkey) ceremonies: registration and
// "discoverable" login (no username typed). Uses github.com/go-webauthn/webauthn
// v0.18.0.
//
// This file does NOT hold Relying Party configuration state (RPID/
// RPOrigins) and does NOT own any *webauthn.WebAuthn: the functions below
// take that instance as an explicit parameter. The reason: handleChangePassword
// (internal/api/handlers_auth.go) rebuilds the whole *auth.Service via
// auth.New(...) on a password change — anything held as a field of Service
// would be silently wiped at that moment. The *webauthn.WebAuthn instance
// lives in internal/api.Router (built once in NewRouter from
// Config.PublicHostname), outside the lifecycle of *auth.Service.
//
// RPID: MUST be the same hostname that /.well-known/assetlinks.json announces
// (handlers_wellknown.go, via Config.AndroidPackageName/
// AndroidSigningFingerprints) — the Android app only accepts the origin whose
// Digital Asset Links match the signed package, and WebAuthn only accepts the
// origin listed in RPOrigins. An RPID that is not the real production hostname
// (never "localhost", never a wildcard value) invalidates the whole ceremony.
// This project answers on two hostnames with no registrable suffix in common
// (panel.northwind.example vs the *.hstgr.cloud fallback) — WebAuthn requires
// ONE single RPID, so Config.PublicHostname decides which of the two is the
// Relying Party (see docs/RUNBOOK-auth.md).

import (
	"errors"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

// webauthnUser adapts a username plus its persisted CredentialRecords to the
// webauthn.User interface. WebAuthnID is deliberately the bytes of the
// username itself (not a random handle): this project's user list is small,
// fixed, and defined in config (Config.AllUsers), so the "user handle"
// returned by the authenticator in a discoverable login already IS the lookup
// key — no global credential-id -> username index is needed, and
// FinishDiscoverableLogin never has to scan every user.
type webauthnUser struct {
	username string
	records  []CredentialRecord
}

func (u *webauthnUser) WebAuthnID() []byte { return []byte(u.username) }

func (u *webauthnUser) WebAuthnName() string { return u.username }

func (u *webauthnUser) WebAuthnDisplayName() string { return u.username }

func (u *webauthnUser) WebAuthnCredentials() []webauthn.Credential {
	creds := make([]webauthn.Credential, 0, len(u.records))
	for _, r := range u.records {
		creds = append(creds, r.Credential)
	}
	return creds
}

// NewWebAuthnConfig builds the Relying Party configuration. rpID must be the
// bare hostname (no scheme or port); rpOrigin the full origin
// (https://<rpID>) — the same one the Android app uses to identify itself via
// Digital Asset Links.
func NewWebAuthnConfig(rpID, rpOrigin, displayName string) *webauthn.Config {
	if displayName == "" {
		displayName = "VPS Manager"
	}
	return &webauthn.Config{
		RPID:          rpID,
		RPDisplayName: displayName,
		RPOrigins:     []string{rpOrigin},
	}
}

// BeginRegistrationForUser starts a passkey registration ceremony for
// `username`. `approved` must be ONLY the user's already-approved credentials
// (WebAuthnCredentialsStore.List(), never ListAll()) — using those to exclude
// duplicates is safe; using pending credentials here would leak the existence
// of an in-flight pairing to someone who did not start it.
func BeginRegistrationForUser(w *webauthn.WebAuthn, username string, approved []CredentialRecord) (*protocol.CredentialCreation, *webauthn.SessionData, error) {
	if w == nil {
		return nil, nil, errors.New("webauthn: Relying Party instance not configured")
	}
	u := &webauthnUser{username: username, records: approved}
	excl := make([]protocol.CredentialDescriptor, 0, len(approved))
	for _, r := range approved {
		excl = append(excl, r.Credential.Descriptor())
	}
	return w.BeginRegistration(u, webauthn.WithExclusions(excl))
}

// FinishRegistrationForUser validates the browser/authenticator response
// against the SessionData issued by BeginRegistrationForUser and returns the
// resulting webauthn.Credential. This function does NOT persist the credential
// and does NOT decide pending/approved status — the caller (mobilebff) owns
// that decision via WebAuthnCredentialsStore.Add, which ALWAYS starts pending:
// a registered credential is never, on its own, a login path.
func FinishRegistrationForUser(w *webauthn.WebAuthn, username string, approved []CredentialRecord, session webauthn.SessionData, responseBody []byte) (*webauthn.Credential, error) {
	if w == nil {
		return nil, errors.New("webauthn: Relying Party instance not configured")
	}
	u := &webauthnUser{username: username, records: approved}
	parsed, err := protocol.ParseCredentialCreationResponseBytes(responseBody)
	if err != nil {
		return nil, err
	}
	return w.CreateCredential(u, session, parsed)
}

// BeginDiscoverableLogin starts a login ceremony with no user
// (passwordless/usernameless): the browser resolves, among the passkeys
// resident on the authenticator, which one serves this RPID. It deliberately
// takes no username — there is no "does this user exist?" branch here
// whose answer or run time could leak through enumeration.
func BeginDiscoverableLogin(w *webauthn.WebAuthn) (*protocol.CredentialAssertion, *webauthn.SessionData, error) {
	if w == nil {
		return nil, nil, errors.New("webauthn: Relying Party instance not configured")
	}
	return w.BeginDiscoverableLogin()
}

// OpenUserCredentialStore resolves a username (decoded from the userHandle
// returned by the authenticator — see webauthnUser.WebAuthnID) to THAT user's
// credential store. It is implemented by the caller (mobilebff, via
// internal/auth.WebAuthnCredentialsPath) — this package never opens a file on
// its own, so there is no code path here that could open the wrong file.
type OpenUserCredentialStore func(username string) (*WebAuthnCredentialsStore, error)

// FinishDiscoverableLogin validates the assertion response against the
// SessionData issued by BeginDiscoverableLogin. `openStore` is called only
// with the username THIS VERY PROCESS decoded from the response's userHandle —
// never with a value coming from anywhere else — so there is no way to open
// the file of a user other than the one that signed the assertion.
//
// `openStore` must load ALL of the user's credentials (ListAll, not List): a
// still-pending credential has to be RECOGNISABLE here (so the caller can then
// refuse it with pending_approval) — hiding it from go-webauthn would make
// login/finish answer "unknown credential" both for a legitimate pending
// pairing and for an attack, destroying the distinction the approval UI
// depends on.
//
// Returns the username and the UPDATED *webauthn.Credential (new signature
// counter) — the caller must persist it back via
// WebAuthnCredentialsStore.UpdateCredential.
func FinishDiscoverableLogin(w *webauthn.WebAuthn, openStore OpenUserCredentialStore, session webauthn.SessionData, responseBody []byte) (username string, credential *webauthn.Credential, err error) {
	if w == nil {
		return "", nil, errors.New("webauthn: Relying Party instance not configured")
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(responseBody)
	if err != nil {
		return "", nil, err
	}
	handler := func(rawID, userHandle []byte) (webauthn.User, error) {
		uname := string(userHandle)
		if uname == "" {
			return nil, errors.New("empty userHandle")
		}
		store, serr := openStore(uname)
		if serr != nil {
			return nil, serr
		}
		recs, serr := store.ListAll()
		if serr != nil {
			return nil, serr
		}
		return &webauthnUser{username: uname, records: recs}, nil
	}
	user, cred, verr := w.ValidatePasskeyLogin(handler, session, parsed)
	if verr != nil {
		return "", nil, verr
	}
	wu, ok := user.(*webauthnUser)
	if !ok || cred == nil {
		return "", nil, errors.New("webauthn: unexpected user type returned by the library")
	}
	return wu.username, cred, nil
}
