package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"server-control-panel/internal/docker"
	"server-control-panel/internal/gameservers"
	"server-control-panel/internal/nodeagent"
)

type unavailableBackend struct{ reason string }

func (b unavailableBackend) Execute(context.Context, gameservers.OpName, json.RawMessage) (json.RawMessage, error) {
	return nil, fmt.Errorf("%s", b.reason)
}
func (b unavailableBackend) Open(context.Context, gameservers.Handle) (io.ReadCloser, error) {
	return nil, fmt.Errorf("%s", b.reason)
}
func (b unavailableBackend) Receive(context.Context, io.Reader) (gameservers.Handle, error) {
	return "", fmt.Errorf("%s", b.reason)
}
func (b unavailableBackend) Describe() string { return "back end unavailable: " + b.reason }

func buildBackend(no, dataDir string) gameservers.Backend {
	if dataDir == "" {
		return unavailableBackend{reason: "no node data directory (NODE_AGENT_DATA_DIR is empty)"}
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return unavailableBackend{reason: fmt.Sprintf("data directory %s unreachable: %v", dataDir, err)}
	}
	dc, err := docker.New()
	if err != nil {
		return unavailableBackend{reason: fmt.Sprintf("docker on this node is unavailable: %v", err)}
	}

	ctxPing, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := dc.Ping(ctxPing); err != nil {
		log.Printf("node-agent: ALERT — the Docker socket did not answer the ping (%v). "+
			"The back end is wired up, but every container operation will fail. "+
			"Suspects, in order: the unit's ReadWritePaths does not cover the socket; "+
			"daemon stopped; socket group.", err)
	} else {
		log.Printf("node-agent: docker on this node answered the ping")
	}
	return gameservers.NewBackendLocal(gameservers.New(dataDir, dc), no)
}

var stamp = "unstamped"

func main() {
	var (
		no        = flag.String("no", envOr("NODE_AGENT_NODE", "unknown"), "node name (label in /metrics)")
		tokenFile = flag.String("token-file", envOr("NODE_AGENT_TOKEN_FILE", "/etc/node-agent/token"), "0600 file holding this node's bearer token")
		bridge    = flag.String("bridge-ip", envOr("NODE_AGENT_BRIDGE_IP", ""), "internal bridge IP to listen on (required; a wildcard is refused)")
		port      = flag.Int("port", envInt("NODE_AGENT_PORT", 8710), "port for both listeners")
		dataDir   = flag.String("data-dir", envOr("NODE_AGENT_DATA_DIR", ""), "node data directory (game inventory and history)")
	)
	flag.Parse()

	secret, err := nodeagent.SecretFromFile(*tokenFile)
	if err != nil {
		log.Fatalf("node-agent: %v", err)
	}
	if !secret.Present() {
		log.Printf("node-agent: WARNING — no secret at %s: the agent comes up INERT (401 on every operation). /healthz keeps answering.", *tokenFile)
	}

	back := buildBackend(*no, *dataDir)
	log.Printf("node-agent: back-end = %s", back.Describe())
	ag := &nodeagent.Agent{No: *no, Back: back}
	srv := nodeagent.NewServer(ag, secret, nodeagent.NewMetrics(*no))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Printf("node-agent: stamp=%s", stamp)
	log.Printf("node-agent: node=%s listening on 127.0.0.1:%d and %s:%d", *no, *port, *bridge, *port)
	if err := srv.Listen(ctx, *bridge, *port); err != nil {
		log.Fatalf("node-agent: %v", err)
	}
	log.Printf("node-agent: shut down")
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	var n int
	if _, err := fmt.Sscanf(v, "%d", &n); err != nil {
		return fallback
	}
	return n
}
