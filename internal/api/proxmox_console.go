package api

// proxmox_console.go — the remote console for the PVE guests.
//
// ─────────────────────────────────────────────────────────────────────────────
// 🔴 WHY THIS BRIDGE EXISTS, and why it is not "the browser talking straight to
// the hypervisor".
//
// The naive path would be to hand `{ticket, port}` to the browser and let it
// open the WebSocket against PVE. It works, and it is wrong for four measured
// reasons:
//
//  1. The ticket IS A CONSOLE CREDENTIAL. Whoever holds it opens a shell on the
//     guest without presenting anything else. Handing it to the browser puts it
//     in the DevTools network history and in the cache of whoever is looking at
//     the machine.
//  2. The browser cannot reach the hypervisor. `hypervisor.local` does not
//     resolve away from home, the certificate comes from the cluster's own CA
//     and the SAN lists a stale IP. Only the dashboard holds the TLS pin and the
//     address override (pve/tls.go).
//  3. THE FRAME LENGTH IS IN BYTES. Measured against CT 204: "0:2:é" delivers
//     0xC3 0xA9; "0:1:é" delivers 0xC3 — half a character. And `data.length`
//     in JavaScript counts UTF-16 units. A "ç" typed in the browser would
//     arrive truncated. Whoever counts bytes has to be the Go side.
//  4. Without the bridge there is no TRAIL. And it is the trail that pays for
//     the exception below.
//
// 🔴 THE CONSCIOUS EXCEPTION. This project's own rules say "NAMED operations,
// never exec(cmd)" — a shell endpoint "turns the agent into SSH with extra
// steps and erases its whole justification". A console is exactly that.
// It goes in all the same because the operator works fully remotely and cannot
// reach the Proxmox UI: with no console, a guest whose network does not come up
// is a guest lost until somebody physically walks to the house.
//
// The price of the exception is the record: ONE auditEvent when the session
// OPENS and ONE when it CLOSES, with guest, UPID and duration. A console with no
// trace is the opposite of the agent's reason to exist.
//
// And a shell on the hypervisor's own host stays OUT, forever: that requires
// Sys.Console, which this dashboard does not have and is not going to ask for.
// ─────────────────────────────────────────────────────────────────────────────
//
// The BROWSER-side protocol (JSON text), the same as the /ws/shell the dashboard
// already speaks — reusing the vocabulary avoids a second terminal dialect in
// the same application:
//
//	{"type":"input","data":"…"}          → stdin
//	{"type":"resize","cols":N,"rows":N}  → resize
//	{"type":"ping"}                      → keeps the session alive
//
// From server to browser: BINARY is raw terminal output (xterm.js writes it
// straight out), TEXT is control ({"type":"ready"} / {"type":"error"}).
// The output never passes through a string at any point: transcoding would
// break ANSI sequences and UTF-8 split across two frames.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"server-control-panel/internal/inventory"
	"server-control-panel/internal/pve"
	"server-control-panel/internal/wsorigin"
)

const (
	// consolePongWait / consolePingPeriod: the dashboard pings the browser and
	// sends keepalive to the hypervisor at the same rhythm. Without the keepalive,
	// PVE's proxy ends the idle session and the console dies "on its own" after a
	// coffee break.
	consolePongWait   = 60 * time.Second
	consolePingPeriod = 25 * time.Second
	consoleWriteWait  = 10 * time.Second
	// consoleMaxClientMsg is the ceiling on what the BROWSER sends. Pasting a
	// whole file into the terminal is a real case; 256 KiB covers it with room to
	// spare and stops a malicious tab pushing memory into the dashboard.
	consoleMaxClientMsg = 256 << 10
)

