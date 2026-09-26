package api

// datasaver_probe.go — the data-saver's safety net.
//
// The tunnel blackout was caused by routing a device's web traffic to a proxy
// that sing-box could not reach (NXDOMAIN): Hiddify connected, but nothing
// opened. The lesson: TURNING ON data saving for a device may only happen if
// that exit's proxy is PROVEN to be working — otherwise the user's connection
// drops. This probe makes a real HTTP request THROUGH the proxy out to the
// internet and confirms 200 + a valid exit IP. Used for (1) the health gate of
// the per-device toggle and (2) the watchdog that auto-reverts if the proxy
// dies later.

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

// probeDatasaverProxy verifies that the compression proxy of the exit (vps|casa)
// really reaches the internet. Returns the exit IP as seen by the proxy.
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
