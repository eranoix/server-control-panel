// avatar.go — the WhatsApp avatar proxy (HandleAvatar/handleAvatar).
//
// A proxy for WAHA's URL (which points at WhatsApp's CDN). In-memory cache:
// positive (1h, since the URL changes when a contact swaps their photo) and
// negative (10min, to stop us hitting WAHA again for contacts with no avatar).
//
// Without the cache, opening the WhatsApp panel fired ~200 concurrent
// requests and WAHA answered 429 across the board.
//
// HandleAvatar (capital H) is the entry point Manager.AvatarHandler uses in
// the v2 multi-tenant layout — each Service has its own cache and WAHA client.
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

// avatarFetchClient fetches the pictures from WhatsApp's CDN server-side (a
// same-origin proxy) instead of redirecting the browser to pps.whatsapp.net.
// Hardened against SSRF: it does NOT follow redirects (each hop could escape
// the allowlist) and its DialContext refuses private, loopback and link-local
// IPs (a defence against DNS rebinding — the check is against the connection's
// REAL IP).
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

// avatarHostAllowed requires https plus a host inside WhatsApp's CDN domain.
// The URL comes from WhatsApp's API (GetProfilePicture) or from the store, not
// straight from the user, but the allowlist makes sure a poisoned value cannot
// turn into SSRF against an arbitrary host.
func avatarHostAllowed(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" {
		return false
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	return host == "whatsapp.net" || strings.HasSuffix(host, ".whatsapp.net")
}

// avatarSafeDial resolves the host and refuses to connect to any
// non-publicly-routable IP (loopback, private, link-local, unspecified,
// multicast). It connects to the already-validated IP, closing the TOCTOU
// window (DNS rebinding).
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

// tryServeAvatar fetches the image from the CDN and serves it SAME-ORIGIN. We
// used to redirect (302) to pps.whatsapp.net, but when the CDN URL expires it
// returns an HTML error page that the browser BLOCKS under CORB (Cross-Origin
// Read Blocking) while spamming the console. Proxied, the content is
// same-origin (no CORB).
//
// Returns true when it SERVED an image (a 200 was written). Returns false
// WITHOUT writing anything to the response when the URL failed (expired,
// hotlink-blocked, wrong type) — so the caller can try a FRESH URL
// (self-healing) before answering 204. Crucially, every failure path happens
// BEFORE WriteHeader, so a false never leaves a half-written response.
func (s *Service) tryServeAvatar(w http.ResponseWriter, r *http.Request, url string) bool {
	if !avatarHostAllowed(url) {
		return false // anti-SSRF: only the WhatsApp CDN over https
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
		return false // URL expirada / hotlink-block → caller tenta URL fresca
	}
	// A safelist of inactive RASTER types. Crucially, it rejects image/svg+xml
	// — an SVG can carry a <script> and, served SAME-ORIGIN, would execute XSS
	// on our own domain. We force the Content-Type (rather than echoing
	// upstream's) plus nosniff and a CSP sandbox, so navigating to it directly
	// can never execute active content.
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
	_, _ = io.Copy(w, io.LimitReader(resp.Body, 8<<20)) // cap 8 MiB
	return true
}

// avatarNoPhoto: 204 means "contact has no photo" (a normal state, which the
// browser does not log as an error; the frontend falls back to initials).
func avatarNoPhoto(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.WriteHeader(http.StatusNoContent)
}

// handleAvatar serves a contact's profile picture. Path:
// /api/whatsapp/avatar/<jid> — a proxy for WAHA's URL (which points at
// WhatsApp's CDN). In memory: a positive cache (1h, since the URL changes
// when a contact swaps their photo) and a negative one (10min, to stop us
// hitting WAHA again for contacts with no avatar). Without the cache,
// opening the WhatsApp panel fired ~200 concurrent requests and WAHA
// answered 429 across the board (see the console log).
// HandleAvatar is the exported entrypoint used by Manager.AvatarHandler so
// avatars can be routed per-user (each Service has its own avatar cache and
// WAHA client). Internally just calls handleAvatar.
func (s *Service) HandleAvatar(w http.ResponseWriter, r *http.Request) {
	s.handleAvatar(w, r)
}

func (s *Service) handleAvatar(w http.ResponseWriter, r *http.Request) {
	jid := strings.TrimPrefix(r.URL.Path, "/api/whatsapp/avatar/")
	if jid == "" {
		http.NotFound(w, r)
		return
	}
	// ServeAvatar (service_export.go) carries the cache/proxy/fetch logic —
	// the same one the mobile BFF reuses, which already resolves the jid from
	// a path param.
	s.ServeAvatar(w, r, jid)
}
