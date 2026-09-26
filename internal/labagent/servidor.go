package labagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"server-control-panel/internal/gameservers"
)

const (
	// corpoMax caps the incoming document. A named operation receives a small
	// envelope; a large body is a symptom, not a use.
	corpoMax = 1 << 20 // 1 MiB

	// tempoLeituraCabecalho fecha o slowloris trivial.
	tempoLeituraCabecalho = 10 * time.Second
)

// Servidor is the agent, ready to listen.
type Servidor struct {
	Ag      *Agent
	Seg     Segredo
	Met     *Metricas
	handler http.Handler
}

// NovoServidor assembles the routing.
//
// ONE route serves the whole catalogue: `POST /v1/op/{op}`. There is no route
// per operation, and that is deliberate — with a single route, the agent's list
// of capabilities is exactly the `registry` map, in one place, walkable by a
// test. With N routes, the list becomes "whatever happens to be in the
// ServeMux", which nobody can assert from the outside.
func NovoServidor(ag *Agent, seg Segredo, met *Metricas) *Servidor {
	s := &Servidor{Ag: ag, Seg: seg, Met: met}

	mux := http.NewServeMux()
	// Method-and-path patterns (Go 1.22+). No external router: there are three
	// routes, and the project's own rules say chi only comes in when the
	// middleware chain justifies it.
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /metrics", s.metrics)
	mux.Handle("POST /v1/op/{op}", ExigeBearer(seg, http.HandlerFunc(s.executarOp)))
	// ARTEFACT route. New surface, added on purpose and registered in the
	// allowlist of exec_test.go as well — which is the mechanism working, not
	// being worked around: adding a route has to be a deliberate act, visible in
	// the diff, not an impossible one.
	//
	// Why it needs to exist: a file stream does not fit in a JSON document, so
	// Backend.Abrir is the only point where bytes cross the boundary. Without
	// this route, `world.export` and `backup.download` would return a Handle
	// nobody can open over the network, and the world and backup round-trip the
	// acceptance criterion demands would have no way to happen.
	//
	// Why it does NOT widen the execution surface: the handle is opaque and
	// random, it is not a path; whoever did not receive one from an earlier
	// operation has nothing to send here. A real path sent in place of the
	// handle is simply not in the vault.
	mux.Handle("GET /v1/artefato/{handle}", ExigeBearer(seg, http.HandlerFunc(s.abrirArtefato)))
	// The INBOUND side of the artefact, closing the gap the read side declared:
	// without it, importing a world would require the dashboard to know the
	// node's disk. The body is the file; no name and no path cross over.
	mux.Handle("POST /v1/artefato", ExigeBearer(seg, http.HandlerFunc(s.receberArtefato)))

	s.handler = mux
	return s
}

// Handler exposes the assembled routing (used in tests with httptest).
func (s *Servidor) Handler() http.Handler { return s.handler }

// healthz is a PROCESS probe, not a data surface.
//
// It answers without authentication on purpose — the deploy script's health
// gate has to reach it before any credential exists. In exchange it reveals
// NOTHING beyond liveness: no server name, no path, no version, no hint of
// whether a secret is provisioned. Whoever is on the outside learns only that
// the process answered.
func (s *Servidor) healthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(`{"ok":true}`))
}

func (s *Servidor) metrics(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_, _ = io.WriteString(w, s.Met.Render())
}

