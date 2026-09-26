package nodeagent

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"server-control-panel/internal/gameservers"
)

// backendDouble satisfies gameservers.Backend without touching any disk.
type backendDouble struct {
	calls    []gameservers.OpName
	failure  error
	received int64
}

func (b *backendDouble) Execute(_ context.Context, op gameservers.OpName, _ json.RawMessage) (json.RawMessage, error) {
	b.calls = append(b.calls, op)
	if b.failure != nil {
		return nil, b.failure
	}
	return json.RawMessage(`{"ok":true}`), nil
}
func (b *backendDouble) Open(context.Context, gameservers.Handle) (io.ReadCloser, error) {
	return nil, nil
}
func (b *backendDouble) Receive(_ context.Context, r io.Reader) (gameservers.Handle, error) {
	// Consume the body: a double that does not read would let the upload test
	// pass without a single byte crossing over.
	n, err := io.Copy(io.Discard, r)
	if err != nil {
		return "", err
	}
	b.received = n
	return gameservers.Handle("test-handle"), nil
}
func (b *backendDouble) Describe() string { return "test double" }

func testServer(t *testing.T, token string) (*Server, *backendDouble) {
	t.Helper()
	b := &backendDouble{}
	ag := &Agent{No: "test", Back: b}
	return NewServer(ag, SecretFromText(token), NewMetrics("test")), b
}

func request(t *testing.T, s *Server, method, target, bearer string, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	r := httptest.NewRequest(method, target, reader)
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

// TestAuthWithoutSecretIsInert — an agent with no secret accepts NOTHING.
//
// The half that matters is the second one: even with a syntactically perfect
// Authorization (another node's bearer, say), the answer is 401. "Inert" means
// there is no request it accepts — not that it accepts any request at all. An
// agent that came up with no secret and stayed OPEN would be the worst failure
// possible in this work, and it would be a silent one.
func TestAuthWithoutSecretIsInert(t *testing.T) {
	s, back := testServer(t, "")
	for _, bearer := range []string{"", "anything", "another-nodes-bearer"} {
		w := request(t, s, http.MethodPost, "/v1/op/server.status", bearer, `{}`)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("bearer %q: expected 401, got %d — an agent with no secret canNOT accept anything", bearer, w.Code)
		}
	}
	if len(back.calls) != 0 {
		t.Errorf("the back end was called %d time(s) with no secret provisioned: %v", len(back.calls), back.calls)
	}
}

func TestAuthWrongToken(t *testing.T) {
	s, back := testServer(t, "the-right-one")
	if w := request(t, s, http.MethodPost, "/v1/op/server.status", "the-wrong-one", `{}`); w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
	if len(back.calls) != 0 {
		t.Errorf("back end reached with the wrong token: %v", back.calls)
	}
}

func TestAuthRightToken(t *testing.T) {
	s, back := testServer(t, "the-right-one")
	w := request(t, s, http.MethodPost, "/v1/op/server.status", "the-right-one", `{}`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d — body: %s", w.Code, w.Body.String())
	}
	if len(back.calls) != 1 || back.calls[0] != gameservers.OpServerStatus {
		t.Errorf("the handler did not reach the back end with the right operation: %v", back.calls)
	}
}

// TestAuthRejectsQueryString — the fanhub regression that must not happen.
//
// `fanhub.py:388` accepts `?t=`; a token in the query string leaks into the
// access log, into Referer and into the browser history. Here, with no header,
// it is 401 — no matter what comes in the URL.
func TestAuthRejectsQueryString(t *testing.T) {
	s, back := testServer(t, "the-right-one")
	for _, target := range []string{
		"/v1/op/server.status?token=the-right-one",
		"/v1/op/server.status?t=the-right-one",
		"/v1/op/server.status?access_token=the-right-one",
	} {
		if w := request(t, s, http.MethodPost, target, "", `{}`); w.Code != http.StatusUnauthorized {
			t.Errorf("%s: expected 401, got %d — a token in the query string is being accepted", target, w.Code)
		}
	}
	if len(back.calls) != 0 {
		t.Errorf("back end reached through the query string: %v", back.calls)
	}
}

// TestAuthComparesFixedSizeHash asserts the SHAPE in the AST.
//
// Timing cannot be measured stably in a unit test — a test that tried to put a
// stopwatch on it would be flaky and would get turned off. What can be asserted
// stably is the structure: there is a `sha256.Sum256` on BOTH sides before a
// single `subtle.ConstantTimeCompare`, and there is no `==` comparison of a
// credential string.
//
// Why hash first: ConstantTimeCompare returns early when the lengths differ,
// which leaks the SIZE of the expected token.
func TestAuthComparesFixedSizeHash(t *testing.T) {
	file := filepath.Join(repoRoot(t), "internal", "nodeagent", "auth.go")
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var hasSum, hasCTC bool
	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		switch {
		case pkg.Name == "sha256" && sel.Sel.Name == "Sum256":
			hasSum = true
		case pkg.Name == "subtle" && sel.Sel.Name == "ConstantTimeCompare":
			hasCTC = true
		}
		return true
	})
	if !hasSum {
		t.Error("auth.go does not use sha256.Sum256 — without a fixed-length hash, ConstantTimeCompare leaks the token length")
	}
	if !hasCTC {
		t.Error("auth.go does not use subtle.ConstantTimeCompare")
	}

	// The matching behaviour: tokens of VERY different lengths take the same
	// path and both are refused.
	s := SecretFromText("medium-length-token")
	for _, attempt := range []string{"x", strings.Repeat("y", 4000)} {
		if s.matches(attempt) {
			t.Errorf("accepted a wrong %d-byte credential", len(attempt))
		}
	}
	if !s.matches("medium-length-token") {
		t.Error("refused the correct credential")
	}
}

// TestMissingSecretNeverMatches: a zero Secret refuses even the empty string.
func TestMissingSecretNeverMatches(t *testing.T) {
	var s Secret
	if s.Present() {
		t.Error("a zero Secret claims to be present")
	}
	for _, attempt := range []string{"", "anything"} {
		if s.matches(attempt) {
			t.Errorf("a missing Secret matched %q", attempt)
		}
	}
}
