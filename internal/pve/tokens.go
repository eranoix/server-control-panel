package pve

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// TokenInfo describes ONE hypervisor API token.
//
// 🔴 Expire is a trap and it is not a detail: `verify_token` does `die "token
// access expired"` when `expire < time()`, and the client receives **401 —
// indistinguishable from revocation**. The tokens here were created with +180d.
// Without keeping the date, months from now the panel will say "no credential"
// and nobody will know whether it was revocation or the calendar. That is why
// the field leaves here, crosses the model and reaches the screen.
//
// Expire == 0 means "does not expire" — it is what the hypervisor returns when
// the token was created with no deadline.
type TokenInfo struct {
	TokenID string `json:"tokenid"`
	Privsep bool   `json:"-"` // the hypervisor sends a numeric 0/1; see UnmarshalJSON
	Expire  int64  `json:"expire"`
	Comment string `json:"comment"`
}

// UnmarshalJSON exists for one reason only: the hypervisor serialises booleans
// as a number (`"privsep":1`) and, depending on the route, as a string (`"1"`).
// Leaving the field as a plain bool would make the json fail in silence and
// turn every token into privsep=false — the opposite of what has to be proven
// here.
func (t *TokenInfo) UnmarshalJSON(raw []byte) error {
	type cru TokenInfo // alias with no methods, so as not to recurse
	var aux struct {
		cru
		Privsep json.RawMessage `json:"privsep"`
	}
	if err := json.Unmarshal(raw, &aux); err != nil {
		return err
	}
	*t = TokenInfo(aux.cru)
	t.Privsep = numeroOuStringVerdadeiro(aux.Privsep)
	return nil
}

func numeroOuStringVerdadeiro(raw json.RawMessage) bool {
	s := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	return s == "1" || s == "true"
}

// ExpiraEmDias returns how many days are left before the token expires,
// measured against the clock passed as an argument (unix seconds).
//
// The clock is a PARAMETER, not time.Now(): a criterion that is met by waiting
// is forbidden, and the only way to prove "it warns before expiry" without
// waiting months is to inject the instant. Negative = already expired. The
// second return is false when the token has no deadline at all.
func (t TokenInfo) ExpiraEmDias(agora int64) (dias int, vence bool) {
	if t.Expire == 0 {
		return 0, false
	}
	d := t.Expire - agora
	if d < 0 {
		// Integer division in Go truncates towards zero; -1 s would become 0 days and
		// an expired token would show up as "expires today".
		return int((d - 86399) / 86400), true
	}
	return int(d / 86400), true
}

// ListTokens returns the tokens of a hypervisor user.
func (c *Client) ListTokens(ctx context.Context, user string) ([]TokenInfo, error) {
	if err := userIDValido(user); err != nil {
		return nil, err
	}
	var toks []TokenInfo
	p := "/api2/json/access/users/" + url.PathEscape(user) + "/token"
	if err := c.do(ctx, http.MethodGet, p, &toks); err != nil {
		return nil, err
	}
	return toks, nil
}

// TokenInfo reads ONE token. The hypervisor does not repeat the tokenid inside
// the object (it is already in the path), so the field is filled in here — the
// caller gets the complete struct instead of having to patch it up.
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

// DeleteToken deletes the token on the hypervisor. It is the hypervisor half of
// revocation.
//
// 🔴 The ORDER of revocation belongs to the handler, not here, and it is: DELETE
// on the hypervisor → confirm 401 on a real call → only then delete from the
// vault. Inverted, a failure in the middle would leave the vault clean with the
// token ALIVE on the hypervisor — an orphan credential nobody can revoke any
// more because nobody knows it exists.
//
// This package does NOT touch the vault (TestPveNaoImportaSecrets pins that):
// putting the two halves together in a single place is what makes the order
// verifiable.
func (c *Client) DeleteToken(ctx context.Context, user, tokenID string) error {
	p, err := tokenPath(user, tokenID)
	if err != nil {
		return err
	}
	return c.do(ctx, http.MethodDelete, p, nil)
}

func tokenPath(user, tokenID string) (string, error) {
	if err := userIDValido(user); err != nil {
		return "", err
	}
	if tokenID == "" || strings.ContainsAny(tokenID, "/?#") || strings.Contains(tokenID, "..") {
		return "", fmt.Errorf("pve: invalid token id (%q)", tokenID)
	}
	return "/api2/json/access/users/" + url.PathEscape(user) + "/token/" + url.PathEscape(tokenID), nil
}

// userIDValido requires the USER@REALM form. Without the realm, the path points
// at another hypervisor resource — and the operation that matters most here is
// a DELETE.
func userIDValido(user string) error {
	at := strings.Index(user, "@")
	if at <= 0 || at == len(user)-1 || strings.ContainsAny(user, "/?#") || strings.Contains(user, "..") {
		return fmt.Errorf("pve: invalid userid (%q, expected USER@REALM)", user)
	}
	return nil
}
