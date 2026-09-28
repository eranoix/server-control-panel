package whatsapp

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

var avatarFetchClient = &http.Client{
	Timeout: 15 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
	Transport: &http.Transport{
		DialContext:           avatarSafeDial,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
	},
}

func avatarHostAllowed(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" {
		return false
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	return host == "whatsapp.net" || strings.HasSuffix(host, ".whatsapp.net")
}

func avatarSafeDial(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	d := &net.Dialer{Timeout: 10 * time.Second}
	var lastErr error = fmt.Errorf("avatar: no allowed IP for %s", host)
	for _, ip := range ips {
		if avatarIPBlocked(ip) {
			lastErr = fmt.Errorf("avatar: IP not allowed (%s)", ip)
			continue
		}
		if conn, derr := d.DialContext(ctx, network, net.JoinHostPort(ip.String(), port)); derr == nil {
			return conn, nil
		} else {
			lastErr = derr
		}
	}
	return nil, lastErr
}

func avatarIPBlocked(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast()
}

func (s *Service) tryServeAvatar(w http.ResponseWriter, r *http.Request, url string) bool {
	if !avatarHostAllowed(url) {
		return false
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	resp, err := avatarFetchClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	ct := strings.ToLower(strings.TrimSpace(strings.SplitN(resp.Header.Get("Content-Type"), ";", 2)[0]))
	var safeCT string
	switch ct {
	case "image/jpeg", "image/png", "image/webp", "image/gif":
		safeCT = ct
	default:
		return false
	}
	w.Header().Set("Content-Type", safeCT)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, io.LimitReader(resp.Body, 8<<20))
	return true
}

func avatarNoPhoto(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) HandleAvatar(w http.ResponseWriter, r *http.Request) {
	s.handleAvatar(w, r)
}

func (s *Service) handleAvatar(w http.ResponseWriter, r *http.Request) {
	jid := strings.TrimPrefix(r.URL.Path, "/api/whatsapp/avatar/")
	if jid == "" {
		http.NotFound(w, r)
		return
	}
	s.ServeAvatar(w, r, jid)
}
