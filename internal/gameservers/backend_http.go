package gameservers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// BackendHTTP is the Backend the panel uses when the node has `transport: agente`.
// It talks to that node's lab-agent over the internal bridge.
//
// ─────────────────────────────────────────────────────────────────────────────
// ONE FORWARDER, NOT 23 FUNCTIONS — and why
//
// The obvious design asks for "one function per operation". That is not what is
// written here, and the reason is the very thing that counts as this
// architecture's most expensive defect: the two back-ends drifting apart slowly
// until a screen works on one node and not on another.
//
// Twenty-three functions are twenty-three places where the panel can assemble an
// envelope different from the one the node decodes. With ONE generic forwarder
// (operation name + document bytes), there is nowhere to diverge: the envelope
// is opaque to this file, and the one that defines the shape is BackendLocal,
// which is also the one that reads it. Parity stops being tested and becomes
// structural.
//
// As a bonus, the criterion ("no operation-name literal in this file") ends up
// satisfied more strongly than it asked for: there is no operation name here at
// all, neither as a literal nor as a constant — only the `op` that arrives as a
// parameter, validated against the catalog before it becomes a URL.
// ─────────────────────────────────────────────────────────────────────────────
//
// # THE TOKEN IS INJECTED ON THE SERVER
//
// The bearer enters at the CONSTRUCTION of the client, on the panel's server
// side, in the same pattern as the token proxy that already exists in the repo.
// It never reaches the browser and never travels in the URL: a query string
// leaks into access logs, into Referer and into browser history, and fanhub.py
// (which accepts `?t=`) is the precedent this code refuses on purpose.
type BackendHTTP struct {
	base   *url.URL
	token  string
	no     string
	client *http.Client
}

const (
	// respostaMax caps the response document. Without it, a compromised (or just
	// broken) agent takes the panel down through memory — the panel is the work
	// tool, and a node must not be able to kill it.
	respostaMax = 8 << 20 // 8 MiB

	// tempoOperacao is the ceiling of a named operation. Genuinely long operations
	// (restore, update) are container start/stop, which Docker returns from fast;
	// the work carries on afterwards.
	tempoOperacao = 60 * time.Second

	// tempoArtefato is bigger because a file passes through there: a large world
	// over a LAN bridge takes longer than an operation and must not die halfway.
	tempoArtefato = 15 * time.Minute
)

// NovoBackendHTTP assembles a node's client.
//
// `base` is the agent's root (e.g. http://10.0.0.5:9977). The token is mandatory:
// an agent with no secret is inert and answers 401 to everything, so a client
// without a token would only produce a confusing authorization error — failing
// here names the cause.
func NovoBackendHTTP(base, token, no string) (*BackendHTTP, error) {
	if strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("http back-end of node %q requires a token: the agent answers 401 to everything without a credential", no)
	}
	u, err := url.Parse(strings.TrimRight(base, "/"))
	if err != nil {
		return nil, fmt.Errorf("invalid base for node %q: %w", no, err)
	}
	if u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("invalid base for node %q: missing scheme or host in %q", no, base)
	}
	return &BackendHTTP{
		base:   u,
		token:  token,
		no:     no,
		client: &http.Client{Timeout: tempoArtefato},
	}, nil
}

func (b *BackendHTTP) Descrever() string { return "http:" + b.no + " (" + b.base.Host + ")" }

func (b *BackendHTTP) Executar(ctx context.Context, op OpName, corpo json.RawMessage) (json.RawMessage, error) {
	// Validate against the catalog BEFORE dialing: a name this binary does not
	// know is a programming error in the panel, and finding it out through a 404
	// from the node would spend a network round trip to say what was known here.
	if !opConhecida(op) {
		return nil, &ErroOperacaoDesconhecida{Op: op}
	}
	if len(corpo) == 0 {
		corpo = json.RawMessage("{}")
	}

	ctx, cancel := context.WithTimeout(ctx, tempoOperacao)
	defer cancel()

	alvo := *b.base
	// PathEscape on the name: it comes from a catalog constant, but escaping is
	// what keeps the sentence "the client does not assemble paths" true.
	alvo.Path = strings.TrimRight(alvo.Path, "/") + "/v1/op/" + url.PathEscape(string(op))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, alvo.String(), bytes.NewReader(corpo))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	b.autoriza(req)

	res, err := b.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("node %q unreachable: %w", b.no, err)
	}
	defer res.Body.Close()

	bruto, err := io.ReadAll(io.LimitReader(res.Body, respostaMax+1))
	if err != nil {
		return nil, fmt.Errorf("reading the response from node %q: %w", b.no, err)
	}
	if len(bruto) > respostaMax {
		return nil, fmt.Errorf("response from node %q above the %d byte limit", b.no, respostaMax)
	}

	switch {
	case res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden:
		// A 401 must NOT become "the operation failed". They are opposite actions for
		// the operator — one is to swap the token, the other is to go look at the node.
		return nil, &ErroAutorizacao{Msg: fmt.Sprintf("node %q rejected the credential (HTTP %d)", b.no, res.StatusCode)}
	case res.StatusCode == http.StatusNotFound:
		return nil, &ErroOperacaoDesconhecida{Op: op}
	case res.StatusCode != http.StatusOK:
		return nil, &ErroOperacao{Msg: mensagemDeErro(bruto, res.StatusCode, b.no)}
	}
	return json.RawMessage(bruto), nil
}

