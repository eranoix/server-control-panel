// Package pve is a thin client for the Proxmox VE API (/api2/json), authenticated
// with an API token with privsep=1.
//
// Three things justify this package existing instead of a bare http.Client:
//
//  1. The error is TYPED into four distinct states — no credential (401), no
//     permission (403), unreachable (transport) and hypervisor error (>=400).
//     The screen needs to say WHICH of the four; merging two of them is the false green
//     that per-node revocation exists to forbid.
//  2. TLS is pinned to the PVE CA (see tls.go) — the hypervisor's certificate
//     does not cover any real address, and the easy way out (turning verification off)
//     is precisely the anti-pattern.
//  3. The secret never leaves here: it lives in the vault, enters through Config and only
//     shows up in the Authorization header. Neither Error, nor log, nor screen ever sees it.
//
// Every request goes through do() — a single constructor, as in
// internal/privateaiapi. No hypervisor operation is reimplemented:
// start/stop/snapshot are routes of PVE itself.
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
	// minTimeout is a FLOOR, not a default. The hypervisor delays EVERY 401
	// response by 3 s on purpose (PVE::APIServer::AnyEvent — "always delay
	// unauthorized calls by 3 seconds"; measured at 3.079 s). With a timeout below
	// that, a revoked token would come back as KindUnreachable instead of
	// KindNoCredential and the screen would lie exactly where the revocation rule
	// forbids it.
	minTimeout = 10 * time.Second

	// maxBodyBytes caps what gets read from the hypervisor. /cluster/resources on
	// this lab measures ~4 KB; 1 MiB is wide slack and a ceiling against a
	// pathological response.
	maxBodyBytes = 1 << 20
)

// Configuration errors — the caller takes a different path because of them, so
// they are sentinels, not text.
var (
	// ErrTokenInvalido marks a vault value that is NOT in the
	// "USER@REALM!NOME=SEGREDO" format. The secret was once stored bare and every
	// offline pin stayed green until the first live call took a 401.
	ErrTokenInvalido = errors.New("pve: token must be USER@REALM!NAME=SECRET")
	// ErrBaseURL marks a base_url that is missing or impossible to interpret.
	ErrBaseURL = errors.New("pve: invalid base_url")
	// ErrCAAusente marks https without a pinned CA. Falling back to the system
	// pool would be silent, and the hypervisor certificate is not public — see
	// tls.go.
	ErrCAAusente = errors.New("pve: https requires ca_file (pinned PVE CA)")
	// ErrEsquemaInseguro marks http:// for a host that is not loopback — it would
	// send the hypervisor token over the network in the clear.
	ErrEsquemaInseguro = errors.New("pve: http is only accepted on loopback (use https with a pinned CA)")
	// ErrRespostaGrande marks a body above maxBodyBytes. Without it the TRUNCATED
	// JSON reached json.Unmarshal and the error said "unexpected end of JSON
	// input" — a message that sends you looking for a defect in the parser when
	// the problem is the size of the response. The ceiling has to announce itself.
	ErrRespostaGrande = errors.New("pve: response above the 1 MiB cap — truncated, not parseable")
)

// Kind is the state a call to the hypervisor ended in. The five are disjoint by
// construction and the test TestErrorClassification proves it.
type Kind int

const (
	KindOK           Kind = iota // 2xx
	KindNoCredential             // 401 — token deleted, regenerated or expired → screen: "no credential"
	KindForbidden                // 403 — live token, insufficient ACL          → screen: "no permission"
	KindUnreachable              // dial/timeout/TLS — no response at all       → screen: "unreachable"
	KindHypervisor               // >=400 and up (includes 5xx)                 → screen: PVE text
)

// String returns a stable label, fit for JSON and for the log. Translating it
// into the sentence on screen is the front-end's job; here the value is a
// contract, not UI text.
func (k Kind) String() string {
	switch k {
	case KindOK:
		return "ok"
	case KindNoCredential:
		return "sem_credencial"
	case KindForbidden:
		return "sem_permissao"
	case KindUnreachable:
		return "inalcancavel"
	case KindHypervisor:
		return "erro_hipervisor"
	default:
		return fmt.Sprintf("kind(%d)", int(k))
	}
}

