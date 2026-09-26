package labagent

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

// backDuplo satisfies gameservers.Backend without touching any disk.
type backDuplo struct {
	chamadas []gameservers.OpName
	erro     error
	recebeu  int64
}

func (b *backDuplo) Executar(_ context.Context, op gameservers.OpName, _ json.RawMessage) (json.RawMessage, error) {
	b.chamadas = append(b.chamadas, op)
	if b.erro != nil {
		return nil, b.erro
	}
	return json.RawMessage(`{"ok":true}`), nil
}
func (b *backDuplo) Abrir(context.Context, gameservers.Handle) (io.ReadCloser, error) {
	return nil, nil
}
func (b *backDuplo) Receber(_ context.Context, r io.Reader) (gameservers.Handle, error) {
	// Consume the body: a double that does not read would let the upload test
	// pass without a single byte crossing over.
	n, err := io.Copy(io.Discard, r)
	if err != nil {
		return "", err
	}
	b.recebeu = n
	return gameservers.Handle("handle-de-teste"), nil
}
func (b *backDuplo) Descrever() string { return "duplo de teste" }

func servidorDeTeste(t *testing.T, token string) (*Servidor, *backDuplo) {
	t.Helper()
	b := &backDuplo{}
	ag := &Agent{No: "teste", Back: b}
	return NovoServidor(ag, SegredoDeTexto(token), NovasMetricas("teste")), b
}

func pede(t *testing.T, s *Servidor, metodo, alvo, bearer string, corpo string) *httptest.ResponseRecorder {
	t.Helper()
	var body *strings.Reader
	if corpo == "" {
		body = strings.NewReader("")
	} else {
		body = strings.NewReader(corpo)
	}
	r := httptest.NewRequest(metodo, alvo, body)
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

// TestAuthSemSegredoEhInerte — an agent with no secret accepts NOTHING.
//
// The half that matters is the second one: even with a syntactically perfect
// Authorization (another node's bearer, say), the answer is 401. "Inert" means
// there is no request it accepts — not that it accepts any request at all. An
// agent that came up with no secret and stayed OPEN would be the worst failure
// possible in this work, and it would be a silent one.
func TestAuthSemSegredoEhInerte(t *testing.T) {
	s, back := servidorDeTeste(t, "")
	for _, bearer := range []string{"", "qualquer-coisa", "o-bearer-de-outro-no"} {
		w := pede(t, s, http.MethodPost, "/v1/op/server.status", bearer, `{}`)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("bearer %q: expected 401, got %d — an agent with no secret canNOT accept anything", bearer, w.Code)
		}
	}
	if len(back.chamadas) != 0 {
		t.Errorf("the back end was called %d time(s) with no secret provisioned: %v", len(back.chamadas), back.chamadas)
	}
}

func TestAuthTokenErrado(t *testing.T) {
	s, back := servidorDeTeste(t, "o-certo")
	if w := pede(t, s, http.MethodPost, "/v1/op/server.status", "o-errado", `{}`); w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
	if len(back.chamadas) != 0 {
		t.Errorf("back end reached with the wrong token: %v", back.chamadas)
	}
}

func TestAuthTokenCerto(t *testing.T) {
	s, back := servidorDeTeste(t, "o-certo")
	w := pede(t, s, http.MethodPost, "/v1/op/server.status", "o-certo", `{}`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d — body: %s", w.Code, w.Body.String())
	}
	if len(back.chamadas) != 1 || back.chamadas[0] != gameservers.OpServerStatus {
		t.Errorf("the handler did not reach the back end with the right operation: %v", back.chamadas)
	}
}

// TestAuthNaoAceitaQueryString — the fanhub regression that must not happen.
//
// `fanhub.py:388` accepts `?t=`; a token in the query string leaks into the
// access log, into Referer and into the browser history. Here, with no header,
// it is 401 — no matter what comes in the URL.
func TestAuthNaoAceitaQueryString(t *testing.T) {
	s, back := servidorDeTeste(t, "o-certo")
	for _, alvo := range []string{
		"/v1/op/server.status?token=o-certo",
		"/v1/op/server.status?t=o-certo",
		"/v1/op/server.status?access_token=o-certo",
	} {
		if w := pede(t, s, http.MethodPost, alvo, "", `{}`); w.Code != http.StatusUnauthorized {
			t.Errorf("%s: expected 401, got %d — a token in the query string is being accepted", alvo, w.Code)
		}
	}
	if len(back.chamadas) != 0 {
		t.Errorf("back end reached through the query string: %v", back.chamadas)
	}
}

// TestAuthComparaHashDeTamanhoFixo asserts the SHAPE in the AST.
//
// Timing cannot be measured stably in a unit test — a test that tried to put a
// stopwatch on it would be flaky and would get turned off. What can be asserted
// stably is the structure: there is a `sha256.Sum256` on BOTH sides before a
// single `subtle.ConstantTimeCompare`, and there is no `==` comparison of a
// credential string.
//
// Why hash first: ConstantTimeCompare returns early when the lengths differ,
// which leaks the SIZE of the expected token.
func TestAuthComparaHashDeTamanhoFixo(t *testing.T) {
	arquivo := filepath.Join(raizDoRepo(t), "internal", "labagent", "auth.go")
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, arquivo, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var temSum, temCTC bool
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
			temSum = true
		case pkg.Name == "subtle" && sel.Sel.Name == "ConstantTimeCompare":
			temCTC = true
		}
		return true
	})
	if !temSum {
		t.Error("auth.go does not use sha256.Sum256 — without a fixed-length hash, ConstantTimeCompare leaks the token length")
	}
	if !temCTC {
		t.Error("auth.go does not use subtle.ConstantTimeCompare")
	}

	// The matching behaviour: tokens of VERY different lengths take the same
	// path and both are refused.
	s := SegredoDeTexto("token-de-tamanho-medio")
	for _, tentativa := range []string{"x", strings.Repeat("y", 4000)} {
		if s.confere(tentativa) {
			t.Errorf("accepted a wrong %d-byte credential", len(tentativa))
		}
	}
	if !s.confere("token-de-tamanho-medio") {
		t.Error("refused the correct credential")
	}
}

// TestSegredoAusenteNaoConfere — a zero Segredo refuses even the empty string.
func TestSegredoAusenteNaoConfere(t *testing.T) {
	var s Segredo
	if s.Presente() {
		t.Error("a zero Segredo claims to be present")
	}
	for _, tentativa := range []string{"", "qualquer"} {
		if s.confere(tentativa) {
			t.Errorf("a missing Segredo matched %q", tentativa)
		}
	}
}