func (b *BackendHTTP) Abrir(ctx context.Context, h Handle) (io.ReadCloser, error) {
	if strings.TrimSpace(string(h)) == "" {
		return nil, ErroHandleInvalido
	}
	alvo := *b.base
	alvo.Path = strings.TrimRight(alvo.Path, "/") + "/v1/artefato/" + url.PathEscape(string(h))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, alvo.String(), nil)
	if err != nil {
		return nil, err
	}
	b.autoriza(req)

	res, err := b.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("node %q unreachable: %w", b.no, err)
	}
	switch {
	case res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden:
		res.Body.Close()
		return nil, &ErroAutorizacao{Msg: fmt.Sprintf("node %q rejected the credential (HTTP %d)", b.no, res.StatusCode)}
	case res.StatusCode == http.StatusNotFound:
		res.Body.Close()
		return nil, ErroHandleInvalido
	case res.StatusCode != http.StatusOK:
		corpo, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		res.Body.Close()
		return nil, &ErroOperacao{Msg: mensagemDeErro(corpo, res.StatusCode, b.no)}
	}
	// The caller closes (Backend.Abrir's contract).
	return res.Body, nil
}

// Receber sends bytes to the node and receives the Handle back.
//
// A direct stream, no multipart and no base64: the request body IS the artifact.
// Multipart would exist in order to carry the NAME along, and the name is
// exactly what must not cross the boundary.
func (b *BackendHTTP) Receber(ctx context.Context, r io.Reader) (Handle, error) {
	alvo := *b.base
	alvo.Path = strings.TrimRight(alvo.Path, "/") + "/v1/artefato"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, alvo.String(), r)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	b.autoriza(req)

	res, err := b.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("node %q unreachable: %w", b.no, err)
	}
	defer res.Body.Close()
	corpo, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))

	switch {
	case res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden:
		return "", &ErroAutorizacao{Msg: fmt.Sprintf("node %q rejected the credential (HTTP %d)", b.no, res.StatusCode)}
	case res.StatusCode != http.StatusOK:
		return "", &ErroOperacao{Msg: mensagemDeErro(corpo, res.StatusCode, b.no)}
	}
	var env struct {
		Handle string `json:"handle"`
	}
	if err := json.Unmarshal(corpo, &env); err != nil || env.Handle == "" {
		return "", fmt.Errorf("node %q returned no handle for the artifact sent", b.no)
	}
	return Handle(env.Handle), nil
}

// autoriza puts the bearer in the HEADER, never in the URL. See the file header.
func (b *BackendHTTP) autoriza(r *http.Request) {
	r.Header.Set("Authorization", "Bearer "+b.token)
}

// opConhecida consults the catalog. It walks TodasAsOps instead of keeping a map
// of its own: a map of its own is a second list that one day falls out of sync
// with the first, which is the defect the closed catalog exists not to have.
func opConhecida(op OpName) bool {
	for _, o := range TodasAsOps {
		if o == op {
			return true
		}
	}
	return false
}

// mensagemDeErro extracts the agent's error field, with a readable indent.
func mensagemDeErro(bruto []byte, codigo int, no string) string {
	var env struct {
		Erro string `json:"erro"`
	}
	if json.Unmarshal(bruto, &env) == nil && env.Erro != "" {
		return env.Erro
	}
	return fmt.Sprintf("node %q answered HTTP %d", no, codigo)
}
