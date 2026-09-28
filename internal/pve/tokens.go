package pve

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

type TokenInfo struct {
	TokenID string `json:"tokenid"`
	Privsep bool   `json:"-"`
	Expire  int64  `json:"expire"`
	Comment string `json:"comment"`
}

func (t *TokenInfo) UnmarshalJSON(raw []byte) error {
	type rawValue TokenInfo
	var aux struct {
		rawValue
		Privsep json.RawMessage `json:"privsep"`
	}
	if err := json.Unmarshal(raw, &aux); err != nil {
		return err
	}
	*t = TokenInfo(aux.rawValue)
	t.Privsep = truthyNumberOrString(aux.Privsep)
	return nil
}

func truthyNumberOrString(raw json.RawMessage) bool {
	s := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	return s == "1" || s == "true"
}

func (t TokenInfo) ExpiresInDays(now int64) (days int, expires bool) {
	if t.Expire == 0 {
		return 0, false
	}
	d := t.Expire - now
	if d < 0 {
		return int((d - 86399) / 86400), true
	}
	return int(d / 86400), true
}

func (c *Client) ListTokens(ctx context.Context, user string) ([]TokenInfo, error) {
	if err := validUserID(user); err != nil {
		return nil, err
	}
	var toks []TokenInfo
	p := "/api2/json/access/users/" + url.PathEscape(user) + "/token"
	if err := c.do(ctx, http.MethodGet, p, &toks); err != nil {
		return nil, err
	}
	return toks, nil
}

func (c *Client) TokenInfo(ctx context.Context, user, tokenID string) (TokenInfo, error) {
	p, err := tokenPath(user, tokenID)
	if err != nil {
		return TokenInfo{}, err
	}
	var info TokenInfo
	if err := c.do(ctx, http.MethodGet, p, &info); err != nil {
		return TokenInfo{}, err
	}
	info.TokenID = tokenID
	return info, nil
}

func (c *Client) DeleteToken(ctx context.Context, user, tokenID string) error {
	p, err := tokenPath(user, tokenID)
	if err != nil {
		return err
	}
	return c.do(ctx, http.MethodDelete, p, nil)
}

func tokenPath(user, tokenID string) (string, error) {
	if err := validUserID(user); err != nil {
		return "", err
	}
	if tokenID == "" || strings.ContainsAny(tokenID, "/?#") || strings.Contains(tokenID, "..") {
		return "", fmt.Errorf("pve: invalid token id (%q)", tokenID)
	}
	return "/api2/json/access/users/" + url.PathEscape(user) + "/token/" + url.PathEscape(tokenID), nil
}

func validUserID(user string) error {
	at := strings.Index(user, "@")
	if at <= 0 || at == len(user)-1 || strings.ContainsAny(user, "/?#") || strings.Contains(user, "..") {
		return fmt.Errorf("pve: invalid userid (%q, expected USER@REALM)", user)
	}
	return nil
}
