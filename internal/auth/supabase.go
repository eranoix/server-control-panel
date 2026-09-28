package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var (
	ErrSupabaseInvalidCredentials = errors.New("supabase: invalid credentials")
	ErrSupabaseNetworkError       = errors.New("supabase: network error")
	ErrSupabaseUnexpected         = errors.New("supabase: unexpected response")
)

type SupabaseClient struct {
	BaseURL    string
	AnonKey    string
	HTTPClient *http.Client
}

func NewSupabaseClient(baseURL, anonKey string) *SupabaseClient {
	baseURL = strings.TrimRight(baseURL, "/")
	if baseURL == "" || anonKey == "" {
		return nil
	}
	return &SupabaseClient{
		BaseURL: baseURL,
		AnonKey: anonKey,
		HTTPClient: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

type gotrueTokenRespPartial struct {
	AccessToken  string `json:"access_token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	ExpiresIn    int    `json:"expires_in,omitempty"`
	TokenType    string `json:"token_type,omitempty"`
	ErrorCode    string `json:"error_code,omitempty"`
	ErrorDesc    string `json:"error_description,omitempty"`
	Msg          string `json:"msg,omitempty"`
	User         struct {
		Email string `json:"email,omitempty"`
	} `json:"user,omitempty"`
}

type SupabaseSession struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int
	Email        string
}

func (c *SupabaseClient) VerifyPassword(ctx context.Context, email, password string) (*SupabaseSession, error) {
	if c == nil {
		return nil, ErrSupabaseNetworkError
	}
	url := c.BaseURL + "/auth/v1/token?grant_type=password"

	payload, err := json.Marshal(map[string]string{
		"email":    email,
		"password": password,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: marshal: %v", ErrSupabaseUnexpected, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("%w: build request: %v", ErrSupabaseUnexpected, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("apikey", c.AnonKey)
	req.Header.Set("Authorization", "Bearer "+c.AnonKey)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSupabaseNetworkError, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%w: read body (got %d bytes): %v", ErrSupabaseNetworkError, len(body), err)
	}

	if resp.StatusCode == http.StatusOK {
		var rb gotrueTokenRespPartial
		if jerr := json.Unmarshal(body, &rb); jerr != nil || rb.AccessToken == "" {
			return nil, fmt.Errorf("%w: 200 without access_token (short body=%d)", ErrSupabaseUnexpected, len(body))
		}
		return &SupabaseSession{
			AccessToken:  rb.AccessToken,
			RefreshToken: rb.RefreshToken,
			ExpiresIn:    rb.ExpiresIn,
		}, nil
	}

	if resp.StatusCode >= 500 {
		return nil, fmt.Errorf("%w: status=%d", ErrSupabaseNetworkError, resp.StatusCode)
	}
	var rb gotrueTokenRespPartial
	_ = json.Unmarshal(body, &rb)
	if rb.ErrorCode == "invalid_credentials" || rb.ErrorCode == "invalid_grant" {
		return nil, ErrSupabaseInvalidCredentials
	}
	if rb.ErrorCode == "mfa_required" {
		return nil, nil
	}
	return nil, fmt.Errorf("%w: status=%d error_code=%q msg=%q", ErrSupabaseUnexpected, resp.StatusCode, rb.ErrorCode, rb.Msg)
}

func (c *SupabaseClient) RefreshSession(ctx context.Context, refreshToken string) (*SupabaseSession, error) {
	if c == nil {
		return nil, ErrSupabaseNetworkError
	}
	url := c.BaseURL + "/auth/v1/token?grant_type=refresh_token"
	payload, _ := json.Marshal(map[string]string{"refresh_token": refreshToken})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("%w: build: %v", ErrSupabaseUnexpected, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("apikey", c.AnonKey)
	req.Header.Set("Authorization", "Bearer "+c.AnonKey)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSupabaseNetworkError, err)
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, fmt.Errorf("%w: refresh read body (got %d bytes): %v", ErrSupabaseNetworkError, len(body), readErr)
	}

	if resp.StatusCode == http.StatusOK {
		var rb gotrueTokenRespPartial
		if err := json.Unmarshal(body, &rb); err != nil || rb.AccessToken == "" {
			return nil, fmt.Errorf("%w: 200 without access_token", ErrSupabaseUnexpected)
		}
		return &SupabaseSession{AccessToken: rb.AccessToken, RefreshToken: rb.RefreshToken, ExpiresIn: rb.ExpiresIn, Email: rb.User.Email}, nil
	}
	if resp.StatusCode >= 500 {
		return nil, fmt.Errorf("%w: status=%d", ErrSupabaseNetworkError, resp.StatusCode)
	}
	var rb gotrueTokenRespPartial
	_ = json.Unmarshal(body, &rb)
	if rb.ErrorCode == "invalid_grant" || rb.ErrorCode == "invalid_credentials" || resp.StatusCode == 401 || resp.StatusCode == 403 {
		return nil, ErrSupabaseInvalidCredentials
	}
	return nil, fmt.Errorf("%w: status=%d code=%q", ErrSupabaseUnexpected, resp.StatusCode, rb.ErrorCode)
}

type AuthCallResult struct {
	StatusCode int
	Body       []byte
	Refreshed  *SupabaseSession
}

func (c *SupabaseClient) AuthenticatedRequest(ctx context.Context, accessToken, refreshToken, method, path string, body any) (*AuthCallResult, error) {
	if c == nil {
		return nil, ErrSupabaseNetworkError
	}

	doCall := func(token string) (*AuthCallResult, error) {
		var reqBody io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			reqBody = bytes.NewReader(b)
		}
		req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, reqBody)
		if err != nil {
			return nil, fmt.Errorf("%w: build: %v", ErrSupabaseUnexpected, err)
		}
		req.Header.Set("apikey", c.AnonKey)
		req.Header.Set("Authorization", "Bearer "+token)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := c.HTTPClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrSupabaseNetworkError, err)
		}
		defer resp.Body.Close()
		buf, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return nil, fmt.Errorf("%w: read body (got %d bytes): %v", ErrSupabaseNetworkError, len(buf), readErr)
		}
		return &AuthCallResult{StatusCode: resp.StatusCode, Body: buf}, nil
	}

	res, err := doCall(accessToken)
	if err != nil {
		return nil, err
	}
	if (res.StatusCode == 401 || res.StatusCode == 403) && refreshToken != "" {
		newSess, rerr := c.RefreshSession(ctx, refreshToken)
		if rerr != nil {
			return res, rerr
		}
		retryRes, rerr := doCall(newSess.AccessToken)
		if rerr != nil {
			return res, rerr
		}
		retryRes.Refreshed = newSess
		return retryRes, nil
	}
	return res, nil
}

func (c *SupabaseClient) RevokeRefreshToken(ctx context.Context, refreshToken string) error {
	if c == nil || refreshToken == "" {
		return nil
	}
	url := c.BaseURL + "/auth/v1/logout"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return fmt.Errorf("%w: build request: %v", ErrSupabaseUnexpected, err)
	}
	req.Header.Set("apikey", c.AnonKey)
	req.Header.Set("Authorization", "Bearer "+refreshToken)
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrSupabaseNetworkError, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil
	}
	return fmt.Errorf("%w: status=%d", ErrSupabaseUnexpected, resp.StatusCode)
}

func (c *SupabaseClient) Health(ctx context.Context) error {
	if c == nil {
		return fmt.Errorf("%w: client not configured", ErrSupabaseUnexpected)
	}
	hctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(hctx, http.MethodGet, c.BaseURL+"/auth/v1/settings", nil)
	if err != nil {
		return fmt.Errorf("%w: build request: %v", ErrSupabaseUnexpected, err)
	}
	req.Header.Set("apikey", c.AnonKey)
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrSupabaseNetworkError, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: status=%d", ErrSupabaseUnexpected, resp.StatusCode)
	}
	return nil
}
