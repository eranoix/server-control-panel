package api

// The game back-end factory — the ONE point in the dashboard that decides WHERE
// a game operation goes.
//
// # WHY A SINGLE POINT
//
// A scattered factory is how half the screens stay on the old path: each handler
// resolves it its own way, one of them forgets, and the screen goes on working
// — against the local Manager, on the wrong machine. With a single point,
// "which screens go through the agent?" has an answer you can read, and the AST
// pin (TestHandlersNaoChamamManagerDireto) is able to assert the exception.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/gameservers"
	"server-control-panel/internal/httpx"
	"server-control-panel/internal/inventory"
	"server-control-panel/internal/scope"
)

// portaAgente is the port of the lab-agent's two listeners (see cmd/lab-agent).
//
// A constant of ours, not network configuration: it is neither an IP nor a CTID,
// and the physical allocation still lives only in ALOCACAO.tsv under ~/infra.
const portaAgente = 8710

// fonteDeNosDoPainel implements gameservers.FonteDeNos on top of the
// inventory. It is the adapter that keeps `gameservers` from importing
// `inventory`.
type fonteDeNosDoPainel struct {
	st    *inventory.Store
	token func(no string) string
}

func (f fonteDeNosDoPainel) NoPorID(id string) (gameservers.DestinoNo, bool) {
	if f.st == nil {
		return gameservers.DestinoNo{}, false
	}
	inv, err := f.st.Snapshot()
	if err != nil {
		return gameservers.DestinoNo{}, false
	}
	for _, n := range inv.Nodes {
		if n.ID != id && n.Name != id {
			continue
		}
		d := gameservers.DestinoNo{Nome: n.Name, Transport: string(n.Transport)}
		if n.Transport == inventory.TransportAgente {
			if n.Address == "" {
				// An agent node with no address is an incomplete inventory.
				// Returning a destination with no Base would make NovoBackendHTTP
				// fail further along with a worse message; here the transport is
				// declared and the construction names what is missing.
				d.Base = ""
			} else {
				d.Base = fmt.Sprintf("http://%s:%d", n.Address, portaAgente)
			}
			if f.token != nil {
				d.Token = f.token(n.Name)
			}
		}
		return d, true
	}
	return gameservers.DestinoNo{}, false
}

// tokenDoNo reads that node's bearer from the vault — ON THE SERVER.
//
// Same pattern as trainerToken(): the secret is born in the vault, read inside
// the dashboard's process and injected when the client is built. It is never
// serialised to the browser, never travels in a URL and never shows up in an
// error response.
//
// One key per node, not a global one: revoking `games`' token must not take
// `apps` down. It is the same reason the agent has one bearer per node.
func (r *Router) tokenDoNo(no string) string {
	if r.secrets == nil || no == "" {
		return ""
	}
	u, err := scope.New("sam")
	if err != nil {
		return ""
	}
	segredo, ok := scope.NewUserVault(r.secrets, u).Get("lab_agent_token_" + no)
	if !ok {
		return ""
	}
	return segredo
}

// fonteDeNos assembles the adapter with the token reader built in.
func (r *Router) fonteDeNos() gameservers.FonteDeNos {
	return fonteDeNosDoPainel{st: r.inventoryStore, token: r.tokenDoNo}
}

