// lab-agent — the per-node agent.
//
// It serves a CLOSED catalogue of named operations, behind a per-node bearer
// token, on two explicit listeners (loopback and the internal bridge). There is
// no free-execution route, and the defence against one is the SHAPE of the
// handler, not the name of the route — see the header of
// internal/labagent/registry.go.
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
	"server-control-panel/internal/labagent"
)

// backIndisponivel is the back end for when the wiring to the node could NOT
// be made.
//
// THIS IS NOW SETTLED: the local back end really is wired (see montaBackend
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
type backIndisponivel struct{ motivo string }

func (b backIndisponivel) Executar(context.Context, gameservers.OpName, json.RawMessage) (json.RawMessage, error) {
	return nil, fmt.Errorf("%s", b.motivo)
}
func (b backIndisponivel) Abrir(context.Context, gameservers.Handle) (io.ReadCloser, error) {
	return nil, fmt.Errorf("%s", b.motivo)
}
func (b backIndisponivel) Receber(context.Context, io.Reader) (gameservers.Handle, error) {
	return "", fmt.Errorf("%s", b.motivo)
}
func (b backIndisponivel) Descrever() string { return "back end unavailable: " + b.motivo }

// montaBackend wires THIS node's local back end: the game inventory that lives
// on the disk here and the Docker that runs here.
//
// That is the difference between the agent and the panel: the panel operates the
// Docker of its own host; the agent operates the Docker of the node it was
// installed on. The same internal/gameservers serves both — all that changes is
// which machine the dataDir and the socket come from, and that is why extracting
// that package had to happen before this line.
func montaBackend(no, dataDir string) gameservers.Backend {
	if dataDir == "" {
		return backIndisponivel{motivo: "no node data directory (LAB_AGENT_DATA_DIR is empty)"}
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return backIndisponivel{motivo: fmt.Sprintf("data directory %s unreachable: %v", dataDir, err)}
	}
	dc, err := docker.New()
	if err != nil {
		// Name the socket: "docker indisponivel" without saying WHERE sends the
		// investigation to the wrong place when the problem is the unit's
		// ReadWritePaths and not the daemon.
		return backIndisponivel{motivo: fmt.Sprintf("docker on this node is unavailable: %v", err)}
	}

	// A PING at start-up, and the reason is the unit.
	//
	// docker.New() only BUILDS the client — the connection is lazy. Without this
	// ping, an unreachable socket (a unit with ProtectSystem=strict and the socket
	// outside ReadWritePaths, the wrong group, a stopped daemon) would go unnoticed
	// at start-up and would only appear on the first real operation, as a diffuse
	// "permission denied" on some screen, far from the cause. This log turns that
	// into a named line in `journalctl -u lab-agent`.
	//
	// It does NOT bring the agent down nor swap the back end: Docker may be coming
	// up alongside it, and an agent that refused to serve because of that would stop
	// answering /healthz and make the contract revert a binary that was fine.
	ctxPing, cancela := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancela()
	if err := dc.Ping(ctxPing); err != nil {
		log.Printf("lab-agent: ALERT — the Docker socket did not answer the ping (%v). "+
			"The back end is wired up, but every container operation will fail. "+
			"Suspects, in order: the unit's ReadWritePaths does not cover the socket; "+
			"daemon stopped; socket group.", err)
	} else {
		log.Printf("lab-agent: docker on this node answered the ping")
	}
	return gameservers.NovoBackendLocal(gameservers.New(dataDir, dc), no)
}

// carimbo is filled in at link time (-ldflags -X). With no value, in a
// hand-made build, it says so instead of lying about a number.
var carimbo = "sem-carimbo"

func main() {
	var (
		no       = flag.String("no", envOu("LAB_AGENT_NO", "desconhecido"), "node name (label in /metrics)")
		tokenArq = flag.String("token-file", envOu("LAB_AGENT_TOKEN_FILE", "/etc/lab-agent/token"), "0600 file holding this node's bearer token")
		bridge   = flag.String("bridge-ip", envOu("LAB_AGENT_BRIDGE_IP", ""), "internal bridge IP to listen on (required; a wildcard is refused)")
		porta    = flag.Int("porta", envInt("LAB_AGENT_PORTA", 8710), "port for both listeners")
		dataDir  = flag.String("data-dir", envOu("LAB_AGENT_DATA_DIR", ""), "node data directory (game inventory and history)")
	)
	flag.Parse()

	seg, err := labagent.SegredoDeArquivo(*tokenArq)
	if err != nil {
		log.Fatalf("lab-agent: %v", err)
	}
	if !seg.Presente() {
		// It comes up ANYWAY, and inert. Two reasons: the deploy's health gate needs
		// /healthz answering before the token is provisioned, and an agent that refused
		// to start without a token would be indistinguishable from a broken one. Inert
		// and up is diagnosable; dead is not.
		log.Printf("lab-agent: WARNING — no secret at %s: the agent comes up INERT (401 on every operation). /healthz keeps answering.", *tokenArq)
	}

	back := montaBackend(*no, *dataDir)
	log.Printf("lab-agent: back-end = %s", back.Descrever())
	ag := &labagent.Agent{No: *no, Back: back}
	srv := labagent.NovoServidor(ag, seg, labagent.NovasMetricas(*no))

	ctx, para := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer para()

	log.Printf("lab-agent: carimbo=%s", carimbo)
	log.Printf("lab-agent: node=%s listening on 127.0.0.1:%d and %s:%d", *no, *porta, *bridge, *porta)
	if err := srv.Escuta(ctx, *bridge, *porta); err != nil {
		log.Fatalf("lab-agent: %v", err)
	}
	log.Printf("lab-agent: shut down")
}

func envOu(chave, padrao string) string {
	if v := os.Getenv(chave); v != "" {
		return v
	}
	return padrao
}

func envInt(chave string, padrao int) int {
	v := os.Getenv(chave)
	if v == "" {
		return padrao
	}
	var n int
	if _, err := fmt.Sscanf(v, "%d", &n); err != nil {
		return padrao
	}
	return n
}