// Error is the typed error of every call. Body is the SERVER's body,
// truncated — the authentication header never goes in here
// (TestErrorNaoVazaSegredo pins that).
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
			// 🔴 Without this, Err disappears from the text whenever there was an HTTP
			// response — and that is exactly where it carries the cause the body does
			// not state: "response above the 1 MiB ceiling" (ErrRespostaGrande) showed
			// up as a chunk of JSON with no explanation, sending the operator looking
			// for a defect in the parser. It is the sibling of the defect that
			// detalheDoErroPVE fixes on the other side (Body vanishing when Status is
			// 0).
			msg += ": " + e.Err.Error()
		}
		return msg
	}
	return fmt.Sprintf("pve %s: %s: %v", e.Path, e.Kind, e.Err)
}

func (e *Error) Unwrap() error { return e.Err }

// Config describes ONE hypervisor. The fields come from the descriptor
// data/pve/pve.json (public) plus the secret from the vault — never from argv
// nor from env in plain text.
type Config struct {
	BaseURL    string        // https://hypervisor.local:8006
	Resolve    string        // 198.51.100.20 — the address to dial; SNI and verification follow ServerName
	ServerName string        // hypervisor.local — the cert's SAN does NOT cover .250 nor 100.x
	CAFile     string        // data/pve/pve-root-ca.pem
	TokenID    string        // lab@pve!audit, or the whole value from the vault "lab@pve!audit=<segredo>"
	Secret     string        // empty when TokenID already carries the whole value
	Timeout    time.Duration // 0 = floor of 10 s; a value below the floor is RAISED
}

// Client talks to one hypervisor, with one token.
type Client struct {
	baseURL string
	tokenID string
	secret  string
	httpc   *http.Client

	// tlsCfg and resolve are the SAME pin and the SAME address override that httpc
	// uses, kept here for the console WebSocket dialer (console.go) to reuse.
	// Building a second certificate-verification policy would mean two truths
	// about what a trusted hypervisor is — and the second one would age in
	// silence. tlsCfg is nil on the loopback http path.
	tlsCfg  *tls.Config
	resolve string

	// taskPoll is the interval between WaitTask queries (power.go). Zero in
	// production — WaitTask falls back to defaultTaskPoll of 1 s. It exists so the
	// test can shorten the wait without time.Sleep: a criterion that is met by
	// waiting on the clock is forbidden here.
	taskPoll time.Duration
}

// SplitTokenValue splits the value kept in the vault, in the
// "USER@REALM!NOME=SEGREDO" format. It is the format the PVEAPIToken header
// requires, and the format the vault started keeping after the bare-secret
// defect.
func SplitTokenValue(v string) (tokenID, secret string, err error) {
	v = strings.TrimSpace(v)
	i := strings.Index(v, "=")
	if i <= 0 || i == len(v)-1 {
		return "", "", fmt.Errorf("%w (no '=' separating id and secret)", ErrTokenInvalido)
	}
	tokenID, secret = v[:i], v[i+1:]
	if !validTokenID(tokenID) {
		return "", "", fmt.Errorf("%w (id %q has no USER@REALM!NAME)", ErrTokenInvalido, tokenID)
	}
	return tokenID, secret, nil
}

// validTokenID requires the two marks of a hypervisor token id: the realm (@)
// before the token name (!), in that order, with all three parts non-empty.
func validTokenID(id string) bool {
	bang := strings.Index(id, "!")
	at := strings.Index(id, "@")
	return at > 0 && bang > at+1 && bang < len(id)-1
}

