package api

import (
	"encoding/json"
	"fmt"
	"log"

	"github.com/go-webauthn/webauthn/webauthn"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/mobilebff"
	"server-control-panel/internal/notify"
)

func (r *Router) initPasskey() {
	if r.cfg.PublicHostname == "" {
		log.Printf("passkey: Config.PublicHostname is empty — WebAuthn ceremonies disabled")
		return
	}
	rpOrigin := "https://" + r.cfg.PublicHostname
	w, err := webauthn.New(auth.NewWebAuthnConfig(r.cfg.PublicHostname, rpOrigin, "Server Control Panel"))
	if err != nil {
		log.Printf("passkey: invalid WebAuthn configuration (%v) — ceremonies disabled", err)
		return
	}
	r.webauthnRP = w
}

func (r *Router) credentialStore(username string) *auth.WebAuthnCredentialsStore {
	return auth.NewWebAuthnCredentialsStore(auth.WebAuthnCredentialsPath(r.cfg.DataDir, username))
}

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
		return mobilebff.MobileLoginResult{}, mobilebff.ErrPasskeyInvalidCredential
	}
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

var _ mobilebff.PasskeyBackend = (*Router)(nil)