// backendPara resolves a server's destination and builds the back-end.
//
// It is the ONLY function in the `api` package allowed to touch the Manager,
// and the reason is in `gameservers.NovoBackend`: for a transport other than
// the agent's, the local back-end wraps the Manager that already exists.
// Without that exception declared in a single place, the AST pin would have no
// way to tell "the factory" from "a handler that forgot to migrate".
func (r *Router) backendPara(s gameservers.Server) (gameservers.Backend, gameservers.DestinoNo, error) {
	destino, err := gameservers.ResolverDestino(s, r.fonteDeNos())
	if err != nil {
		return nil, gameservers.DestinoNo{}, err
	}
	b, err := gameservers.NovoBackend(destino, r.gameMgr)
	if err != nil {
		return nil, destino, err
	}
	return b, destino, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// ERROR TRANSLATION
//
// A raw back-end error NEVER reaches the browser. None of them carries the
// token today, but "it does not today" is a property of an implementation, not
// of the design: the first message that started including the request's header
// would leak the credential onto a screen. The translation makes that leak
// structurally impossible, and the test asserts it.

// traduzErroDeNo returns the HTTP status and the OPERATOR's message.
func traduzErroDeNo(err error, no string) (int, string) {
	var auth *gameservers.ErroAutorizacao
	var desconhecida *gameservers.ErroOperacaoDesconhecida
	var oper *gameservers.ErroOperacao

	switch {
	case errosComo(err, &auth):
		// Nothing from the error is passed on: it names node and status, and it
		// is exactly the route by which a future, more "useful" message would
		// bring a credential along.
		return http.StatusBadGateway,
			"node '" + no + "' rejected the panel credential — reprovision this node's token"
	case errosComo(err, &desconhecida):
		return http.StatusBadGateway,
			"the agent on node '" + no + "' does not know this operation: panel and agent are on different versions"
	case errosComo(err, &oper):
		// A business message, produced by OUR agent from OUR code. It is the only
		// class that is passed on, because it is the only one that tells the
		// operator what to do ("world X does not exist").
		return http.StatusBadRequest, oper.Msg
	default:
		return http.StatusBadGateway, "could not reach node '" + no + "'"
	}
}

// errosComo is errors.As with the slim signature the switch above uses.
func errosComo(err error, alvo any) bool { return errors.As(err, alvo) }

// ─────────────────────────────────────────────────────────────────────────────
// EXECUTION + AUDIT

// executaOp is the body shared by every case: resolve, execute, audit, translate.
//
// `escrita` decides TWO things: whether the operation requires the primary
// account, and whether it produces an audit event. Auditing reads would fill
// the log with noise — and an audit trail that records everything is one nobody
// reads, which amounts to having no audit trail when it matters.
func (r *Router) executaOp(
	w http.ResponseWriter, req *http.Request,
	srv gameservers.Server, op gameservers.OpName, corpo any, escrita bool,
) (json.RawMessage, bool) {
	doc, _, _, ok := r.executaComBackend(w, req, srv, op, corpo, escrita)
	return doc, ok
}

// executaComBackend is the same path, ALSO returning the back-end that was used.
//
// ⚠️ It exists for a concrete reason, not out of elegance: `Handle` is resolved
// by the IN-MEMORY vault of the local back-end that minted it. Calling
// `backendPara` twice in the same request creates two back-ends, with two
// vaults, and a handle issued by the first does NOT resolve in the second — the
// world and backup downloads would fail with "invalid handle" without anything
// being wrong on the agent. Whoever needs the operation+artifact pair uses this
// function and reuses the same back-end.
func (r *Router) executaComBackend(
	w http.ResponseWriter, req *http.Request,
	srv gameservers.Server, op gameservers.OpName, corpo any, escrita bool,
) (json.RawMessage, gameservers.Backend, gameservers.DestinoNo, bool) {
	if escrita {
		if _, ok := r.mustPrimary(w, req); !ok {
			return nil, nil, gameservers.DestinoNo{}, false
		}
	}

	back, destino, err := r.backendPara(srv)
	if err != nil {
		// A RESOLUTION error (no node, a node that does not exist, an invalid
		// transport) is a configuration message and goes through whole: it names
		// what to fill in.
		httpx.WriteErr(w, http.StatusBadRequest, err.Error())
		return nil, nil, destino, false
	}

	env, err := json.Marshal(corpo)
	if err != nil {
		httpx.WriteErr(w, http.StatusBadRequest, "invalid body")
		return nil, back, destino, false
	}

	res, err := back.Executar(req.Context(), op, env)
	if escrita {
		r.auditaJogo(req, destino.Nome, srv.ID, op, err)
	}
	if err != nil {
		codigo, msg := traduzErroDeNo(err, destino.Nome)
		httpx.WriteErr(w, codigo, msg)
		return nil, back, destino, false
	}
	return res, back, destino, true
}

// auditaJogo records node, server, operation and result.
//
// The result goes into the SAME event, not into a second one: a log that
// records only the attempt does not answer "did this happen?", which is the
// only question anyone asks an audit log after an incident.
func (r *Router) auditaJogo(req *http.Request, no, servidor string, op gameservers.OpName, err error) {
	usuario := auth.UserFrom(req)
	resultado := "ok"
	if err != nil {
		resultado = "erro"
	}
	r.auditEvent(req, usuario, "gameserver."+string(op),
		fmt.Sprintf("node=%s server=%s result=%s", no, servidor, resultado))
}

// escreveBruto returns the document that came from the node to the browser,
// without re-serialising it.
func escreveBruto(w http.ResponseWriter, doc json.RawMessage) {
	w.Header().Set("Content-Type", "application/json")
	if len(doc) == 0 {
		doc = json.RawMessage(`{}`)
	}
	_, _ = w.Write(doc)
}

// nomeSeguroParaDownload cleans the name that goes into Content-Disposition.
//
// The name comes from the client, and a response header assembled from client
// text is header injection. No path manipulation is used here (the pin counts
// zero path operations in this package): what is used is an allowlist of
// characters.
func nomeSeguroParaDownload(nome, padrao string) string {
	var b strings.Builder
	for _, c := range nome {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
			b.WriteRune(c)
		case c == '-' || c == '_' || c == '.':
			b.WriteRune(c)
		}
	}
	limpo := strings.TrimLeft(b.String(), ".")
	if limpo == "" {
		return padrao
	}
	return limpo
}
