package videocall

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"strings"
	"time"
)

type TURNConfig struct {
	Secret string
	Hosts  []string
}

func (c *TURNConfig) MintTURNCredentials(user string, ttl time.Duration) TURNCredentials {
	if c == nil || c.Secret == "" {
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
