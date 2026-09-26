package pve

// console.go — the remote console of a hypervisor guest.
//
// ─────────────────────────────────────────────────────────────────────────────
// 🔴 THE RULE OF THIS FILE: the `ticket` and the `port` DO NOT LEAVE HERE.
//
// termproxy hands back a ticket that IS a console credential — whoever holds
// it, plus the port, opens a shell on the guest without presenting anything
// else. If it crossed the package boundary it would also cross the handler, the
// JSON and the browser, and would end up living in the DevTools network
// history.
//
// That is why ConsoleAttach does the THREE steps at once (termproxy → upgrade →
// auth frame) and hands back an already authenticated connection. There is no
// function that returns the ticket: what is not returned cannot be passed on by
// mistake. It is the same principle as the header of client.go ("the secret
// never leaves here"), applied to the second secret this package came to
// handle.
// ─────────────────────────────────────────────────────────────────────────────
//
// The protocol below was MEASURED against the home hypervisor, on both kinds of
// guest — `lxc/204`, `lxc/205`, `lxc/207` and `qemu/208`, all 200 on termproxy:
//
//	1) POST /api2/json/nodes/{no}/{lxc|qemu}/{vmid}/termproxy      [VM.Console]
//	   → {"data":{"port":"5900","ticket":"PVEVNC:…","user":"lab@pve!node-lab",
//	      "upid":"UPID:pve:…:vncproxy:204:lab@pve!node-lab:"}}
//
//	2) GET /api2/json/nodes/{no}/{tipo}/{vmid}/vncwebsocket?port=&vncticket=
//	   Upgrade: websocket · Sec-WebSocket-Protocol: binary   → 101
//
//	3) FIRST frame from the client: "user:ticket\n"  → the server answers "OK"
//	   then:    "0:<bytes>:<data>"  stdin
//	            "1:<cols>:<rows>:"  resize
//	            "2"                 keepalive
//
// Three things only the measurement handed over, and that the code honours:
//
//   - **`port` is a STRING** in the JSON ("5900"), not a number. Declaring it
//     `int` would break the unmarshal of the WHOLE object, and the symptom
//     would be "the console does not open" with no hint as to why.
//   - **The "OK" belongs to the handshake, not to the terminal.** It is
//     consumed here. If it were passed on, every console would be born with a
//     phantom "OK" on its 1st line.
//   - **The token authenticates the UPGRADE.** A complete handshake with the
//     `PVEAPIToken` header stopped at the ACL (403 `VM.Console`), not at
//     authentication — so it is the header, and not a session cookie, that
//     opens the WebSocket.
//
// And the UPID that comes back says `vncproxy` even though it was asked for as
// `termproxy`: that is the internal name of the task in the hypervisor. It goes
// to the audit trail exactly as it came.

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// consoleHandshakeTimeout caps the upgrade. The TLS handshake measured against
	// this hypervisor takes ~51 ms; 10 s is wide slack over a tailnet.
	consoleHandshakeTimeout = 10 * time.Second
	// consoleOKTimeout caps the wait for the handshake's "OK". With no deadline, a
	// termproxy that came up but does not talk would leave the screen spinning
	// forever — which is exactly the symptom the operator saw on CT 204's
	// `vncproxy`.
	consoleOKTimeout = 10 * time.Second
	// consoleMaxFrame is the ceiling for a frame coming from the hypervisor.
	// Terminal output is small; 256 KiB covers a `cat` of a big file with room to
	// spare and stops a sick upstream from blowing up the panel's memory.
	consoleMaxFrame = 256 << 10
	// consoleMaxDim is the ceiling for cols/rows. It comes from the browser, so it
	// has one.
	consoleMaxDim = 10000
)

// ErrConsoleSemOK marks a handshake that came up but did not confirm. It is a
// sentinel because the caller swaps the text on account of it: "the guest's
// console did not answer" is a different diagnosis from "no permission".
var ErrConsoleSemOK = errors.New("pve: console did not confirm the handshake (expected \"OK\")")

// ConsoleConn is the narrow cut of *websocket.Conn that the panel's bridge
// uses. It exists so the handler's test can inject a double without standing up
// a fake hypervisor, and to make it explicit, in the type, that nobody here
// closes or reconfigures more than this.
type ConsoleConn interface {
	ReadMessage() (messageType int, p []byte, err error)
	WriteMessage(messageType int, data []byte) error
	SetReadDeadline(t time.Time) error
	SetWriteDeadline(t time.Time) error
	SetReadLimit(limit int64)
	Close() error
}

// termproxyResp is the response of step 1. Every field is a string because that
// is how the hypervisor sends them — `port` included.
type termproxyResp struct {
	Port   string `json:"port"`
	Ticket string `json:"ticket"`
	User   string `json:"user"`
	UPID   string `json:"upid"`
}

