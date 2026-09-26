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

// browserMessage is what the client sends. It is the SAME format as /ws/shell.
type browserMessage struct {
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

	user, ok := r.mustPrimary(w, req)
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
	no, found := findNode(inv, id)
	if !found {
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
	isHost := no.Kind == inventory.NodeKindHost
	if !isHost && (no.Kind != inventory.NodeKindGuest || no.VMID <= 0) {
		writeErr(w, 400, "node "+id+" is neither a guest nor the hypervisor — there is no console for it")
		return
	}

	// The console's credential is the NODE's. The audit token gets 403 on
	// VM.Console — measured: "Permission check failed (/vms/204, VM.Console)".
	// clientForOp already separates the THREE vault states, and its 409
	// NAMES the key that is missing: that is how CT 202 (`pbs`), which has no
	// node token, becomes a sentence on the screen instead of a spinning one.
	op := opGuest
	if isHost {
		op = opHypervisorRead
	}
	cli, ok := r.clientForOp(w, op, no)
	if !ok {
		return
	}

	kind, host := kindAndHost(no)
	if isHost {
		host = no.Name
		if host == "" {
			host = hypervisorNameInInventory(inv)
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
	var writeMu sync.Mutex
	send := func(frameType int, data []byte) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		_ = clientConn.SetWriteDeadline(time.Now().Add(consoleWriteWait))
		return clientConn.WriteMessage(frameType, data)
	}
	sendControl := func(v any) {
		b, err := json.Marshal(v)
		if err != nil {
			return
		}
		_ = send(websocket.TextMessage, b)
	}

	var upConn pve.ConsoleConn
	var upid string
	if isHost {
		upConn, upid, err = cli.ConsoleAttachNode(req.Context(), host)
	} else {
		upConn, upid, err = cli.ConsoleAttach(req.Context(), host, no.VMID, kind)
	}
	if err != nil {
		// The hypervisor's reason goes to the screen WHOLE: "no permission",
		// "guest stopped" and "termproxy failed" call for different actions, and
		// it was precisely one of those failures (CT 204's `vncproxy`) that the
		// operator saw as a screen spinning with no explanation.
		sendControl(map[string]any{
			"type":    "error",
			"code":    "console-indisponivel",
			"fatal":   true,
			"message": "the hypervisor refused the console for " + id + ": " + pveErrorDetail(err),
		})
		r.auditEvent(req, user, "pve.console", "node="+id+" acao=abriu status=recusado motivo="+pveErrorDetail(err))
		r.auditEvent(req, user, "pve.console", "node="+id+" acao=fechou status=recusado")
		return
	}
	defer upConn.Close()

	// 🔴 THE TRAIL, at BOTH ends. A single event, on opening, would leave a
	// session left open and forgotten indistinguishable from a two-second one.
	start := time.Now()
	r.auditEvent(req, user, "pve.console", "node="+id+" acao=abriu upid="+upid)
	defer func() {
		r.auditEvent(req, user, "pve.console",
			fmt.Sprintf("node=%s acao=fechou upid=%s duracao=%s", id, upid, time.Since(start).Round(time.Second)))
	}()

	sendControl(map[string]any{"type": "ready", "node": id, "vmid": no.VMID, "guest": no.Name})

	ready := make(chan struct{})
	var closeOnce sync.Once
	shutdown := func() { closeOnce.Do(func() { close(ready) }) }

	var wg sync.WaitGroup

	// ── hypervisor → browser ────────────────────────────────────────────
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer shutdown()
		for {
			_, data, err := upConn.ReadMessage()
			if err != nil {
				return
			}
			// BINARY, always. Going through a string here would break ANSI
			// sequences and UTF-8 split across two frames — and the terminal would
			// draw junk.
			if err := send(websocket.BinaryMessage, data); err != nil {
				return
			}
		}
	}()

	// ── browser → hypervisor ────────────────────────────────────────────
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer shutdown()
		for {
			_, rawValue, err := clientConn.ReadMessage()
			if err != nil {
				return
			}
			var m browserMessage
			if err := json.Unmarshal(rawValue, &m); err != nil {
				continue
			}
			var frame []byte
			switch m.Type {
			case "input":
				// pve.InputFrame counts BYTES. It is the only reason this
				// translation cannot live in the browser.
				frame = pve.InputFrame([]byte(m.Data))
			case "resize":
				// nil when the dimension is absurd: a malformed frame kills the
				// connection with no explanation, and cols/rows come from the browser.
				frame = pve.ResizeFrame(m.Cols, m.Rows)
			case "ping":
				frame = pve.KeepaliveFrame()
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
			case <-ready:
				return
			case <-t.C:
				if err := send(websocket.PingMessage, nil); err != nil {
					shutdown()
					return
				}
				_ = upConn.SetWriteDeadline(time.Now().Add(consoleWriteWait))
				if err := upConn.WriteMessage(websocket.BinaryMessage, pve.KeepaliveFrame()); err != nil {
					shutdown()
					return
				}
			}
		}
	}()

	<-ready
	// Closing both sides unblocks the two blocked reads; without this the handler
	// would hang on a goroutine waiting for a frame that never comes.
	_ = upConn.Close()
	_ = clientConn.Close()
	wg.Wait()
}
