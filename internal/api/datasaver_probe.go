package api

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func (r *Router) probeDatasaverProxy(ctx context.Context, exit string) (string, error) {
	ep, err := r.singboxManager().ProxyEndpoint(exit)
	if err != nil {
		return "", err
	}
	proxyURL, err := url.Parse("http://" + ep)
	if err != nil {
		return "", fmt.Errorf("invalid proxy endpoint %q: %w", ep, err)
	}
	tr := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
	defer tr.CloseIdleConnections()
	cli := &http.Client{Timeout: 12 * time.Second, Transport: tr}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://api.ipify.org", nil)
	if err != nil {
		return "", err
	}
	resp, err := cli.Do(req)
	if err != nil {
		return "", fmt.Errorf("proxy %s unreachable: %w", ep, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("proxy %s answered HTTP %d", ep, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 64))
	if err != nil {
		return "", fmt.Errorf("proxy %s: read failed: %w", ep, err)
	}
	ip := strings.TrimSpace(string(b))
	if net.ParseIP(ip) == nil {
		return "", fmt.Errorf("proxy %s did not return a valid IP (%q)", ep, ip)
	}
	return ip, nil
}
