package pve

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	minTimeout = 10 * time.Second

	maxBodyBytes = 1 << 20
)

var (
	ErrInvalidToken     = errors.New("pve: token must be USER@REALM!NAME=SECRET")
	ErrBaseURL          = errors.New("pve: invalid base_url")
	ErrCAMissing        = errors.New("pve: https requires ca_file (pinned PVE CA)")
	ErrInsecureScheme   = errors.New("pve: http is only accepted on loopback (use https with a pinned CA)")
	ErrResponseTooLarge = errors.New("pve: response above the 1 MiB cap — truncated, not parseable")
)

type Kind int

const (
	KindOK Kind = iota
	KindNoCredential
	KindForbidden
	KindUnreachable
	KindHypervisor
)

func (k Kind) String() string {
	switch k {
	case KindOK:
		return "ok"
	case KindNoCredential:
		return "no_credential"
	case KindForbidden:
		return "no_permission"
	case KindUnreachable:
		return "unreachable"
	case KindHypervisor:
		return "hypervisor_error"
	default:
		return fmt.Sprintf("kind(%d)", int(k))
	}
}

type Error struct {
	Kind   Kind
	Status int
	Path   string
	Body   string
	Err    error
}

func (e *Error) Error() string {
	if e.Status > 0 {
		msg := fmt.Sprintf("pve %s: %s (%d) %s", e.Path, e.Kind, e.Status, strings.TrimSpace(e.Body))
		if e.Err != nil {
			msg += ": " + e.Err.Error()
		}
		return msg
	}
	return fmt.Sprintf("pve %s: %s: %v", e.Path, e.Kind, e.Err)
}

func (e *Error) Unwrap() error { return e.Err }

type Config struct {
	BaseURL    string
	Resolve    string
	ServerName string
	CAFile     string
	TokenID    string
	Secret     string
	Timeout    time.Duration
}

type Client struct {
	baseURL string
	tokenID string
	secret  string
	httpc   *http.Client

	tlsCfg  *tls.Config
	resolve string

	taskPoll time.Duration
}

func SplitTokenValue(v string) (tokenID, secret string, err error) {
	v = strings.TrimSpace(v)
	i := strings.Index(v, "=")
	if i <= 0 || i == len(v)-1 {
		return "", "", fmt.Errorf("%w (no '=' separating id and secret)", ErrInvalidToken)
	}
	tokenID, secret = v[:i], v[i+1:]
	if !validTokenID(tokenID) {
		return "", "", fmt.Errorf("%w (id %q has no USER@REALM!NAME)", ErrInvalidToken, tokenID)
	}
	return tokenID, secret, nil
}

func validTokenID(id string) bool {
	bang := strings.Index(id, "!")
	at := strings.Index(id, "@")
	return at > 0 && bang > at+1 && bang < len(id)-1
}

func New(cfg Config) (*Client, error) {
	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("%w: %q", ErrBaseURL, cfg.BaseURL)
	}

	tokenID, secret := strings.TrimSpace(cfg.TokenID), strings.TrimSpace(cfg.Secret)
	if secret == "" {
		tokenID, secret, err = SplitTokenValue(tokenID)
		if err != nil {
			return nil, err
		}
	} else if !validTokenID(tokenID) {
		return nil, fmt.Errorf("%w (id %q)", ErrInvalidToken, tokenID)
	}

	timeout := cfg.Timeout
	if timeout < minTimeout {
		timeout = minTimeout
	}

	var tr *http.Transport
	switch u.Scheme {
	case "https":
		if strings.TrimSpace(cfg.CAFile) == "" {
			return nil, ErrCAMissing
		}
		serverName := strings.TrimSpace(cfg.ServerName)
		if serverName == "" {
			serverName = u.Hostname()
		}
		if tr, err = newTransport(cfg.CAFile, serverName, cfg.Resolve); err != nil {
			return nil, err
		}
	default:
		if !loopback(u.Hostname()) {
			return nil, fmt.Errorf("%w: %s", ErrInsecureScheme, u.Host)
		}
		tr = plainTransport(cfg.Resolve)
	}

	return &Client{
		baseURL: base,
		tokenID: tokenID,
		secret:  secret,
		httpc:   &http.Client{Timeout: timeout, Transport: tr},
		tlsCfg:  clientTLSConfig(tr),
		resolve: strings.TrimSpace(cfg.Resolve),
	}, nil
}

func loopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (c *Client) do(ctx context.Context, method, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, nil)
	if err != nil {
		return &Error{Kind: KindUnreachable, Path: path, Err: err}
	}
	req.Header.Set("Authorization", "PVEAPIToken="+c.tokenID+"="+c.secret)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpc.Do(req)
	if err != nil {
		return &Error{Kind: KindUnreachable, Path: path, Err: err}
	}
	defer resp.Body.Close()

	raw, rerr := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if rerr != nil {
		return &Error{Kind: KindUnreachable, Status: resp.StatusCode, Path: path, Err: rerr}
	}
	if len(raw) > maxBodyBytes {
		return &Error{Kind: KindHypervisor, Status: resp.StatusCode, Path: path,
			Body: string(raw[:512]), Err: ErrResponseTooLarge}
	}

	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return &Error{Kind: KindNoCredential, Status: resp.StatusCode, Path: path, Body: string(raw)}
	case resp.StatusCode == http.StatusForbidden:
		return &Error{Kind: KindForbidden, Status: resp.StatusCode, Path: path, Body: string(raw)}
	case resp.StatusCode >= 400:
		return &Error{Kind: KindHypervisor, Status: resp.StatusCode, Path: path, Body: string(raw)}
	}

	if out == nil {
		return nil
	}
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return &Error{Kind: KindHypervisor, Status: resp.StatusCode, Path: path, Body: string(raw), Err: err}
	}
	if len(env.Data) == 0 {
		return &Error{Kind: KindHypervisor, Status: resp.StatusCode, Path: path, Body: string(raw),
			Err: errors.New("response without a \"data\" envelope")}
	}
	if err := json.Unmarshal(env.Data, out); err != nil {
		return &Error{Kind: KindHypervisor, Status: resp.StatusCode, Path: path, Body: string(raw), Err: err}
	}
	return nil
}
