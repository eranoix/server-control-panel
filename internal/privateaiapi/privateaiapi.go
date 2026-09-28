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

const DefaultBaseURL = "http://127.0.0.1:8787"

const (
	requestTimeout = 8 * time.Second
	maxBodyBytes   = 1 << 20
)

type Client struct {
	BaseURL    string
	AdminToken string
	httpc      *http.Client
}

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

type Result struct {
	Status int
	Body   []byte
}

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

func (c *Client) ListKeys(ctx context.Context) (*Result, error) {
	return c.do(ctx, http.MethodGet, "/admin/api/keys", nil)
}

func (c *Client) CreateKey(ctx context.Context, body any) (*Result, error) {
	return c.do(ctx, http.MethodPost, "/admin/api/keys", body)
}

func (c *Client) RevokeKey(ctx context.Context, id int64) (*Result, error) {
	return c.do(ctx, http.MethodPost, fmt.Sprintf("/admin/api/keys/%d/revoke", id), nil)
}

func (c *Client) UpdateKey(ctx context.Context, id int64, body any) (*Result, error) {
	return c.do(ctx, http.MethodPatch, fmt.Sprintf("/admin/api/keys/%d", id), body)
}

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
