package videocall

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"strings"
	"time"
)

// TURNConfig is what the videocall.Service needs to mint coturn-compatible
// time-limited credentials. The same `Secret` must be configured in
// /etc/coturn/turnserver.conf under `static-auth-secret` (or
// `use-auth-secret` keyword); vpsmctl videocall init writes both sides from
// the same source.
//
// Hosts is the list of TURN/STUN endpoints to advertise. Typically:
//
//	["turn:tunnel.example.com:3478?transport=udp",
//	 "turn:tunnel.example.com:3478?transport=tcp",
//	 "stun:stun.l.google.com:19302"]
//
// The Google STUN entry is gratuitous public infra; coturn is the actual
// fallback. Empty TURNConfig means TURN disabled — calls work P2P-only and
// fail behind symmetric NAT. Used in dev / single-network demos.
type TURNConfig struct {
	Secret string
	Hosts  []string
}

// MintTURNCredentials produces a coturn REST-API style time-limited credential.
// Spec (coturn man page, section "TURN REST API"):
//
//	username  = <expiration_unix_ts>:<user>
//	password  = base64(hmac_sha1(secret, username))
//
// `user` is the application username — coturn doesn't validate it, but it
// makes audit logs grep-friendly. TTL clamps to [60s, 24h] to bound abuse.
func (c *TURNConfig) MintTURNCredentials(user string, ttl time.Duration) TURNCredentials {
	if c == nil || c.Secret == "" {
		// TURN disabled — return STUN-only creds. Most calls still work.
		return TURNCredentials{
			URLs: stunOnly(c),
			TTL:  0,
		}
	}
	if ttl < 60*time.Second {
		ttl = 60 * time.Second
	}
	if ttl > 24*time.Hour {
		ttl = 24 * time.Hour
	}
	exp := time.Now().Add(ttl).Unix()
	username := fmt.Sprintf("%d:%s", exp, sanitizeUser(user))
	mac := hmac.New(sha1.New, []byte(c.Secret))
	mac.Write([]byte(username))
	credential := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	return TURNCredentials{
		URLs:       append([]string(nil), c.Hosts...),
		Username:   username,
		Credential: credential,
		TTL:        int64(ttl.Seconds()),
	}
}

func stunOnly(c *TURNConfig) []string {
	if c == nil {
		return []string{"stun:stun.l.google.com:19302"}
	}
	out := make([]string, 0, len(c.Hosts))
	for _, h := range c.Hosts {
		if strings.HasPrefix(h, "stun:") {
			out = append(out, h)
		}
	}
	if len(out) == 0 {
		out = append(out, "stun:stun.l.google.com:19302")
	}
	return out
}

// sanitizeUser strips characters that would break coturn's username parsing
// (it splits on ':' to get the expiration timestamp).
func sanitizeUser(u string) string {
	if u == "" {
		return "anon"
	}
	out := make([]byte, 0, len(u))
	for i := 0; i < len(u); i++ {
		c := u[i]
		if c == ':' || c == ' ' || c == '\t' || c == '\n' {
			out = append(out, '_')
		} else {
			out = append(out, c)
		}
	}
	return string(out)
}
