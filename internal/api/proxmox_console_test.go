package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"server-control-panel/internal/auth"
	"server-control-panel/internal/inventory"
	"server-control-panel/internal/pve"
)

// proxmox_console_test.go — the pins for /ws/proxmox/console.
//
// ─────────────────────────────────────────────────────────────────────────────
// 🔴 THE CONSOLE IS THE CONSCIOUS EXCEPTION TO THIS PROJECT'S RULE, "named
// operations, never exec(cmd)" — and the price of the exception is the TRAIL.
//
// An endpoint that hands out a shell is, by definition, the widest surface in
// the dashboard. It exists because the operator works fully remotely and cannot
// reach the Proxmox UI; and it is only justified if every session leaves a record
// of who opened it, on which guest, and when it closed. A console with no trace
// is the opposite of the agent's reason to exist.
//
// That is why three tests in this file are not about the terminal working: they
// are about the trail existing at both ends, about the secret not crossing the
// boundary, and about the token not travelling in the URL.
// ─────────────────────────────────────────────────────────────────────────────

// ----------------------------------------------------------------- doubles --

// consoleFalso is the double for the connection internal/pve returns. It keeps
// EVERYTHING the bridge wrote to the hypervisor — which is how the protocol
// translation becomes verifiable without standing up a PVE.
type consoleFalso struct {
	mu       sync.Mutex
	escritos [][]byte
	saida    chan []byte
	fechado  bool
}

func novoConsoleFalso() *consoleFalso {
	return &consoleFalso{saida: make(chan []byte, 8)}
}

func (c *consoleFalso) ReadMessage() (int, []byte, error) {
	b, ok := <-c.saida
	if !ok {
		return 0, nil, io.EOF
	}
	return websocket.BinaryMessage, b, nil
}

func (c *consoleFalso) WriteMessage(mt int, data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fechado {
		return io.ErrClosedPipe
	}
	c.escritos = append(c.escritos, append([]byte(nil), data...))
	return nil
}

func (c *consoleFalso) SetReadDeadline(time.Time) error  { return nil }
func (c *consoleFalso) SetWriteDeadline(time.Time) error { return nil }
func (c *consoleFalso) SetReadLimit(int64)               {}
func (c *consoleFalso) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.fechado {
		c.fechado = true
		close(c.saida)
	}
	return nil
}

func (c *consoleFalso) vistos() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, 0, len(c.escritos))
	for _, b := range c.escritos {
		out = append(out, string(b))
	}
	return out
}

// esperaFrames waits ACTIVELY for the frames to arrive, with a deadline. It is
// not a clock wait: what is being awaited is an event — the bridge finishing its
// translation — and the test dies in 3 s instead of hanging.
func (c *consoleFalso) esperaFrames(n int) []string {
	prazo := time.Now().Add(3 * time.Second)
	for {
		v := c.vistos()
		if len(v) >= n || time.Now().After(prazo) {
			return v
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// ------------------------------------------------------------- scaffolding --

// servidorDeConsoleDoPainel brings the handler up behind a real httptest.Server
// — WebSocket demands a real handshake, and httptest.NewRecorder does not upgrade.
func servidorDeConsoleDoPainel(t *testing.T, r *Router) (*httptest.Server, string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// auth.WithUser is the scaffolding the package already sanctions: this
		// work does not deliver authentication, and pretending otherwise would be
		// the mistake refused earlier. The real gate is the mux's `protected` (auth.Middleware).
		r.handleProxmoxConsole(w, req.WithContext(auth.WithUser(req.Context(), "sam")))
	}))
	t.Cleanup(srv.Close)
	return srv, "ws" + strings.TrimPrefix(srv.URL, "http")
}

func routerDeConsole(t *testing.T, fake *pveFalso) (*Router, *auth.AuditLog) {
	t.Helper()
	r, _ := novoRouterProxmox(t, cofrePadrao(), fake)
	al, err := auth.NewAuditLog(filepath.Join(t.TempDir(), "audit.log"))
	if err != nil {
		t.Fatal(err)
	}
	r.audit = al
	return r, al
}

// eventosDeConsole returns the console events in CHRONOLOGICAL order.
// `Tail` delivers newest to oldest (which is what the /audit page
// shows); asserting "opened before closed" over that order would prove
// the opposite of what is wanted.
func eventosDeConsole(al *auth.AuditLog) []auth.Event {
	var out []auth.Event
	tail := al.Tail(50)
	for i := len(tail) - 1; i >= 0; i-- {
		if tail[i].Action == "pve.console" {
			out = append(out, tail[i])
		}
	}
	return out
}

