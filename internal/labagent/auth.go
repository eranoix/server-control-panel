package labagent

import (
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"net/http"
	"os"
	"strings"
)

// Agent authentication: a per-node bearer, header only.
//
// THE PRECEDENT THAT IS FOLLOWED: internal/api/handlers_trainer_token.go:44-55,
// the pattern already validated in this house — `expected == "" ||
// ConstantTimeCompare(...) != 1` returns 401.
//
// THE PRECEDENT THAT IS NOT FOLLOWED, and why: `fanhub.py` is the precedent for
// the BIND (and for that alone). Its auth has two defects that must not cross
// over to here:
//
//  1. it compares with Python's `==` — that is not constant time;
//  2. it accepts the token by QUERY STRING (`fanhub.py:388`, `?t=`), which leaks
//     into the access log, into Referer and into the browser history.
//
// Here the token comes in ONLY through the Authorization header. There is no
// read of `r.URL.Query()` in this file, and the acceptance criterion verifies
// that by grep — not even by accident.

// tamanhoMaxHeader caps the Authorization header before any work happens.
const tamanhoMaxHeader = 4096

// Segredo holds the hash of the bearer expected for this node.
//
// It is the HASH that is kept, not the token: the process does not need the
// plaintext in memory after boot, and a memory dump then gives up less.
type Segredo struct {
	hash     [sha256.Size]byte
	presente bool
}

// SegredoDeArquivo reads the bearer from a file (the same one systemd
// provisions with mode 0600). A missing or empty file yields an ABSENT Segredo
// — and an agent with no secret is INERT, never open.
func SegredoDeArquivo(caminho string) (Segredo, error) {
	b, err := os.ReadFile(caminho)
	if err != nil {
		if os.IsNotExist(err) {
			return Segredo{}, nil
		}
		return Segredo{}, fmt.Errorf("reading the bearer from %s: %w", caminho, err)
	}
	return SegredoDeTexto(strings.TrimSpace(string(b))), nil
}

// SegredoDeTexto builds the Segredo from the token in plaintext.
func SegredoDeTexto(token string) Segredo {
	if token == "" {
		return Segredo{}
	}
	return Segredo{hash: sha256.Sum256([]byte(token)), presente: true}
}

// Presente reports whether a secret has been provisioned.
func (s Segredo) Presente() bool { return s.presente }

// confere compares in constant time.
//
// WHY HASH BOTH SIDES, instead of ConstantTimeCompare straight on the token:
// ConstantTimeCompare returns early when the LENGTHS differ, which leaks the
// size of the expected token. Comparing fixed-size sha256 digests removes that
// early exit.
//
// Honest severity: LOW on a home-LAN bridge with a single operator. But it is
// one line, and the agent is precisely the piece this work exists to make
// auditable — paying a line here is cheaper than explaining it later.
func (s Segredo) confere(apresentado string) bool {
	if !s.presente {
		return false
	}
	dele := sha256.Sum256([]byte(apresentado))
	return subtle.ConstantTimeCompare(s.hash[:], dele[:]) == 1
}

// ExigeBearer is the authentication middleware.
//
// With no secret provisioned, EVERY protected route returns 401 — including one
// carrying a syntactically correct Authorization (which would be another node's
// bearer). An agent with no secret is inert, and "inert" means there is no
// request it accepts, not that it accepts any request at all.
func ExigeBearer(s Segredo, prox http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.presente {
			naoAutorizado(w, "agent has no provisioned secret")
			return
		}
		cab := r.Header.Get("Authorization")
		if len(cab) > tamanhoMaxHeader {
			naoAutorizado(w, "malformed credential")
			return
		}
		const prefixo = "Bearer "
		if !strings.HasPrefix(cab, prefixo) {
			naoAutorizado(w, "credential missing")
			return
		}
		if !s.confere(strings.TrimSpace(cab[len(prefixo):])) {
			naoAutorizado(w, "invalid credential")
			return
		}
		prox.ServeHTTP(w, r)
	})
}

// naoAutorizado answers 401 without distinguishing "missing" from "wrong" for
// whoever is on the outside — the distinction stays in the internal reason,
// which is not returned in enough detail to serve as an oracle.
func naoAutorizado(w http.ResponseWriter, _ string) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="lab-agent"`)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"erro":"nao autorizado"}`))
}
