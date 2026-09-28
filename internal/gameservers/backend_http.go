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

type BackendHTTP struct {
	base   *url.URL
	token  string
	no     string
	client *http.Client
}

const (
	maxResponse = 8 << 20

	operationTimeout = 60 * time.Second

	artifactTimeout = 15 * time.Minute
)

func NewBackendHTTP(base, token, no string) (*BackendHTTP, error) {
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
		client: &http.Client{Timeout: artifactTimeout},
	}, nil
}

func (b *BackendHTTP) Describe() string { return "http:" + b.no + " (" + b.base.Host + ")" }

func (b *BackendHTTP) Execute(ctx context.Context, op OpName, body json.RawMessage) (json.RawMessage, error) {
	if !knownOp(op) {
		return nil, &UnknownOperationError{Op: op}
	}
	if len(body) == 0 {
		body = json.RawMessage("{}")
	}

	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()

	target := *b.base
	target.Path = strings.TrimRight(target.Path, "/") + "/v1/op/" + url.PathEscape(string(op))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	b.authorize(req)

	res, err := b.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("node %q unreachable: %w", b.no, err)
	}
	defer res.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(res.Body, maxResponse+1))
	if err != nil {
		return nil, fmt.Errorf("reading the response from node %q: %w", b.no, err)
	}
	if len(raw) > maxResponse {
		return nil, fmt.Errorf("response from node %q above the %d byte limit", b.no, maxResponse)
	}

	switch {
	case res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden:
		return nil, &AuthorizationError{Msg: fmt.Sprintf("node %q rejected the credential (HTTP %d)", b.no, res.StatusCode)}
	case res.StatusCode == http.StatusNotFound:
		return nil, &UnknownOperationError{Op: op}
	case res.StatusCode != http.StatusOK:
		return nil, &OperationError{Msg: errorMessage(raw, res.StatusCode, b.no)}
	}
	return json.RawMessage(raw), nil
}

func (b *BackendHTTP) Open(ctx context.Context, h Handle) (io.ReadCloser, error) {
	if strings.TrimSpace(string(h)) == "" {
		return nil, ErrHandleInvalid
	}
	target := *b.base
	target.Path = strings.TrimRight(target.Path, "/") + "/v1/artifact/" + url.PathEscape(string(h))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, err
	}
	b.authorize(req)

	res, err := b.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("node %q unreachable: %w", b.no, err)
	}
	switch {
	case res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden:
		res.Body.Close()
		return nil, &AuthorizationError{Msg: fmt.Sprintf("node %q rejected the credential (HTTP %d)", b.no, res.StatusCode)}
	case res.StatusCode == http.StatusNotFound:
		res.Body.Close()
		return nil, ErrHandleInvalid
	case res.StatusCode != http.StatusOK:
		body, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		res.Body.Close()
		return nil, &OperationError{Msg: errorMessage(body, res.StatusCode, b.no)}
	}
	return res.Body, nil
}

func (b *BackendHTTP) Receive(ctx context.Context, r io.Reader) (Handle, error) {
	target := *b.base
	target.Path = strings.TrimRight(target.Path, "/") + "/v1/artifact"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), r)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	b.authorize(req)

	res, err := b.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("node %q unreachable: %w", b.no, err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))

	switch {
	case res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden:
		return "", &AuthorizationError{Msg: fmt.Sprintf("node %q rejected the credential (HTTP %d)", b.no, res.StatusCode)}
	case res.StatusCode != http.StatusOK:
		return "", &OperationError{Msg: errorMessage(body, res.StatusCode, b.no)}
	}
	var env struct {
		Handle string `json:"handle"`
	}
	if err := json.Unmarshal(body, &env); err != nil || env.Handle == "" {
		return "", fmt.Errorf("node %q returned no handle for the artifact sent", b.no)
	}
	return Handle(env.Handle), nil
}

func (b *BackendHTTP) authorize(r *http.Request) {
	r.Header.Set("Authorization", "Bearer "+b.token)
}

func knownOp(op OpName) bool {
	for _, o := range AllOps {
		if o == op {
			return true
		}
	}
	return false
}

func errorMessage(raw []byte, code int, no string) string {
	var env struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(raw, &env) == nil && env.Error != "" {
		return env.Error
	}
	return fmt.Sprintf("node %q answered HTTP %d", no, code)
}