// executarOp resolves the name against the registry and delegates.
func (s *Servidor) executarOp(w http.ResponseWriter, r *http.Request) {
	nome := gameservers.OpName(r.PathValue("op"))

	op, existe := Registro(nome)
	if !existe {
		// 404, never 400 or 500: "does not exist" and "failed" must never get
		// confused in a diagnosis — it is the difference between hunting a bug in
		// the agent and hunting a typo in the client.
		s.Met.Conta(string(nome), "desconhecida")
		responde(w, http.StatusNotFound, map[string]any{"erro": "unknown operation", "op": string(nome)})
		return
	}

	corpo, err := io.ReadAll(io.LimitReader(r.Body, corpoMax+1))
	if err != nil {
		s.Met.Conta(string(nome), "erro")
		responde(w, http.StatusBadRequest, map[string]any{"erro": "unreadable body"})
		return
	}
	if len(corpo) > corpoMax {
		// 413 BEFORE calling the handler: the limit is worth nothing if the work
		// has already happened.
		s.Met.Conta(string(nome), "grande")
		responde(w, http.StatusRequestEntityTooLarge, map[string]any{"erro": "body above the limit"})
		return
	}
	if len(corpo) == 0 {
		corpo = []byte("{}")
	}

	res, err := op.Handler(s.Ag, r.Context(), json.RawMessage(corpo))
	if err != nil {
		s.Met.Conta(string(nome), "erro")
		responde(w, http.StatusInternalServerError, map[string]any{"erro": err.Error()})
		return
	}
	s.Met.Conta(string(nome), "ok")
	responde(w, http.StatusOK, res)
}

// abrirArtefato hands over the bytes a Handle refers to.
//
// There is no `Content-Disposition` carrying a client-supplied name, and no
// file name in the response: the name is the dashboard's business, and it
// already received it alongside the handle. Echoing back here a name the client
// sent is how header injection gets in.
func (s *Servidor) abrirArtefato(w http.ResponseWriter, r *http.Request) {
	h := gameservers.Handle(r.PathValue("handle"))
	if s.Ag == nil || s.Ag.Back == nil {
		s.Met.Conta("artefato", "erro")
		responde(w, http.StatusInternalServerError, map[string]any{"erro": "agent has no back end configured"})
		return
	}
	rc, err := s.Ag.Back.Abrir(r.Context(), h)
	if err != nil {
		// 404 for a handle that does not resolve: the same "does not exist"
		// silence the vault already gives, forged and expired alike.
		s.Met.Conta("artefato", "desconhecida")
		responde(w, http.StatusNotFound, map[string]any{"erro": err.Error()})
		return
	}
	defer rc.Close()

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	if _, err := io.Copy(w, rc); err != nil {
		// The header has already gone out. Only the counter records it — writing
		// an error body here would produce a corrupt file that LOOKS complete.
		s.Met.Conta("artefato", "erro")
		return
	}
	s.Met.Conta("artefato", "ok")
}

// receberArtefato accepts bytes and returns the Handle that refers to them.
//
// The real limit belongs to the back-end (recebidoMax); all that is guaranteed
// here is that the body is not read without a ceiling before it gets there.
func (s *Servidor) receberArtefato(w http.ResponseWriter, r *http.Request) {
	if s.Ag == nil || s.Ag.Back == nil {
		s.Met.Conta("artefato-in", "erro")
		responde(w, http.StatusInternalServerError, map[string]any{"erro": "agent has no back end configured"})
		return
	}
	h, err := s.Ag.Back.Receber(r.Context(), r.Body)
	if err != nil {
		s.Met.Conta("artefato-in", "erro")
		responde(w, http.StatusBadRequest, map[string]any{"erro": err.Error()})
		return
	}
	s.Met.Conta("artefato-in", "ok")
	responde(w, http.StatusOK, map[string]any{"handle": string(h)})
}

func responde(w http.ResponseWriter, codigo int, corpo any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(codigo)
	_ = json.NewEncoder(w).Encode(corpo)
}

