// supabase.go — the migration to Supabase Auth (GoTrue).
//
// This layer does ONE specific job: validate a username+password combination
// against the self-hosted GoTrue shared with northwind-web.
// It does not mint the v2 JWT (that stays r.auth.Issue), does not touch
// sessions, does not touch TOTP. It only replaces bcrypt.CompareHashAndPassword.
//
// Why the boundary is this narrow:
//   - The v2 JWT stays HS256 with the same secret as GoTrue (an earlier step
//     synchronised them). The 5 jwt.Parse sites keep working byte-identically.
//   - Supabase refresh/logout/MFA arrive in later steps.
//   - Rollback is switching VPSM_AUTH_BACKEND=local — local bcrypt remained
//     viable until it was retired.
//
// Classified errors (the caller decides what to do):
//   - ErrSupabaseInvalidCredentials → 401 to the user, audit "login.fail.supabase"
//   - ErrSupabaseNetworkError       → fall back to local bcrypt when backend=both
//   - ErrSupabaseUnexpected         → 500, audit + logging with a short payload
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

// Errors classified for the caller.
var (
	ErrSupabaseInvalidCredentials = errors.New("supabase: invalid credentials")
	ErrSupabaseNetworkError       = errors.New("supabase: network error")
	ErrSupabaseUnexpected         = errors.New("supabase: unexpected response")
)

// SupabaseClient is the minimal facade over calls to the self-hosted GoTrue.
// It is stateless beyond the credentials — it can be instantiated per request
// or reused. http.Client with a configured timeout.
type SupabaseClient struct {
	BaseURL    string // ex.: https://db.northwind.example
	AnonKey    string // SUPABASE_ANON_KEY (public — apikey header)
	HTTPClient *http.Client
}

// NewSupabaseClient returns a client with a 5s timeout. The caller must ensure
// BaseURL and AnonKey are not empty — if they are, it returns nil (presence is
// checked in main.go at boot).
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

// gotrueTokenRespPartial captures just enough to infer success (the presence
// of access_token) and to classify errors.
type gotrueTokenRespPartial struct {
	AccessToken  string `json:"access_token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	ExpiresIn    int    `json:"expires_in,omitempty"`
	TokenType    string `json:"token_type,omitempty"`
	ErrorCode    string `json:"error_code,omitempty"`
	ErrorDesc    string `json:"error_description,omitempty"`
	Msg          string `json:"msg,omitempty"`
	// User is the identity object GoTrue returns alongside the session. We
	// capture only the email — used on a cookie-based refresh (with no valid
	// access token in the header) to remap email → canonical username via
	// UUIDMap.
	User struct {
		Email string `json:"email,omitempty"`
	} `json:"user,omitempty"`
}

// SupabaseSession captures the tokens returned by /auth/v1/token.
// RefreshToken is what POST /auth/v1/logout uses for server-side revocation;
// the remaining fields are kept for future evolution (transparent refresh).
type SupabaseSession struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int
	// Email is the email of the user who owns the session (when GoTrue includes
	// it in the response — always on the refresh_token grant). It lets us remap
	// the identity without decoding the access_token JWT by hand.
	Email string
}

// VerifyPassword attempts POST /auth/v1/token?grant_type=password against
// GoTrue. On success it returns *SupabaseSession + nil; on failure, nil plus
// one of the Err* values.
//
// The caller decides what to do with the Session:
//   - discard it (the v2 JWT is still minted locally)
//   - keep refresh_token in an httpOnly cookie for logout
//   - future: refresh_token cycled via /auth/v1/token?grant_type=refresh_token
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

// RefreshSession trades a refresh_token for a new session. Used by the
// transparent supabaseCallWithRefresh when the access_token expires.
//
// Errors:
//   - ErrSupabaseInvalidCredentials → refresh invalid/expired (re-login needed)
//   - ErrSupabaseNetworkError       → 5xx or the network is down
//   - ErrSupabaseUnexpected         → unexpected state
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

// AuthCallResult returns the parsed body of an authenticated, user-scoped call,
// plus the effective Session (which may have been rotated if a refresh
// happened). The caller checks Refreshed to update the cookies in the
// operator's response.
type AuthCallResult struct {
	StatusCode int
	Body       []byte
	Refreshed  *SupabaseSession // non-nil when access_token was rotated during the call
}

// AuthenticatedRequest makes a user-scoped call (Bearer access_token) to
// GoTrue with transparent refresh. If the first call returns 401, it tries the
// refresh_token, updates the access_token, and retries ONCE. Failure:
//   - if the refresh also fails → ErrSupabaseInvalidCredentials (re-login needed)
//   - if the network is down → ErrSupabaseNetworkError
//
// method/path/body are the parameters of the GoTrue call; e.g.:
//
//	AuthenticatedRequest(ctx, access, refresh, "POST", "/auth/v1/factors",
//	    map[string]any{"factor_type":"totp","friendly_name":"vpsm"})
//
// The caller is responsible for writing the updated cookie from Refreshed.AccessToken
// (when non-nil) — keep it response-aware.
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
		// No LimitReader here — GoTrue's response on /factors enroll-start
		// includes an inline SVG QR code that can exceed 100KB. A silent limit
		// truncates the body without an error and produces "unexpected end of
		// JSON input" at Unmarshal — poor diagnostics. http.Client has a 5s
		// Timeout that already covers runaway responses. The caller is trusted
		// (internal handlers).
		buf, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return nil, fmt.Errorf("%w: read body (got %d bytes): %v", ErrSupabaseNetworkError, len(buf), readErr)
		}
		return &AuthCallResult{StatusCode: resp.StatusCode, Body: buf}, nil
	}

	// First attempt with the current access_token.
	res, err := doCall(accessToken)
	if err != nil {
		return nil, err
	}
	// 401 → refresh + retry. 403 as well (factor enforcement can return 403).
	if (res.StatusCode == 401 || res.StatusCode == 403) && refreshToken != "" {
		newSess, rerr := c.RefreshSession(ctx, refreshToken)
		if rerr != nil {
			return res, rerr // devolve o res original + refresh err
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

// RevokeRefreshToken calls POST /auth/v1/logout with the refresh_token as the
// Bearer. GoTrue invalidates the refresh server-side. Errors here are
// best-effort: network failure, GoTrue 500, etc. — the local logout ALWAYS
// proceeds even if the remote revocation fails (clearing the cookie on the
// user's side is what matters).
//
// Idempotent: calling it with an already-revoked token returns without error.
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
		// Already revoked or expired — best-effort, not an error.
		return nil
	}
	return fmt.Errorf("%w: status=%d", ErrSupabaseUnexpected, resp.StatusCode)
}

// Fast GoTrue health probe (used by /api/api/health). Issues GET /auth/v1/settings
// (a public endpoint that still requires the apikey — it confirms both that ANON
// authentication works AND that the server answers). Short timeout (2s) so it does
// not slow down the vps-manager health check.
// Returns nil = OK; an error means unreachable/down.
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
