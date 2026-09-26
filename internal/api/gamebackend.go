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
// pin (TestHandlersDoNotCallManagerDirectly) is able to assert the exception.

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

// agentPort is the port of the lab-agent's two listeners (see cmd/lab-agent).
//
// A constant of ours, not network configuration: it is neither an IP nor a CTID,
// and the physical allocation still lives only in ALOCACAO.tsv under ~/infra.
const agentPort = 8710

// panelNodeSource implements gameservers.NodeSource on top of the
// inventory. It is the adapter that keeps `gameservers` from importing
// `inventory`.
type panelNodeSource struct {
	st    *inventory.Store
	token func(no string) string
}

func (f panelNodeSource) NodeByID(id string) (gameservers.NodeTarget, bool) {
	if f.st == nil {
		return gameservers.NodeTarget{}, false
	}
	inv, err := f.st.Snapshot()
	if err != nil {
		return gameservers.NodeTarget{}, false
	}
	for _, n := range inv.Nodes {
		if n.ID != id && n.Name != id {
			continue
		}
		d := gameservers.NodeTarget{Name: n.Name, Transport: string(n.Transport)}
		if n.Transport == inventory.TransportAgent {
			if n.Address == "" {
				// An agent node with no address is an incomplete inventory.
				// Returning a destination with no Base would make NewBackendHTTP
				// fail further along with a worse message; here the transport is
				// declared and the construction names what is missing.
				d.Base = ""
			} else {
				d.Base = fmt.Sprintf("http://%s:%d", n.Address, agentPort)
			}
			if f.token != nil {
				d.Token = f.token(n.Name)
			}
		}
		return d, true
	}
	return gameservers.NodeTarget{}, false
}

// nodeToken reads that node's bearer from the vault — ON THE SERVER.
//
// Same pattern as trainerToken(): the secret is born in the vault, read inside
// the dashboard's process and injected when the client is built. It is never
// serialised to the browser, never travels in a URL and never shows up in an
// error response.
//
// One key per node, not a global one: revoking `games`' token must not take
// `apps` down. It is the same reason the agent has one bearer per node.
func (r *Router) nodeToken(no string) string {
	if r.secrets == nil || no == "" {
		return ""
	}
	u, err := scope.New("sam")
	if err != nil {
		return ""
	}
	secret, ok := scope.NewUserVault(r.secrets, u).Get("lab_agent_token_" + no)
	if !ok {
		return ""
	}
	return secret
}

// nodeSource assembles the adapter with the token reader built in.
func (r *Router) nodeSource() gameservers.NodeSource {
	return panelNodeSource{st: r.inventoryStore, token: r.nodeToken}
}

