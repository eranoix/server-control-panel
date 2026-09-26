// node-agent — the per-node agent.
//
// It serves a CLOSED catalogue of named operations, behind a per-node bearer
// token, on two explicit listeners (loopback and the internal bridge). There is
// no free-execution route, and the defence against one is the SHAPE of the
// handler, not the name of the route — see the header of
// internal/nodeagent/registry.go.
//
// What this binary deliberately does NOT do: anything hypervisor-shaped. Start,
// stop, snapshot and clone of a VM/LXC stay on the PVE API behind a `privsep=1`
// token. Reimplementing that here would trade an auditable authority for an
// invented one.
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

// unavailableBackend is the back end for when the wiring to the node could NOT
// be made.
//
// THIS IS NOW SETTLED: the local back end really is wired (see buildBackend
// just below). This type stopped being the normal path and became the FAILURE
// path — what is left when the node's Docker does not answer or the data
// directory does not exist.
//
// IT FAILS LOUD, ON PURPOSE, and that has not changed. A silent stub answering
// an empty 200 would make the wiring look finished: the screens would come back
// with empty lists, somebody would conclude "there are no worlds" and the defect
// would only surface much later, far from its cause. A named error is the only
// honest answer a part that does not exist can give.
//
// Why it does not stop the process from coming up: /healthz has to answer before
// the deploy's health gate decides, and an agent that refused to start because of
// Docker would be indistinguishable from a broken binary — the contract would
// roll back a binary that was fine. Same reasoning as for the missing token.
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

// buildBackend wires THIS node's local back end: the game inventory that lives
// on the disk here and the Docker that runs here.
//
// That is the difference between the agent and the panel: the panel operates the
// Docker of its own host; the agent operates the Docker of the node it was
// installed on. The same internal/gameservers serves both — all that changes is
// which machine the dataDir and the socket come from, and that is why extracting
// that package had to happen before this line.
func buildBackend(no, dataDir string) gameservers.Backend {
	if dataDir == "" {
		return unavailableBackend{reason: "no node data directory (NODE_AGENT_DATA_DIR is empty)"}
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return unavailableBackend{reason: fmt.Sprintf("data directory %s unreachable: %v", dataDir, err)}
	}
	dc, err := docker.New()
	if err != nil {
		// Say WHERE docker failed: a bare "docker unavailable" sends the
		// investigation to the daemon when the problem is often the unit's
		// ReadWritePaths.
		return unavailableBackend{reason: fmt.Sprintf("docker on this node is unavailable: %v", err)}
	}

	// A PING at start-up, and the reason is the unit.
	//
	// docker.New() only BUILDS the client — the connection is lazy. Without this
	// ping, an unreachable socket (a unit with ProtectSystem=strict and the socket
	// outside ReadWritePaths, the wrong group, a stopped daemon) would go unnoticed
	// at start-up and would only appear on the first real operation, as a diffuse
	// "permission denied" on some screen, far from the cause. This log turns that
	// into a named line in `journalctl -u node-agent`.
	//
	// It does NOT bring the agent down nor swap the back end: Docker may be coming
	// up alongside it, and an agent that refused to serve because of that would stop
	// answering /healthz and make the contract revert a binary that was fine.
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

// stamp is filled in at link time (-ldflags -X). A hand-made build says so
// instead of reporting a made-up number.
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
		// It comes up ANYWAY, and inert. Two reasons: the deploy's health gate needs
		// /healthz answering before the token is provisioned, and an agent that refused
		// to start without a token would be indistinguishable from a broken one. Inert
		// and up is diagnosable; dead is not.
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