// ------------------------------------------------------------------- tests --

// 🔴 TestConsoleTraduzOProtocoloNoSERVIDOR is the pin for the MEASURED pitfall:
// termproxy reads N BYTES after "0:N:", and `data.length` in JavaScript counts
// UTF-16 units. If the frame were assembled in the browser, "é" would arrive cut
// in half — measured against CT 204.
func TestConsoleTraduzOProtocoloNoSERVIDOR(t *testing.T) {
	fake := &pveFalso{console: novoConsoleFalso(), upid: "UPID:pve:x:vncproxy:204:lab@pve!node-lab:"}
	r, _ := routerDeConsole(t, fake)
	_, wsURL := servidorDeConsoleDoPainel(t, r)

	c, _, err := websocket.DefaultDialer.Dial(wsURL+"?node=lxc/204", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	if err := c.WriteJSON(map[string]any{"type": "resize", "cols": 120, "rows": 40}); err != nil {
		t.Fatal(err)
	}
	if err := c.WriteJSON(map[string]any{"type": "input", "data": "é\n"}); err != nil {
		t.Fatal(err)
	}

	frames := fake.console.esperaFrames(2)
	if len(frames) < 2 {
		t.Fatalf("translated frames = %q, want resize and input", frames)
	}
	if frames[0] != "1:120:40:" {
		t.Errorf("resize frame = %q, want \"1:120:40:\"", frames[0])
	}
	if frames[1] != "0:3:é\n" {
		t.Errorf("input frame = %q, want \"0:3:é\\n\" — 3 BYTES, not 2 characters", frames[1])
	}
}

// TestConsoleEntregaASaidaDoTerminal closes the loop in the other direction:
// what the hypervisor spits out reaches the browser as BINARY, without passing
// through a string (transcoding here would break ANSI sequences and UTF-8 split
// across frames).
func TestConsoleEntregaASaidaDoTerminal(t *testing.T) {
	fake := &pveFalso{console: novoConsoleFalso(), upid: "UPID:x"}
	r, _ := routerDeConsole(t, fake)
	_, wsURL := servidorDeConsoleDoPainel(t, r)

	c, _, err := websocket.DefaultDialer.Dial(wsURL+"?node=lxc/204", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	fake.console.saida <- []byte("\x1b[H\x1b[Jlab login: ")
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		mt, data, err := c.ReadMessage()
		if err != nil {
			t.Fatalf("leitura: %v", err)
		}
		if mt == websocket.TextMessage { // control ({"type":"ready"}) — move on
			continue
		}
		if mt != websocket.BinaryMessage {
			t.Fatalf("frame type = %d, want binary", mt)
		}
		if string(data) != "\x1b[H\x1b[Jlab login: " {
			t.Fatalf("output = %q", data)
		}
		return
	}
}

// 🔴 TestConsoleRecusaTokenNaURL. The precedent is explicit in the dashboard
// (00-shell.js: "we do NOT pass the JWT in the URL (?token=) — the query lands in
// the proxy/server access log = a replayable shell credential"). Accepting
// `?token=` here would reopen exactly the hole the terminal's WS already closed,
// and in an endpoint that also hands out a shell.
func TestConsoleRecusaTokenNaURL(t *testing.T) {
	fake := &pveFalso{console: novoConsoleFalso(), upid: "UPID:x"}
	r, _ := routerDeConsole(t, fake)
	_, wsURL := servidorDeConsoleDoPainel(t, r)

	_, resp, err := websocket.DefaultDialer.Dial(wsURL+"?node=lxc/204&token=umjwtqualquer", nil)
	if err == nil {
		t.Fatal("the upgrade with a token in the URL was accepted")
	}
	if resp == nil || resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %v, want 400", resp)
	}
	if len(fake.console.vistos()) > 0 || fake.chamouConsole {
		t.Error("it went as far as opening a console on the hypervisor even with a token in the URL")
	}
}

// 🔴 TestConsoleSemCredencialExplicaEmVezDeFalhar is CT 202 (`pbs`): it has no
// node token, so it gets no console. The screen has to SAY that — a 409 naming
// the key that is missing — instead of spinning or showing "error".
func TestConsoleSemCredencialExplicaEmVezDeFalhar(t *testing.T) {
	fake := &pveFalso{console: novoConsoleFalso(), upid: "UPID:x"}
	r, _ := routerDeConsole(t, fake)
	if err := r.inventoryStore.Replace(func(iv *inventory.Inventory) {
		iv.Nodes = append(iv.Nodes, noDeTeste("lxc/202", "pbs", 202, agoraDeTeste-10))
	}); err != nil {
		t.Fatal(err)
	}
	_, wsURL := servidorDeConsoleDoPainel(t, r)

	_, resp, err := websocket.DefaultDialer.Dial(wsURL+"?node=lxc/202", nil)
	if err == nil {
		t.Fatal("the upgrade was accepted for a guest with no credential")
	}
	if resp == nil || resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %v, want 409", resp)
	}
	corpo, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(corpo), "pve_token_node_pbs") {
		t.Errorf("body = %s, want it to name the missing key (pve_token_node_pbs)", corpo)
	}
	if fake.chamouConsole {
		t.Error("dialed the hypervisor with no credential")
	}
}

