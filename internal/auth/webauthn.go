package auth

import (
	"errors"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

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

func NewWebAuthnConfig(rpID, rpOrigin, displayName string) *webauthn.Config {
	if displayName == "" {
		displayName = "Server Control Panel"
	}
	return &webauthn.Config{
		RPID:          rpID,
		RPDisplayName: displayName,
		RPOrigins:     []string{rpOrigin},
	}
}

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

func BeginDiscoverableLogin(w *webauthn.WebAuthn) (*protocol.CredentialAssertion, *webauthn.SessionData, error) {
	if w == nil {
		return nil, nil, errors.New("webauthn: Relying Party instance not configured")
	}
	return w.BeginDiscoverableLogin()
}

type OpenUserCredentialStore func(username string) (*WebAuthnCredentialsStore, error)

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
