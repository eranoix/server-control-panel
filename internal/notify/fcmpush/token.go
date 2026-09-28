package fcmpush

import (
	"context"
	"encoding/json"
	"fmt"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const fcmMessagingScope = "https://www.googleapis.com/auth/firebase.messaging"

type serviceAccountFile struct {
	ProjectID string `json:"project_id"`
}

type credential struct {
	projectID string
	tokens    oauth2.TokenSource
}

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

func (c *credential) bearer() (string, error) {
	tok, err := c.tokens.Token()
	if err != nil {
		return "", fmt.Errorf("fcmpush: minting oauth2 token: %w", err)
	}
	return "Bearer " + tok.AccessToken, nil
}