// UnmarshalJSON accepts `port` as a string OR as a number. The home hypervisor
// sends a string, and that is what the test pins; but the hypervisor serializes
// the same field in different shapes depending on the route (the same reason
// TokenInfo.UnmarshalJSON exists), and an upgrade that changes the shape must
// not take the whole console down.
func (t *termproxyResp) UnmarshalJSON(raw []byte) error {
	var aux struct {
		Port   json.RawMessage `json:"port"`
		Ticket string          `json:"ticket"`
		User   string          `json:"user"`
		UPID   string          `json:"upid"`
	}
	if err := json.Unmarshal(raw, &aux); err != nil {
		return err
	}
	t.Ticket, t.User, t.UPID = aux.Ticket, aux.User, aux.UPID
	t.Port = strings.Trim(strings.TrimSpace(string(aux.Port)), `"`)
	return nil
}

// ConsoleAttach opens a guest's console and hands back the connection ALREADY
// AUTHENTICATED, plus the UPID of the task (for the audit trail).
//
// The caller gets a bidirectional pipe of bytes: write the frames from
// FrameDeEntrada/FrameDeResize/FrameDeKeepalive and read the terminal output.
// Neither the ticket nor the port crosses this boundary.
func (c *Client) ConsoleAttach(ctx context.Context, node string, vmid int, typ string) (ConsoleConn, string, error) {
	base, err := guestPath(node, vmid, typ)
	if err != nil {
		return nil, "", err
	}
	return c.consoleEm(ctx, base)
}

// ConsoleAttachNode opens the shell of the HYPERVISOR ITSELF — the "Shell"
// button on the Proxmox screen.
//
// 🔴 THIS ONLY EXISTS BECAUSE THE TOKEN WAS GIVEN FULL ACCESS. `POST
// /nodes/{n}/termproxy` requires Sys.Console, which the audit token did not
// have; the route answered 403. Once the operator granted full access, it
// started answering.
//
// It is the same three-step protocol as the guest console, with ONE difference
// that is the whole difference: the path has no guest segment, so this shell is
// root ON THE HYPERVISOR. Whoever reaches it can power off all nine guests with
// one command — and the screen has to say so before opening, not after.
func (c *Client) ConsoleAttachNode(ctx context.Context, node string) (ConsoleConn, string, error) {
	if node == "" {
		return nil, "", fmt.Errorf("pve: empty node in ConsoleAttachNode")
	}
	return c.consoleEm(ctx, "/api2/json/nodes/"+url.PathEscape(node))
}

// consoleEm runs the three steps from a path base. Guest and node share the
// whole protocol; duplicating it would be two truths about the same handshake,
// and the second would age in silence.
func (c *Client) consoleEm(ctx context.Context, base string) (ConsoleConn, string, error) {
	// ── step 1: termproxy ─────────────────────────────────────────────────
	//
	// A 403 here is the end of the road, and on purpose: trying the upgrade
	// afterwards would spend a connection to receive another 403, and the Kind
	// that reached the screen would be the upgrade's, not that of the operation
	// the operator asked for.
	var tp termproxyResp
	if err := c.do(ctx, http.MethodPost, base+"/termproxy", &tp); err != nil {
		return nil, "", err
	}
	if tp.Port == "" || tp.Ticket == "" || tp.User == "" {
		// Without naming what came back: the only interesting field would be the
		// ticket.
		return nil, "", &Error{Kind: KindHypervisor, Path: base + "/termproxy",
			Err: errors.New("termproxy answered without port/ticket/user")}
	}

	// ── step 2: upgrade to WebSocket ─────────────────────────────────────
	wsURL, err := c.consoleWSURL(base, tp.Port, tp.Ticket)
	if err != nil {
		return nil, "", err
	}
	h := http.Header{}
	h.Set("Authorization", "PVEAPIToken="+c.tokenID+"="+c.secret)
	conn, resp, err := c.consoleDialer().DialContext(ctx, wsURL, h)
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		// The URL does NOT go into the error's Path: it carries the vncticket in the
		// query.
		return nil, "", &Error{Kind: kindDoStatus(status, err), Status: status,
			Path: base + "/vncwebsocket", Err: err}
	}
	conn.SetReadLimit(consoleMaxFrame)

	// ── step 3: the auth frame, and the "OK" that STAYS HERE ─────────────
	_ = conn.SetWriteDeadline(time.Now().Add(consoleOKTimeout))
	if err := conn.WriteMessage(websocket.BinaryMessage, []byte(tp.User+":"+tp.Ticket+"\n")); err != nil {
		_ = conn.Close()
		return nil, "", &Error{Kind: KindUnreachable, Path: base + "/vncwebsocket", Err: err}
	}
	_ = conn.SetReadDeadline(time.Now().Add(consoleOKTimeout))
	_, ack, err := conn.ReadMessage()
	if err != nil {
		_ = conn.Close()
		return nil, "", &Error{Kind: KindUnreachable, Path: base + "/vncwebsocket", Err: err}
	}
	if strings.TrimSpace(string(ack)) != "OK" {
		_ = conn.Close()
		// The body does NOT go in: what arrived here could be anything, and the
		// ticket travelled over this very connection.
		return nil, "", &Error{Kind: KindHypervisor, Path: base + "/vncwebsocket", Err: ErrConsoleSemOK}
	}
	// Deadlines cleared: what sets the pace now is the panel's bridge, which has
	// its own keepalive. A deadline inherited from here would kill an idle
	// console.
	_ = conn.SetReadDeadline(time.Time{})
	_ = conn.SetWriteDeadline(time.Time{})
	return conn, tp.UPID, nil
}