// New validates the configuration and builds the client. It fails closed: a bad
// base_url, a token outside the vault format or https without a pinned CA
// return an error instead of a client that only breaks on the first live call.
func New(cfg Config) (*Client, error) {
	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("%w: %q", ErrBaseURL, cfg.BaseURL)
	}

	tokenID, secret := strings.TrimSpace(cfg.TokenID), strings.TrimSpace(cfg.Secret)
	if secret == "" {
		// Vault path: TokenID carries "<tokenid>=<secret>" in a single string.
		tokenID, secret, err = SplitTokenValue(tokenID)
		if err != nil {
			return nil, err
		}
	} else if !validTokenID(tokenID) {
		return nil, fmt.Errorf("%w (id %q)", ErrTokenInvalido, tokenID)
	}

	timeout := cfg.Timeout
	if timeout < minTimeout {
		timeout = minTimeout
	}

	var tr *http.Transport
	switch u.Scheme {
	case "https":
		if strings.TrimSpace(cfg.CAFile) == "" {
			return nil, ErrCAAusente
		}
		serverName := strings.TrimSpace(cfg.ServerName)
		if serverName == "" {
			serverName = u.Hostname()
		}
		if tr, err = newTransport(cfg.CAFile, serverName, cfg.Resolve); err != nil {
			return nil, err
		}
	default:
		// http only exists for the fake server in the test, on loopback. For a real
		// host it would be the hypervisor token travelling in the clear.
		if !loopback(u.Hostname()) {
			return nil, fmt.Errorf("%w: %s", ErrEsquemaInseguro, u.Host)
		}
		tr = plainTransport(cfg.Resolve)
	}

	return &Client{
		baseURL: base,
		tokenID: tokenID,
		secret:  secret,
		httpc:   &http.Client{Timeout: timeout, Transport: tr},
		tlsCfg:  tlsConfigDoCliente(tr),
		resolve: strings.TrimSpace(cfg.Resolve),
	}, nil
}

// loopback says whether the host is this machine itself — the only place where
// http:// does not expose the token on the network.
func loopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// do is the ONLY request constructor in the package. It builds the URL, sets
// the token header, reads the body with a ceiling and classifies the failure
// into the four states.
//
// out nil discards the body; out non-nil receives the contents of "data" — the
// hypervisor wraps every response in {"data": …}.
func (c *Client) do(ctx context.Context, method, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, nil)
	if err != nil {
		return &Error{Kind: KindUnreachable, Path: path, Err: err}
	}
	// Exact format required by the hypervisor, with no space after "PVEAPIToken=":
	//   PVEAPIToken=USER@REALM!NOME=SEGREDO
	// An API token does not need the session anti-forgery header
	// (HTTPServer.pm:122-129): that is only required of cookie sessions. Do not
	// invent any header beyond these two — the test asserts their absence.
	req.Header.Set("Authorization", "PVEAPIToken="+c.tokenID+"="+c.secret)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpc.Do(req)
	if err != nil {
		// No response: DNS, connection refused, timeout or TLS. This is
		// "unreachable", never "no credential" — that distinction is the point of the
		// package.
		return &Error{Kind: KindUnreachable, Path: path, Err: err}
	}
	defer resp.Body.Close()

	// +1 byte past the ceiling: it is what tells "fit exactly" apart from
	// "overflowed". Without the +1, a large response arrives here already
	// truncated and indistinguishable from a well-formed one.
	raw, rerr := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if rerr != nil {
		return &Error{Kind: KindUnreachable, Status: resp.StatusCode, Path: path, Err: rerr}
	}
	if len(raw) > maxBodyBytes {
		// The Body is CUT at 512 bytes: this error becomes text on screen, and 1 MiB
		// of JSON in the message is noise, not a clue.
		return &Error{Kind: KindHypervisor, Status: resp.StatusCode, Path: path,
			Body: string(raw[:512]), Err: ErrRespostaGrande}
	}

	// 401 and 403 stay in SEPARATE cases on purpose. internal/jira/client.go:145
	// merges the two into a single error; here that would erase the difference
	// between "the token was revoked" (401) and "the token is alive but does not
	// have that ACL" (403) — and the per-node revocation screen depends on exactly
	// that difference.
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
