package fcmpush

import (
	"context"
	"encoding/json"
	"fmt"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// fcmMessagingScope is the single OAuth2 scope FCM HTTP v1 needs to send
// messages — narrower than the broad "cloud-platform" scope some Firebase
// Admin SDK examples request.
const fcmMessagingScope = "https://www.googleapis.com/auth/firebase.messaging"

// serviceAccountFile reads just the one field google.JWTConfigFromJSON's
// own return type (*jwt.Config) does not expose: the GCP project ID the
// FCM v1 endpoint path requires (.../v1/projects/{project}/messages:send).
type serviceAccountFile struct {
	ProjectID string `json:"project_id"`
}

// credential is what Sender needs from the service-account JSON: an
// oauth2.TokenSource that mints/caches/refreshes its own bearer token — the
// library's internal reuse-until-near-expiry wrapping already satisfies
// "cache the token and refresh before its ~1h expiry", so Sender never
// tracks token timestamps itself — plus the project ID for the send URL.
type credential struct {
	projectID string
	tokens    oauth2.TokenSource
}

// newCredential parses saJSON — the FCM service-account key, loaded by the
// caller from the vault (this package never touches internal/secrets
// itself, mirroring how internal/webpush never touches secret/config
// loading) — into a credential backed by a self-refreshing TokenSource.
func newCredential(ctx context.Context, saJSON []byte) (*credential, error) {
	var f serviceAccountFile
	if err := json.Unmarshal(saJSON, &f); err != nil {
		return nil, fmt.Errorf("fcmpush: parsing service account JSON: %w", err)
	}
	if f.ProjectID == "" {
		return nil, fmt.Errorf("fcmpush: service account JSON missing project_id")
	}
	jwtCfg, err := google.JWTConfigFromJSON(saJSON, fcmMessagingScope)
	if err != nil {
		return nil, fmt.Errorf("fcmpush: parsing service account credentials: %w", err)
	}
	return &credential{projectID: f.ProjectID, tokens: jwtCfg.TokenSource(ctx)}, nil
}

// bearer returns a valid "Bearer <token>" header value, transparently
// refreshing the underlying oauth2.TokenSource when the cached token is
// near expiry.
func (c *credential) bearer() (string, error) {
	tok, err := c.tokens.Token()
	if err != nil {
		return "", fmt.Errorf("fcmpush: minting oauth2 token: %w", err)
	}
	return "Bearer " + tok.AccessToken, nil
}