// mensagemDoBrowser is what the client sends. It is the SAME format as /ws/shell.
type mensagemDoBrowser struct {
	Type string `json:"type"`
	Data string `json:"data"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

// handleProxmoxConsole opens the console of a hypervisor guest.
//
// EVERYTHING that can refuse the session happens BEFORE the upgrade, and on
// purpose: after the 101 there is no HTTP status left to return, and an error
// becomes either a control frame or a screen spinning forever. The shape comes
// from stt.go, which authenticates before bringing the WebSocket up for the same
// reason.
func (r *Router) handleProxmoxConsole(w http.ResponseWriter, req *http.Request) {
	// 🔴 THE TOKEN DOES NOT TRAVEL IN THE URL. The precedent is explicit in the
	// dashboard itself (00-shell.js, the terminal's WS): "the query string ends
	// up in the proxy's or the server's access log = a replayable shell
	// credential". Refusing here, rather than merely ignoring it, is what stops
	// somebody "fixing" a future client by sending the JWT as a query parameter
	// with nobody noticing. The session comes from the HttpOnly cookie, which
	// auth.Middleware has already resolved.
	if req.URL.Query().Get("token") != "" {
		writeErr(w, 400, "token does not go in the URL — the panel session travels in the cookie")
		return
	}

	usuario, ok := r.mustPrimary(w, req)
	if !ok {
		return
	}

	st := r.inventoryStoreOrNil(w)
	if st == nil {
		return
	}
	inv, err := st.Snapshot()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}

	id := strings.TrimSpace(req.URL.Query().Get("node"))
	if id == "" {
		writeErr(w, 400, "node is required (e.g. node=lxc/204)")
		return
	}
	no, achou := achaNo(inv, id)
	if !achou {
		writeErr(w, 404, "node not found: "+id)
		return
	}
	// 🔴 THE HOST GAINED A SHELL, and what changed was permission, not design.
	// This block used to refuse with "the host console requires Sys.Console and
	// is out of scope" — true until the operator granted full access.
	//
	// The difference between the two consoles is the whole difference: the
	// guest's is root INSIDE a container; the host's is root ON THE HYPERVISOR,
	// and from there one command takes all nine guests down. That is why it uses
	// the hypervisor's READ token (the full-access one) and not a node token —
	// no node token has Sys.Console, and reusing one here would give a confusing
	// 403 instead of a working screen.
	ehHost := no.Kind == inventory.NodeKindHost
	if !ehHost && (no.Kind != inventory.NodeKindGuest || no.VMID <= 0) {
		writeErr(w, 400, "node "+id+" is neither a guest nor the hypervisor — there is no console for it")
		return
	}

	// The console's credential is the NODE's. The audit token gets 403 on
	// VM.Console — measured: "Permission check failed (/vms/204, VM.Console)".
	// clienteParaOperacao already separates the THREE vault states, and its 409
	// NAMES the key that is missing: that is how CT 202 (`pbs`), which has no
	// node token, becomes a sentence on the screen instead of a spinning one.
	op := opGuest
	if ehHost {
		op = opLeituraHipervisor
	}
	cli, ok := r.clienteParaOperacao(w, op, no)
	if !ok {
		return
	}

	tipo, host := tipoEHost(no)
	if ehHost {
		host = no.Name
		if host == "" {
			host = nomeDoHipervisorNoInventario(inv)
		}
	}

	// ── from here down there is no HTTP status any more ──────────────────
	clientConn, err := wsUpgrader.Upgrade(w, req, wsorigin.SecHeaders())
	if err != nil {
		return
	}
	defer clientConn.Close()
	clientConn.SetReadLimit(consoleMaxClientMsg)
	_ = clientConn.SetReadDeadline(time.Now().Add(consolePongWait))
	clientConn.SetPongHandler(func(string) error {
		return clientConn.SetReadDeadline(time.Now().Add(consolePongWait))
	})

	// A single writer: the ping, the terminal's output and the control frames
	// come from different goroutines, and gorilla/websocket does not accept two
	// writers.
	var escritorMu sync.Mutex
	enviar := func(tipoFrame int, dados []byte) error {
		escritorMu.Lock()
		defer escritorMu.Unlock()
		_ = clientConn.SetWriteDeadline(time.Now().Add(consoleWriteWait))
		return clientConn.WriteMessage(tipoFrame, dados)
	}
	enviarControle := func(v any) {
		b, err := json.Marshal(v)
		if err != nil {
			return
		}
		_ = enviar(websocket.TextMessage, b)
	}

	var upConn pve.ConsoleConn
	var upid string
	if ehHost {
		upConn, upid, err = cli.ConsoleAttachNode(req.Context(), host)
	} else {
		upConn, upid, err = cli.ConsoleAttach(req.Context(), host, no.VMID, tipo)
	}
	if err != nil {
		// The hypervisor's reason goes to the screen WHOLE: "no permission",
		// "guest stopped" and "termproxy failed" call for different actions, and
		// it was precisely one of those failures (CT 204's `vncproxy`) that the
		// operator saw as a screen spinning with no explanation.
		enviarControle(map[string]any{
			"type":    "error",
			"code":    "console-indisponivel",
			"fatal":   true,
			"message": "the hypervisor refused the console for " + id + ": " + detalheDoErroPVE(err),
		})
		r.auditEvent(req, usuario, "pve.console", "node="+id+" acao=abriu status=recusado motivo="+detalheDoErroPVE(err))
		r.auditEvent(req, usuario, "pve.console", "node="+id+" acao=fechou status=recusado")
		return
	}
	defer upConn.Close()

	// 🔴 THE TRAIL, at BOTH ends. A single event, on opening, would leave a
	// session left open and forgotten indistinguishable from a two-second one.
	inicio := time.Now()
	r.auditEvent(req, usuario, "pve.console", "node="+id+" acao=abriu upid="+upid)
	defer func() {
		r.auditEvent(req, usuario, "pve.console",
			fmt.Sprintf("node=%s acao=fechou upid=%s duracao=%s", id, upid, time.Since(inicio).Round(time.Second)))
	}()

	enviarControle(map[string]any{"type": "ready", "node": id, "vmid": no.VMID, "guest": no.Name})

	pronto := make(chan struct{})
	var fecharUma sync.Once
	fechar := func() { fecharUma.Do(func() { close(pronto) }) }

	var wg sync.WaitGroup

	// ── hypervisor → browser ────────────────────────────────────────────
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer fechar()
		for {
			_, dados, err := upConn.ReadMessage()
			if err != nil {
				return
			}
			// BINARY, always. Going through a string here would break ANSI
			// sequences and UTF-8 split across two frames — and the terminal would
			// draw junk.
			if err := enviar(websocket.BinaryMessage, dados); err != nil {
				return
			}
		}
	}()

	// ── browser → hypervisor ────────────────────────────────────────────
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer fechar()
		for {
			_, cru, err := clientConn.ReadMessage()
			if err != nil {
				return
			}
			var m mensagemDoBrowser
			if err := json.Unmarshal(cru, &m); err != nil {
				continue
			}
			var frame []byte
			switch m.Type {
			case "input":
				// pve.FrameDeEntrada counts BYTES. It is the only reason this
				// translation cannot live in the browser.
				frame = pve.FrameDeEntrada([]byte(m.Data))
			case "resize":
				// nil when the dimension is absurd: a malformed frame kills the
				// connection with no explanation, and cols/rows come from the browser.
				frame = pve.FrameDeResize(m.Cols, m.Rows)
			case "ping":
				frame = pve.FrameDeKeepalive()
			default:
				continue
			}
			if frame == nil {
				continue
			}
			_ = upConn.SetWriteDeadline(time.Now().Add(consoleWriteWait))
			if err := upConn.WriteMessage(websocket.BinaryMessage, frame); err != nil {
				return
			}
		}
	}()

	// ── a heartbeat on both sides ───────────────────────────────────────
	//
	// The ping to the browser detects a tab closed without a clean close; the
	// keepalive to the hypervisor stops it ending the idle session.
	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.NewTicker(consolePingPeriod)
		defer t.Stop()
		for {
			select {
			case <-pronto:
				return
			case <-t.C:
				if err := enviar(websocket.PingMessage, nil); err != nil {
					fechar()
					return
				}
				_ = upConn.SetWriteDeadline(time.Now().Add(consoleWriteWait))
				if err := upConn.WriteMessage(websocket.BinaryMessage, pve.FrameDeKeepalive()); err != nil {
					fechar()
					return
				}
			}
		}
	}()

	<-pronto
	// Closing both sides unblocks the two blocked reads; without this the handler
	// would hang on a goroutine waiting for a frame that never comes.
	_ = upConn.Close()
	_ = clientConn.Close()
	wg.Wait()
}
