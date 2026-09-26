// avatar.go — server-side proxy for Jira user avatars.
//
// Why: the avatarUrls Jira hands back point at THIRD-PARTY CDNs
// (secure.gravatar.com, i0.wp.com, *.atl-paas.net). Loading them straight in
// the browser trips Edge/Chrome's "Tracking Prevention blocked access to
// storage" (gravatar/wp.com are classified as trackers). By fetching the bytes
// on the server and serving them from OUR origin, the browser never touches
// those domains → the warning disappears. It also drops the CSP dependency on
// those hosts.
package jira

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// avatarHostAllowed restricts the hosts the proxy is willing to fetch from
// (SSRF defence). Covers the Jira site itself + the known avatar CDNs.
func avatarHostAllowed(host, siteHost string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if siteHost != "" && host == strings.ToLower(siteHost) {
		return true
	}
	if host == "secure.gravatar.com" || host == "gravatar.com" {
		return true
	}
	for _, suf := range []string{".wp.com", ".atl-paas.net", ".atlassian.net"} {
		if strings.HasSuffix(host, suf) {
			return true
		}
	}
	return false
}

// AvatarContent fetches an avatar by URL on the server. Allowlisted hosts only
// (the allowlist is re-applied on EVERY redirect — gravatar tends to redirect
// to the `?d=` fallback on atl-paas). Basic auth is attached only when the host
// is the Jira site itself (custom avatars hosted there); public CDNs go without.
// Returns the body (the caller closes it), the content-type, and a cap left
// implicit in the caller.
func (c *Client) AvatarContent(ctx context.Context, rawURL string) (io.ReadCloser, string, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return nil, "", fmt.Errorf("invalid avatar url")
	}
	siteHost := ""
	if su, e := url.Parse(c.site); e == nil {
		siteHost = su.Host
	}
	if !avatarHostAllowed(u.Host, siteHost) {
		return nil, "", fmt.Errorf("host not allowed: %s", u.Host)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Accept", "image/*")
	if strings.EqualFold(u.Host, siteHost) && siteHost != "" {
		req.Header.Set("Authorization", "Basic "+basicAuth(c.email+":"+c.token))
	}
	// Dedicated client: short timeout + the allowlist re-applied on every
	// redirect hop (it never touches the shared c.http).
	client := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("too many redirects")
			}
			if !avatarHostAllowed(r.URL.Host, siteHost) {
				return fmt.Errorf("redirect host not allowed: %s", r.URL.Host)
			}
			return nil
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode >= 300 {
		resp.Body.Close()
		return nil, "", fmt.Errorf("avatar: %d", resp.StatusCode)
	}
	// XSS defence (content-type smuggling): the avatar is served from OUR
	// origin, so an allowlisted upstream that returned text/html — or
	// image/svg+xml, which executes script — would become first-party XSS for
	// anyone navigating straight to /api/jira/avatar?u=. We accept raster images
	// only (SVG is refused on purpose). On failure → the caller answers 204 and
	// the front end falls back to initials.
	ctype := resp.Header.Get("Content-Type")
	if !isSafeImageType(ctype) {
		resp.Body.Close()
		return nil, "", fmt.Errorf("avatar: unsafe content-type %q", ctype)
	}
	return resp.Body, ctype, nil
}

// isSafeImageType accepts only raster image types the browser never treats as
// an active document. image/svg+xml is EXCLUDED on purpose (SVG runs script).
func isSafeImageType(ct string) bool {
	ct = strings.ToLower(strings.TrimSpace(ct))
	if i := strings.IndexByte(ct, ';'); i >= 0 { // drops "; charset=..."
		ct = strings.TrimSpace(ct[:i])
	}
	switch ct {
	case "image/png", "image/jpeg", "image/jpg", "image/gif", "image/webp", "image/avif":
		return true
	}
	return false
}
