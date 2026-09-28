package nodeagent

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
	maxBody = 1 << 20

	headerReadTimeout = 10 * time.Second
)

type Server struct {
	Ag      *Agent
	Secret  Secret
	Met     *Metrics
	handler http.Handler
}

func NewServer(ag *Agent, secret Secret, met *Metrics) *Server {
	s := &Server{Ag: ag, Secret: secret, Met: met}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /metrics", s.metrics)
	mux.Handle("POST /v1/op/{op}", RequireBearer(secret, http.HandlerFunc(s.runOp)))
	mux.Handle("GET /v1/artifact/{handle}", RequireBearer(secret, http.HandlerFunc(s.openArtifact)))
	mux.Handle("POST /v1/artifact", RequireBearer(secret, http.HandlerFunc(s.receiveArtifact)))

	s.handler = mux
	return s
}

func (s *Server) Handler() http.Handler { return s.handler }

func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(`{"ok":true}`))
}

func (s *Server) metrics(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_, _ = io.WriteString(w, s.Met.Render())
}

func (s *Server) runOp(w http.ResponseWriter, r *http.Request) {
	name := gameservers.OpName(r.PathValue("op"))

	op, exists := Lookup(name)
	if !exists {
		s.Met.Count(string(name), "unknown")
		respond(w, http.StatusNotFound, map[string]any{"error": "unknown operation", "op": string(name)})
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	if err != nil {
		s.Met.Count(string(name), "error")
		respond(w, http.StatusBadRequest, map[string]any{"error": "unreadable body"})
		return
	}
	if len(body) > maxBody {
		s.Met.Count(string(name), "large")
		respond(w, http.StatusRequestEntityTooLarge, map[string]any{"error": "body above the limit"})
		return
	}
	if len(body) == 0 {
		body = []byte("{}")
	}

	res, err := op.Handler(s.Ag, r.Context(), json.RawMessage(body))
	if err != nil {
		s.Met.Count(string(name), "error")
		respond(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	s.Met.Count(string(name), "ok")
	respond(w, http.StatusOK, res)
}

func (s *Server) openArtifact(w http.ResponseWriter, r *http.Request) {
	h := gameservers.Handle(r.PathValue("handle"))
	if s.Ag == nil || s.Ag.Back == nil {
		s.Met.Count("artifact", "error")
		respond(w, http.StatusInternalServerError, map[string]any{"error": "agent has no back end configured"})
		return
	}
	rc, err := s.Ag.Back.Open(r.Context(), h)
	if err != nil {
		s.Met.Count("artifact", "unknown")
		respond(w, http.StatusNotFound, map[string]any{"error": err.Error()})
		return
	}
	defer rc.Close()

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	if _, err := io.Copy(w, rc); err != nil {
		s.Met.Count("artifact", "error")
		return
	}
	s.Met.Count("artifact", "ok")
}

func (s *Server) receiveArtifact(w http.ResponseWriter, r *http.Request) {
	if s.Ag == nil || s.Ag.Back == nil {
		s.Met.Count("artifact-in", "error")
		respond(w, http.StatusInternalServerError, map[string]any{"error": "agent has no back end configured"})
		return
	}
	h, err := s.Ag.Back.Receive(r.Context(), r.Body)
	if err != nil {
		s.Met.Count("artifact-in", "error")
		respond(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	s.Met.Count("artifact-in", "ok")
	respond(w, http.StatusOK, map[string]any{"handle": string(h)})
}

func respond(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

func listenAddrs(bridgeIP string, port int) ([]string, error) {
	if port <= 0 || port > 65535 {
		return nil, fmt.Errorf("invalid port: %d", port)
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
		net.JoinHostPort("127.0.0.1", fmt.Sprint(port)),
		net.JoinHostPort(bridgeIP, fmt.Sprint(port)),
	}, nil
}

func (s *Server) Listen(ctx context.Context, bridgeIP string, port int) error {
	addrs, err := listenAddrs(bridgeIP, port)
	if err != nil {
		return err
	}

	var listeners []net.Listener
	closeAll := func() {
		for _, l := range listeners {
			_ = l.Close()
		}
	}

	l0, err := net.Listen("tcp", addrs[0])
	if err != nil {
		return fmt.Errorf("listening on %s: %w", addrs[0], err)
	}
	listeners = append(listeners, l0)

	l1, err := net.Listen("tcp", addrs[1])
	if err != nil {
		closeAll()
		return fmt.Errorf("listening on %s: %w", addrs[1], err)
	}
	listeners = append(listeners, l1)

	srv := &http.Server{
		Handler:           s.handler,
		ReadHeaderTimeout: headerReadTimeout,
	}

	errs := make(chan error, len(listeners))
	for _, l := range listeners {
		go func(l net.Listener) { errs <- srv.Serve(l) }(l)
	}

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	case err := <-errs:
		closeAll()
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
