package api

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

const agentPort = 8710

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

func (r *Router) nodeToken(no string) string {
	if r.secrets == nil || no == "" {
		return ""
	}
	u, err := scope.New("sam")
	if err != nil {
		return ""
	}
	secret, ok := scope.NewUserVault(r.secrets, u).Get("node_agent_token_" + no)
	if !ok {
		return ""
	}
	return secret
}

func (r *Router) nodeSource() gameservers.NodeSource {
	return panelNodeSource{st: r.inventoryStore, token: r.nodeToken}
}

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

func translateNodeError(err error, no string) (int, string) {
	var auth *gameservers.AuthorizationError
	var unknown *gameservers.UnknownOperationError
	var oper *gameservers.OperationError

	switch {
	case errorsAs(err, &auth):
		return http.StatusBadGateway,
			"node '" + no + "' rejected the panel credential — reprovision this node's token"
	case errorsAs(err, &unknown):
		return http.StatusBadGateway,
			"the agent on node '" + no + "' does not know this operation: panel and agent are on different versions"
	case errorsAs(err, &oper):
		return http.StatusBadRequest, oper.Msg
	default:
		return http.StatusBadGateway, "could not reach node '" + no + "'"
	}
}

func errorsAs(err error, target any) bool { return errors.As(err, target) }

func (r *Router) execOp(
	w http.ResponseWriter, req *http.Request,
	srv gameservers.Server, op gameservers.OpName, body any, isWrite bool,
) (json.RawMessage, bool) {
	doc, _, _, ok := r.execWithBackend(w, req, srv, op, body, isWrite)
	return doc, ok
}

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

func (r *Router) auditGame(req *http.Request, no, server string, op gameservers.OpName, err error) {
	user := auth.UserFrom(req)
	result := "ok"
	if err != nil {
		result = "error"
	}
	r.auditEvent(req, user, "gameserver."+string(op),
		fmt.Sprintf("node=%s server=%s result=%s", no, server, result))
}

func writeRaw(w http.ResponseWriter, doc json.RawMessage) {
	w.Header().Set("Content-Type", "application/json")
	if len(doc) == 0 {
		doc = json.RawMessage(`{}`)
	}
	_, _ = w.Write(doc)
}

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