// 🔴 TestConsoleAuditaAberturaEFECHAMENTO — the price of that conscious exception.
// A single event, at open time, would say who came in and never when they left; a
// session opened and forgotten would be indistinguishable from a two-second one.
func TestConsoleAuditaAberturaEFechamento(t *testing.T) {
	fake := &pveFalso{console: novoConsoleFalso(), upid: "UPID:pve:x:vncproxy:204:lab@pve!node-lab:"}
	r, al := routerDeConsole(t, fake)
	_, wsURL := servidorDeConsoleDoPainel(t, r)

	c, _, err := websocket.DefaultDialer.Dial(wsURL+"?node=lxc/204", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	c.Close()

	var evs []auth.Event
	prazo := time.Now().Add(3 * time.Second)
	for {
		if evs = eventosDeConsole(al); len(evs) >= 2 || time.Now().After(prazo) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(evs) != 2 {
		t.Fatalf("pve.console events = %d (%+v), want 2: opened and closed", len(evs), evs)
	}
	if !strings.Contains(evs[0].Target, "abriu") || !strings.Contains(evs[0].Target, "lxc/204") {
		t.Errorf("open event = %q", evs[0].Target)
	}
	if !strings.Contains(evs[1].Target, "fechou") {
		t.Errorf("close event = %q", evs[1].Target)
	}
	for _, e := range evs {
		if e.User != "sam" {
			t.Errorf("event missing the user: %+v", e)
		}
	}
}

// 🔴 TestConsoleNaoVazaSegredoParaOBrowser: nothing the bridge sends to the
// browser may contain the vault's value. The termproxy ticket never even gets
// here (it stays locked inside internal/pve), but the node token passes through
// this handler — and it is the one that would open a shell on any guest of that node.
func TestConsoleNaoVazaSegredoParaOBrowser(t *testing.T) {
	fake := &pveFalso{console: novoConsoleFalso(), upid: "UPID:x"}
	r, _ := routerDeConsole(t, fake)
	_, wsURL := servidorDeConsoleDoPainel(t, r)

	c, resp, err := websocket.DefaultDialer.Dial(wsURL+"?node=lxc/204", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()
	fake.console.saida <- []byte("saída qualquer")

	var tudo strings.Builder
	for k, v := range resp.Header {
		tudo.WriteString(k + strings.Join(v, ""))
	}
	_ = c.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	for i := 0; i < 3; i++ {
		_, data, err := c.ReadMessage()
		if err != nil {
			break
		}
		tudo.Write(data)
	}
	for _, proibido := range []string{"s3cr3t", "lab@pve!node-lab=s3cr3t", "PVEAPIToken"} {
		if strings.Contains(tudo.String(), proibido) {
			t.Errorf("the browser received %q", proibido)
		}
	}
}

// TestConsoleRecusaAlvoQueNaoEGuest: only a guest and the hypervisor itself have a console.
//
// 🔴 THIS TEST USED TO CLAIM THE HOST WOULD NEVER GET A SHELL — "Sys.Console is
// out of scope forever". The "forever" lasted until the operator granted full
// access; the test then failed over a screen that had improved, which is exactly
// the right behaviour for it.
//
// What still holds: a target that is NEITHER a guest NOR the hypervisor has no
// console at all, and an unknown id is a 404. A generic 400 would hide the
// reason, and that is why this test was born.
func TestConsoleRecusaAlvoQueNaoEGuest(t *testing.T) {
	fake := &pveFalso{console: novoConsoleFalso(), upid: "UPID:x"}
	r, _ := routerDeConsole(t, fake)
	if err := r.inventoryStore.Replace(func(iv *inventory.Inventory) {
		iv.Nodes = append(iv.Nodes,
			inventory.Node{ID: "node/pve", Name: "pve", Kind: inventory.NodeKindHost, Transport: inventory.TransportPVEAPI},
			inventory.Node{ID: "vps-187", Name: "vps", Kind: inventory.NodeKindExterno, Transport: inventory.TransportSSH})
	}); err != nil {
		t.Fatal(err)
	}
	_, wsURL := servidorDeConsoleDoPainel(t, r)

	for _, caso := range []struct {
		alvo string
		quer int
	}{
		// The `externo` node (the VPS, until December) is neither a guest of the
		// hypervisor nor the hypervisor itself: there is no termproxy for it anywhere.
		{"vps-187", http.StatusBadRequest},
		{"lxc/999", http.StatusNotFound},
		{"", http.StatusBadRequest},
	} {
		_, resp, err := websocket.DefaultDialer.Dial(wsURL+"?node="+caso.alvo, nil)
		if err == nil {
			t.Fatalf("%q: upgrade aceito", caso.alvo)
		}
		if resp == nil || resp.StatusCode != caso.quer {
			t.Errorf("%q: status = %v, want %d", caso.alvo, resp, caso.quer)
		}
	}
	if fake.chamouConsole {
		t.Error("dialed the hypervisor for a target that is not a guest")
	}
}

// 🔴 TestConsoleUsaOTokenDONO — the token rule applied to the console. What opens
// a console on a guest is THAT node's credential, never the audit one: the `audit`
// token gets a 403 (measured: "Permission check failed (/vms/204, VM.Console)"),
// and the dashboard would show "no permission" on a guest it CAN open.
func TestConsoleUsaOTokenDoNo(t *testing.T) {
	cofre := cofrePadrao()
	fake := &pveFalso{console: novoConsoleFalso(), upid: "UPID:x"}
	r, _ := novoRouterProxmox(t, cofre, fake)
	al, _ := auth.NewAuditLog(filepath.Join(t.TempDir(), "audit.log"))
	r.audit = al
	_, wsURL := servidorDeConsoleDoPainel(t, r)

	c, _, err := websocket.DefaultDialer.Dial(wsURL+"?node=lxc/204", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	c.Close()

	if !cofre.leu("pve_token_node_lab") {
		t.Errorf("keys read = %v, want it to contain pve_token_node_lab", cofre.lidas)
	}
	if cofre.leu(pveSecretAudit) {
		t.Errorf("read the AUDIT token (%v) — it gets a 403 on VM.Console", cofre.lidas)
	}
}

// 🔴 TestNenhumHandlerDevolveTicketDeConsole is the structural pin for the
// invariant: `ticket` and `port` may not exist in internal/api's vocabulary. If
// somebody one day "improves" the design by exposing the ticket so the browser
// can open the WebSocket straight against the hypervisor, the secret starts living
// in DevTools — and this test fails before the deploy.
func TestNenhumHandlerDevolveTicketDeConsole(t *testing.T) {
	proibidos := []string{"vncticket", "vncwebsocket", "PVEVNC"}
	entradas, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entradas {
		nome := e.Name()
		if e.IsDir() || !strings.HasSuffix(nome, ".go") || strings.HasSuffix(nome, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(nome)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range proibidos {
			if strings.Contains(string(raw), p) {
				t.Errorf("%s mentions %q — the console's ticket and port must NOT cross the internal/pve boundary", nome, p)
			}
		}
	}
}

// TestConsoleAvisaOBrowserQuandoOHipervisorRecusa: after the upgrade there is no
// HTTP status left to return. The error has to become a control frame, otherwise
// the screen just spins — the symptom the operator saw on CT 204's vncproxy.
func TestConsoleAvisaOBrowserQuandoOHipervisorRecusa(t *testing.T) {
	fake := &pveFalso{console: novoConsoleFalso(), erroConsole: &pve.Error{Kind: pve.KindForbidden, Status: 403,
		Path: "/api2/json/nodes/pve/lxc/204/termproxy", Body: "Permission check failed (/vms/204, VM.Console)"}}
	r, _ := routerDeConsole(t, fake)
	_, wsURL := servidorDeConsoleDoPainel(t, r)

	c, _, err := websocket.DefaultDialer.Dial(wsURL+"?node=lxc/204", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	mt, data, err := c.ReadMessage()
	if err != nil {
		t.Fatalf("leitura: %v", err)
	}
	if mt != websocket.TextMessage {
		t.Fatalf("type = %d, want text (control frame)", mt)
	}
	var msg map[string]any
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("control is not JSON: %s", data)
	}
	if msg["type"] != "error" {
		t.Errorf("control = %v, want type=error", msg)
	}
	if txt, _ := msg["message"].(string); !strings.Contains(txt, "VM.Console") {
		t.Errorf("message = %q, want it to carry the hypervisor's reason", txt)
	}
}
