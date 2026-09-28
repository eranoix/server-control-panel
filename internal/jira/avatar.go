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
	ctype := resp.Header.Get("Content-Type")
	if !isSafeImageType(ctype) {
		resp.Body.Close()
		return nil, "", fmt.Errorf("avatar: unsafe content-type %q", ctype)
	}
	return resp.Body, ctype, nil
}

func isSafeImageType(ct string) bool {
	ct = strings.ToLower(strings.TrimSpace(ct))
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	switch ct {
	case "image/png", "image/jpeg", "image/jpg", "image/gif", "image/webp", "image/avif":
		return true
	}
	return false
}
