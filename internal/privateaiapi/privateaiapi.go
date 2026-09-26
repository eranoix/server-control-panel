// Package privateaiapi is a thin client over the private-ai-api admin API.
//
// private-ai-api (https://127.0.0.1:8787 by default) is a self-hosted OAuth
// proxy that turns a Claude Max subscription into a private API. It exposes an
// /admin/* surface — gated by a bearer ADMIN_TOKEN — to manage the user-facing
// API keys ("tokens"): list, create, revoke and edit limits.
//
// VPSM proxies these endpoints server-side so the ADMIN_TOKEN never reaches the
// browser. To stay resilient to upstream shape changes, most methods pass the
// raw JSON body + HTTP status straight through (see Result); only Status fans
// several read endpoints into one object for the dashboard.
package privateaiapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultBaseURL is where the private-ai-api admin surface listens on the host.
const DefaultBaseURL = "http://127.0.0.1:8787"

const (
	requestTimeout = 8 * time.Second
	maxBodyBytes   = 1 << 20 // 1 MiB — admin responses are tiny
)

// Client talks to one private-ai-api instance with one admin token.
type Client struct {
	BaseURL    string
	AdminToken string
	httpc      *http.Client
}

// New builds a Client. An empty baseURL falls back to DefaultBaseURL.
func New(baseURL, adminToken string) *Client {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{
		BaseURL:    strings.TrimRight(baseURL, "/"),
		AdminToken: adminToken,
		httpc:      &http.Client{Timeout: requestTimeout},
	}
}

// Result carries the upstream HTTP status and raw JSON body so callers can
// pass the response through transparently (preserving the upstream status).
type Result struct {
	Status int
	Body   []byte
}

// do performs one authenticated request against the admin API. A nil body
// sends no payload; any other value is JSON-encoded.
func (c *Client) do(ctx context.Context, method, path string, body any) (*Result, error) {
	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("privateaiapi: marshal body: %w", err)
		}
		reader = bytes.NewReader(buf)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, reader)
	if err != nil {
		return nil, fmt.Errorf("privateaiapi: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.AdminToken)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("privateaiapi: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("privateaiapi: read response: %w", err)
	}
	return &Result{Status: resp.StatusCode, Body: raw}, nil
}

// ListKeys returns the raw `{ data: [...] }` body from GET /admin/api/keys.
func (c *Client) ListKeys(ctx context.Context) (*Result, error) {
	return c.do(ctx, http.MethodGet, "/admin/api/keys", nil)
}

// CreateKey forwards a create request to POST /admin/api/keys. The upstream
// returns 201 `{ plaintext, row }` — the plaintext is only ever shown here.
// body is the validated create payload (name + optional limits).
func (c *Client) CreateKey(ctx context.Context, body any) (*Result, error) {
	return c.do(ctx, http.MethodPost, "/admin/api/keys", body)
}

// RevokeKey revokes a key by id via POST /admin/api/keys/:id/revoke.
func (c *Client) RevokeKey(ctx context.Context, id int64) (*Result, error) {
	return c.do(ctx, http.MethodPost, fmt.Sprintf("/admin/api/keys/%d/revoke", id), nil)
}

// UpdateKey edits a key's limits via PATCH /admin/api/keys/:id.
func (c *Client) UpdateKey(ctx context.Context, id int64, body any) (*Result, error) {
	return c.do(ctx, http.MethodPatch, fmt.Sprintf("/admin/api/keys/%d", id), body)
}

// Status fans in the read-only admin endpoints (oauth-status, system, stats)
// into a single object for the dashboard. A failing sub-call yields a null for
// that key plus an `errors` map entry, instead of failing the whole status.
func (c *Client) Status(ctx context.Context) map[string]any {
	out := map[string]any{}
	errs := map[string]string{}

	sub := []struct{ key, path string }{
		{"oauth", "/admin/api/oauth-status"},
		{"system", "/admin/api/system"},
		{"stats", "/admin/api/stats?rangeHours=24"},
	}
	for _, s := range sub {
		res, err := c.do(ctx, http.MethodGet, s.path, nil)
		if err != nil {
			out[s.key] = nil
			errs[s.key] = err.Error()
			continue
		}
		if res.Status != http.StatusOK {
			out[s.key] = nil
			errs[s.key] = fmt.Sprintf("HTTP %d", res.Status)
			continue
		}
		var v any
		if jerr := json.Unmarshal(res.Body, &v); jerr != nil {
			out[s.key] = nil
			errs[s.key] = "invalid response"
			continue
		}
		out[s.key] = v
	}
	if len(errs) > 0 {
		out["errors"] = errs
	}
	return out
}
