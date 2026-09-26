// Package adguard is a thin client over the AdGuard Home admin API.
//
// AdGuard Home (http://127.0.0.1:3000 by default) is a self-hosted DNS filter
// that blocks ads/trackers/telemetry. It runs on the host in Docker, published
// only on loopback. VPSM proxies its control surface server-side (Segurança →
// AdGuard) so the admin credentials never reach the browser and the panel is
// gated by VPSM's own auth instead of being exposed publicly.
//
// The admin API uses HTTP Basic auth. This client covers the two things the
// panel needs: read a combined status (protection on/off + stats) and toggle
// protection (with an optional pause duration).
package adguard

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultBaseURL is where the AdGuard Home admin surface listens on the host.
const DefaultBaseURL = "http://127.0.0.1:3000"

const (
	requestTimeout = 8 * time.Second
	maxBodyBytes   = 1 << 20 // 1 MiB — control responses are small
)

// Client talks to one AdGuard Home instance with one set of admin credentials.
type Client struct {
	BaseURL string
	User    string
	Pass    string
	httpc   *http.Client
}

// New builds a Client. An empty baseURL falls back to DefaultBaseURL.
func New(baseURL, user, pass string) *Client {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		User:    user,
		Pass:    pass,
		httpc:   &http.Client{Timeout: requestTimeout},
	}
}

// DomainCount is one entry of the top-blocked-domains list.
type DomainCount struct {
	Domain string `json:"domain"`
	Count  int    `json:"count"`
}

// Status is the combined snapshot the dashboard renders: protection state plus
// the 24h stats. A failing sub-call leaves its fields zero and adds an entry to
// Errors instead of failing the whole status.
type Status struct {
	ProtectionEnabled bool              `json:"protection_enabled"`
	Running           bool              `json:"running"`
	Version           string            `json:"version"`
	NumQueries        int64             `json:"num_queries"`
	NumBlocked        int64             `json:"num_blocked"`
	BlockedPct        float64           `json:"blocked_pct"`
	AvgProcessingMs   float64           `json:"avg_processing_ms"`
	TopBlocked        []DomainCount     `json:"top_blocked"`
	Errors            map[string]string `json:"errors,omitempty"`
}

// do performs one authenticated request. A nil body sends no payload; any other
// value is JSON-encoded.
func (c *Client) do(ctx context.Context, method, path string, body any) (int, []byte, error) {
	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return 0, nil, fmt.Errorf("adguard: marshal body: %w", err)
		}
		reader = bytes.NewReader(buf)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, reader)
	if err != nil {
		return 0, nil, fmt.Errorf("adguard: build request: %w", err)
	}
	auth := base64.StdEncoding.EncodeToString([]byte(c.User + ":" + c.Pass))
	req.Header.Set("Authorization", "Basic "+auth)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpc.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("adguard: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return 0, nil, fmt.Errorf("adguard: read response: %w", err)
	}
	return resp.StatusCode, raw, nil
}

// Status fans /control/status and /control/stats into one object. A failing
// sub-call is recorded in Errors and leaves that section's fields zero.
func (c *Client) Status(ctx context.Context) (*Status, error) {
	out := &Status{}
	errs := map[string]string{}

	// /control/status — protection flag, running, version.
	if code, raw, err := c.do(ctx, http.MethodGet, "/control/status", nil); err != nil {
		errs["status"] = err.Error()
	} else if code != http.StatusOK {
		errs["status"] = fmt.Sprintf("HTTP %d", code)
	} else {
		var st struct {
			ProtectionEnabled bool   `json:"protection_enabled"`
			Running           bool   `json:"running"`
			Version           string `json:"version"`
		}
		if json.Unmarshal(raw, &st) != nil {
			errs["status"] = "invalid response"
		} else {
			out.ProtectionEnabled = st.ProtectionEnabled
			out.Running = st.Running
			out.Version = st.Version
		}
	}

	// /control/stats — 24h counters + top blocked domains.
	if code, raw, err := c.do(ctx, http.MethodGet, "/control/stats", nil); err != nil {
		errs["stats"] = err.Error()
	} else if code != http.StatusOK {
		errs["stats"] = fmt.Sprintf("HTTP %d", code)
	} else {
		var stx struct {
			NumDNSQueries      int64            `json:"num_dns_queries"`
			NumBlockedFiltered int64            `json:"num_blocked_filtering"`
			AvgProcessingTime  float64          `json:"avg_processing_time"`
			TopBlocked         []map[string]int `json:"top_blocked_domains"`
		}
		if json.Unmarshal(raw, &stx) != nil {
			errs["stats"] = "invalid response"
		} else {
			out.NumQueries = stx.NumDNSQueries
			out.NumBlocked = stx.NumBlockedFiltered
			out.AvgProcessingMs = stx.AvgProcessingTime * 1000 // s → ms
			if stx.NumDNSQueries > 0 {
				out.BlockedPct = float64(stx.NumBlockedFiltered) / float64(stx.NumDNSQueries) * 100
			}
			for _, m := range stx.TopBlocked {
				for dom, cnt := range m {
					out.TopBlocked = append(out.TopBlocked, DomainCount{Domain: dom, Count: cnt})
				}
			}
		}
	}

	if len(errs) > 0 {
		out.Errors = errs
	}
	return out, nil
}

// SetProtection toggles filtering. durationMs > 0 pauses protection for that
// long then re-enables it automatically (only meaningful when enabled=false);
// 0 makes the change indefinite. Returns the upstream HTTP status.
func (c *Client) SetProtection(ctx context.Context, enabled bool, durationMs int) (int, error) {
	body := map[string]any{"enabled": enabled}
	if !enabled && durationMs > 0 {
		body["duration"] = durationMs
	}
	code, raw, err := c.do(ctx, http.MethodPost, "/control/protection", body)
	if err != nil {
		return 0, err
	}
	if code != http.StatusOK {
		return code, fmt.Errorf("adguard: /control/protection HTTP %d: %s", code, strings.TrimSpace(string(raw)))
	}
	return code, nil
}