// backendFor resolves a server's destination and builds the back-end.
//
// It is the ONLY function in the `api` package allowed to touch the Manager,
// and the reason is in `gameservers.NewBackend`: for a transport other than
// the agent's, the local back-end wraps the Manager that already exists.
// Without that exception declared in a single place, the AST pin would have no
// way to tell "the factory" from "a handler that forgot to migrate".
func (r *Router) backendFor(s gameservers.Server) (gameservers.Backend, gameservers.NodeTarget, error) {
	dest, err := gameservers.ResolveTarget(s, r.nodeSource())
	if err != nil {
		return nil, gameservers.NodeTarget{}, err
	}
	b, err := gameservers.NewBackend(dest, r.gameMgr)
	if err != nil {
		return nil, dest, err
	}
	return b, dest, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// ERROR TRANSLATION
//
// A raw back-end error NEVER reaches the browser. None of them carries the
// token today, but "it does not today" is a property of an implementation, not
// of the design: the first message that started including the request's header
// would leak the credential onto a screen. The translation makes that leak
// structurally impossible, and the test asserts it.

// translateNodeError returns the HTTP status and the OPERATOR's message.
func translateNodeError(err error, no string) (int, string) {
	var auth *gameservers.AuthorizationError
	var unknown *gameservers.UnknownOperationError
	var oper *gameservers.OperationError

	switch {
	case errorsAs(err, &auth):
		// Nothing from the error is passed on: it names node and status, and it
		// is exactly the route by which a future, more "useful" message would
		// bring a credential along.
		return http.StatusBadGateway,
			"node '" + no + "' rejected the panel credential — reprovision this node's token"
	case errorsAs(err, &unknown):
		return http.StatusBadGateway,
			"the agent on node '" + no + "' does not know this operation: panel and agent are on different versions"
	case errorsAs(err, &oper):
		// A business message, produced by OUR agent from OUR code. It is the only
		// class that is passed on, because it is the only one that tells the
		// operator what to do ("world X does not exist").
		return http.StatusBadRequest, oper.Msg
	default:
		return http.StatusBadGateway, "could not reach node '" + no + "'"
	}
}

// errorsAs is errors.As with the slim signature the switch above uses.
func errorsAs(err error, target any) bool { return errors.As(err, target) }

// ─────────────────────────────────────────────────────────────────────────────
// EXECUTION + AUDIT

// execOp is the body shared by every case: resolve, execute, audit, translate.
//
// `escrita` decides TWO things: whether the operation requires the primary
// account, and whether it produces an audit event. Auditing reads would fill
// the log with noise — and an audit trail that records everything is one nobody
// reads, which amounts to having no audit trail when it matters.
func (r *Router) execOp(
	w http.ResponseWriter, req *http.Request,
	srv gameservers.Server, op gameservers.OpName, body any, isWrite bool,
) (json.RawMessage, bool) {
	doc, _, _, ok := r.execWithBackend(w, req, srv, op, body, isWrite)
	return doc, ok
}

// execWithBackend is the same path, ALSO returning the back-end that was used.
//
// ⚠️ It exists for a concrete reason, not out of elegance: `Handle` is resolved
// by the IN-MEMORY vault of the local back-end that minted it. Calling
// `backendFor` twice in the same request creates two back-ends, with two
// vaults, and a handle issued by the first does NOT resolve in the second — the
// world and backup downloads would fail with "invalid handle" without anything
// being wrong on the agent. Whoever needs the operation+artifact pair uses this
// function and reuses the same back-end.
func (r *Router) execWithBackend(
	w http.ResponseWriter, req *http.Request,
	srv gameservers.Server, op gameservers.OpName, body any, isWrite bool,
) (json.RawMessage, gameservers.Backend, gameservers.NodeTarget, bool) {
	if isWrite {
		if _, ok := r.mustPrimary(w, req); !ok {
			return nil, nil, gameservers.NodeTarget{}, false
		}
	}

	back, dest, err := r.backendFor(srv)
	if err != nil {
		// A RESOLUTION error (no node, a node that does not exist, an invalid
		// transport) is a configuration message and goes through whole: it names
		// what to fill in.
		httpx.WriteErr(w, http.StatusBadRequest, err.Error())
		return nil, nil, dest, false
	}

	env, err := json.Marshal(body)
	if err != nil {
		httpx.WriteErr(w, http.StatusBadRequest, "invalid body")
		return nil, back, dest, false
	}

	res, err := back.Execute(req.Context(), op, env)
	if isWrite {
		r.auditGame(req, dest.Name, srv.ID, op, err)
	}
	if err != nil {
		code, msg := translateNodeError(err, dest.Name)
		httpx.WriteErr(w, code, msg)
		return nil, back, dest, false
	}
	return res, back, dest, true
}

// auditGame records node, server, operation and result.
//
// The result goes into the SAME event, not into a second one: a log that
// records only the attempt does not answer "did this happen?", which is the
// only question anyone asks an audit log after an incident.
func (r *Router) auditGame(req *http.Request, no, server string, op gameservers.OpName, err error) {
	user := auth.UserFrom(req)
	result := "ok"
	if err != nil {
		result = "erro"
	}
	r.auditEvent(req, user, "gameserver."+string(op),
		fmt.Sprintf("node=%s server=%s result=%s", no, server, result))
}

// writeRaw returns the document that came from the node to the browser,
// without re-serialising it.
func writeRaw(w http.ResponseWriter, doc json.RawMessage) {
	w.Header().Set("Content-Type", "application/json")
	if len(doc) == 0 {
		doc = json.RawMessage(`{}`)
	}
	_, _ = w.Write(doc)
}

// safeDownloadName cleans the name that goes into Content-Disposition.
//
// The name comes from the client, and a response header assembled from client
// text is header injection. No path manipulation is used here (the pin counts
// zero path operations in this package): what is used is an allowlist of
// characters.
func safeDownloadName(name, fallback string) string {
	var b strings.Builder
	for _, c := range name {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
			b.WriteRune(c)
		case c == '-' || c == '_' || c == '.':
			b.WriteRune(c)
		}
	}
	clean := strings.TrimLeft(b.String(), ".")
	if clean == "" {
		return fallback
	}
	return clean
}