// ─────────────────────────────────────────────────────────────────────────────
// BIND
//
// The agent listens on TWO EXPLICIT listeners: loopback and the IP of the
// internal bridge it was handed. Never `":porta"`, never `0.0.0.0`.
//
// Why two explicit listeners and not a wildcard with a filter afterwards:
// binding on a wildcard leaves the port EXISTING for whoever arrives by any
// other route — a new interface, a VPN, a bridge somebody adds later. The
// filter protects the data; it does not protect the surface. With an explicit
// listener, the port simply does not exist outside the two addresses.
//
// ⚠️ THIS DECISION INVERTS THE PROJECT'S OWN RULE FOR THIS CASE.
//
// The old rule stays recorded here ALONGSIDE the new one, never in its place
// (the precedent set the last time a decision of this kind was revised). The
// rule says: "HTTP over the Tailscale interface (100.x), never 0.0.0.0". Three
// measured facts win for this case:
//
//  1. The precedent the rule ITSELF cites already does the opposite:
//     `fanhub.py:18` uses `BIND = "192.168.100.50"` — the internal bridge, not
//     `tailscale0`.
//  2. `games` and `apps` are NOT on the tailnet. The measured `tailscale status`
//     lists only hypervisor-01, data, dev, observ, vm-240 and vps-187. Binding
//     on `tailscale0` would require `/dev/net/tun` plus a reboot on two guests
//     — scope creep by rule, not by need.
//  3. The tailnet is the second ADMINISTRATION path, not a DATA path. Coupling
//     game traffic to it repeats the "single tunnel" mistake the design has
//     already rejected.
//
// The rest of the rule goes on applying in full — in particular the per-node
// bearer with ConstantTimeCompare: with no encryption on the wire, security
// rests ENTIRELY on the bearer, and that is why the mitigations are mandatory.
// ─────────────────────────────────────────────────────────────────────────────

// enderecosDeEscuta validates and returns the two addresses.
func enderecosDeEscuta(bridgeIP string, porta int) ([]string, error) {
	if porta <= 0 || porta > 65535 {
		return nil, fmt.Errorf("invalid port: %d", porta)
	}
	if bridgeIP == "" {
		return nil, errors.New("empty bridgeIP: the agent requires an explicit internal bridge address — binding to a wildcard would leave the port open to any interface")
	}
	if bridgeIP == "0.0.0.0" || bridgeIP == "::" {
		return nil, fmt.Errorf("wildcard bridgeIP refused (%q): the agent requires an explicit address; a wildcard exposes the port to every present and future interface", bridgeIP)
	}
	if net.ParseIP(bridgeIP) == nil {
		return nil, fmt.Errorf("bridgeIP is not an IP address: %q", bridgeIP)
	}
	return []string{
		net.JoinHostPort("127.0.0.1", fmt.Sprint(porta)),
		net.JoinHostPort(bridgeIP, fmt.Sprint(porta)),
	}, nil
}

// Escuta opens both listeners and serves.
//
// A failure of EITHER one brings the agent down at start-up. Better not to come
// up than to come up listening on less than was asked for (the node is
// unreachable and somebody notices) or on more than was asked for (nobody
// notices, which is the dangerous case).
func (s *Servidor) Escuta(ctx context.Context, bridgeIP string, porta int) error {
	enderecos, err := enderecosDeEscuta(bridgeIP, porta)
	if err != nil {
		return err
	}

	var listeners []net.Listener
	fecharTudo := func() {
		for _, l := range listeners {
			_ = l.Close()
		}
	}

	l0, err := net.Listen("tcp", enderecos[0])
	if err != nil {
		return fmt.Errorf("listening on %s: %w", enderecos[0], err)
	}
	listeners = append(listeners, l0)

	l1, err := net.Listen("tcp", enderecos[1])
	if err != nil {
		fecharTudo()
		return fmt.Errorf("listening on %s: %w", enderecos[1], err)
	}
	listeners = append(listeners, l1)

	srv := &http.Server{
		Handler:           s.handler,
		ReadHeaderTimeout: tempoLeituraCabecalho,
	}

	erros := make(chan error, len(listeners))
	for _, l := range listeners {
		go func(l net.Listener) { erros <- srv.Serve(l) }(l)
	}

	select {
	case <-ctx.Done():
		desliga, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(desliga)
	case err := <-erros:
		fecharTudo()
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
