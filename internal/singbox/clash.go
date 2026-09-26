package singbox

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Summary is the aggregate live state of the tunnel from the Clash API.
//
// The official sing-box build exposes the Clash API but its /connections schema
// does NOT attribute a connection to a VLESS user (no per-user field) — that
// would need the V2Ray API (build tag with_v2ray_api). So we surface tunnel-wide
// activity, not per-device liveness.
type Summary struct {
	ActiveConns int   `json:"active_conns"`
	Up          int64 `json:"up"`   // cumulative upload bytes across active conns
	Down        int64 `json:"down"` // cumulative download bytes across active conns
}

// ClashClient reads aggregate live state from sing-box's Clash API.
type ClashClient struct {
	BaseURL string
	Secret  string
	httpc   *http.Client
}

func NewClash(baseURL, secret string) *ClashClient {
	return &ClashClient{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Secret:  secret,
		httpc:   &http.Client{Timeout: 6 * time.Second},
	}
}

// Aggregate returns the tunnel-wide active connection count and cumulative
// traffic. Returns an error (so the caller can render without live data) when
// the Clash API is unreachable.
func (c *ClashClient) Aggregate(ctx context.Context) (*Summary, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/connections", nil)
	if err != nil {
		return nil, err
	}
	if c.Secret != "" {
		req.Header.Set("Authorization", "Bearer "+c.Secret)
	}
	resp, err := c.httpc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("clash api unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("clash api HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	var body struct {
		Connections []struct {
			Upload   int64 `json:"upload"`
			Download int64 `json:"download"`
		} `json:"connections"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, fmt.Errorf("invalid clash api response: %w", err)
	}
	s := &Summary{ActiveConns: len(body.Connections)}
	for _, cn := range body.Connections {
		s.Up += cn.Upload
		s.Down += cn.Download
	}
	return s, nil
}