// consoleWSURL builds the URL of step 2 from the client's base, swapping the
// http(s) scheme for ws(s). The ticket goes in the query BECAUSE that is how
// the hypervisor demands it — and that is exactly why this URL never shows up
// in an error or in a log.
func (c *Client) consoleWSURL(base, port, ticket string) (string, error) {
	u, err := url.Parse(c.baseURL + base + "/vncwebsocket")
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrBaseURL, err)
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	default:
		return "", fmt.Errorf("%w: scheme %q", ErrBaseURL, u.Scheme)
	}
	u.RawQuery = url.Values{"port": {port}, "vncticket": {ticket}}.Encode()
	return u.String(), nil
}

// consoleDialer reuses the SAME TLS pin and the SAME address redirect as the
// http.Client (tls.go). Building a dialer with a config of its own would open a
// second verification policy — and the second would age in silence.
func (c *Client) consoleDialer() *websocket.Dialer {
	d := &net.Dialer{Timeout: dialTimeout, KeepAlive: 30 * time.Second}
	return &websocket.Dialer{
		HandshakeTimeout: consoleHandshakeTimeout,
		Subprotocols:     []string{"binary"},
		TLSClientConfig:  c.tlsCfg.Clone(),
		NetDialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return d.DialContext(ctx, network, redirectAddr(addr, c.resolve))
		},
	}
}

// kindDoStatus classifies the UPGRADE failure into the same states as do().
// Without it, a 403 on the upgrade would become "unreachable" and the screen
// would send the operator hunting the network when the problem is ACL.
func kindDoStatus(status int, err error) Kind {
	switch {
	case status == http.StatusUnauthorized:
		return KindNoCredential
	case status == http.StatusForbidden:
		return KindForbidden
	case status >= 400:
		return KindHypervisor
	case err != nil:
		return KindUnreachable
	default:
		return KindHypervisor
	}
}

// FrameDeEntrada builds the stdin frame of the termproxy protocol.
//
// 🔴 THE LENGTH IS IN BYTES, and that was measured, not deduced. Against CT 204:
//
//	"0:2:é" (2 bytes)     → the terminal received 0xC3 0xA9  ✅
//	"0:1:é" (1 character) → the terminal received 0xC3       ❌ half a character
//
// It is this line that forces the translation to live on the SERVER:
// `data.length` in JavaScript counts UTF-16 units, so "ç", "é" and emoji typed
// in the browser would arrive cut. The browser sends text; the one who counts
// bytes is Go.
func FrameDeEntrada(dados []byte) []byte {
	out := make([]byte, 0, len(dados)+8)
	out = append(out, "0:"...)
	out = strconv.AppendInt(out, int64(len(dados)), 10)
	out = append(out, ':')
	return append(out, dados...)
}

// FrameDeResize builds the resize frame. It returns nil — and not a crooked
// frame — for a dimension out of range: cols/rows come from the browser, and a
// frame termproxy cannot read kills the connection with no explanation.
func FrameDeResize(cols, rows int) []byte {
	if cols <= 0 || rows <= 0 || cols > consoleMaxDim || rows > consoleMaxDim {
		return nil
	}
	return []byte("1:" + strconv.Itoa(cols) + ":" + strconv.Itoa(rows) + ":")
}

// FrameDeKeepalive is the "2" that pve-xtermjs's main.js sends periodically.
// Without it termproxy closes the idle session.
func FrameDeKeepalive() []byte { return []byte("2") }

// tlsConfigDoCliente returns the TLS pin the transport built, for the WebSocket
// dialer to reuse. Outside this file nobody needs it.
func tlsConfigDoCliente(tr *http.Transport) *tls.Config { return tr.TLSClientConfig }
