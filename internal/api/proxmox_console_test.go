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

type fakeConsole struct {
	mu      sync.Mutex
	written [][]byte
	output  chan []byte
	closed  bool
}

func newFakeConsole() *fakeConsole {
	return &fakeConsole{output: make(chan []byte, 8)}
}

func (c *fakeConsole) ReadMessage() (int, []byte, error) {
	b, ok := <-c.output
	if !ok {
		return 0, nil, io.EOF
	}
	return websocket.BinaryMessage, b, nil
}

func (c *fakeConsole) WriteMessage(mt int, data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return io.ErrClosedPipe
	}
	c.written = append(c.written, append([]byte(nil), data...))
	return nil
}

func (c *fakeConsole) SetReadDeadline(time.Time) error  { return nil }
func (c *fakeConsole) SetWriteDeadline(time.Time) error { return nil }
func (c *fakeConsole) SetReadLimit(int64)               {}
func (c *fakeConsole) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closed {
		c.closed = true
		close(c.output)
	}
	return nil
}

func (c *fakeConsole) seen() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, 0, len(c.written))
	for _, b := range c.written {
		out = append(out, string(b))
	}
	return out
}

func (c *fakeConsole) waitFrames(n int) []string {
	deadline := time.Now().Add(3 * time.Second)
	for {
		v := c.seen()
		if len(v) >= n || time.Now().After(deadline) {
			return v
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func panelConsoleServer(t *testing.T, r *Router) (*httptest.Server, string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.handleProxmoxConsole(w, req.WithContext(auth.WithUser(req.Context(), "sam")))
	}))
	t.Cleanup(srv.Close)
	return srv, "ws" + strings.TrimPrefix(srv.URL, "http")
}

func consoleRouter(t *testing.T, fake *fakePVE) (*Router, *auth.AuditLog) {
	t.Helper()
	r, _ := newProxmoxRouter(t, defaultVault(), fake)
	al, err := auth.NewAuditLog(filepath.Join(t.TempDir(), "audit.log"))
	if err != nil {
		t.Fatal(err)
	}
	r.audit = al
	return r, al
}

func consoleEvents(al *auth.AuditLog) []auth.Event {
	var out []auth.Event
	tail := al.Tail(50)
	for i := len(tail) - 1; i >= 0; i-- {
		if tail[i].Action == "pve.console" {
			out = append(out, tail[i])
		}
	}
	return out
}

func TestConsoleTranslatesProtocolOnSERVER(t *testing.T) {
	fake := &fakePVE{console: newFakeConsole(), upid: "UPID:pve:x:vncproxy:204:panel@pve!node-lab:"}
	r, _ := consoleRouter(t, fake)
	_, wsURL := panelConsoleServer(t, r)

	c, _, err := websocket.DefaultDialer.Dial(wsURL+"?node=lxc/204", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	if err := c.WriteJSON(map[string]any{"type": "resize", "cols": 120, "rows": 40}); err != nil {
		t.Fatal(err)
	}
	if err := c.WriteJSON(map[string]any{"type": "input", "data": "ñ\n"}); err != nil {
		t.Fatal(err)
	}

	frames := fake.console.waitFrames(2)
	if len(frames) < 2 {
		t.Fatalf("translated frames = %q, want resize and input", frames)
	}
	if frames[0] != "1:120:40:" {
		t.Errorf("resize frame = %q, want \"1:120:40:\"", frames[0])
	}
	if frames[1] != "0:3:ñ\n" {
		t.Errorf("input frame = %q, want \"0:3:ñ\\n\" — 3 BYTES, not 2 characters", frames[1])
	}
}

func TestConsoleDeliversTerminalOutput(t *testing.T) {
	fake := &fakePVE{console: newFakeConsole(), upid: "UPID:x"}
	r, _ := consoleRouter(t, fake)
	_, wsURL := panelConsoleServer(t, r)

	c, _, err := websocket.DefaultDialer.Dial(wsURL+"?node=lxc/204", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	fake.console.output <- []byte("\x1b[H\x1b[Jlab login: ")
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		mt, data, err := c.ReadMessage()
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if mt == websocket.TextMessage {
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

func TestConsoleRejectsTokenInURL(t *testing.T) {
	fake := &fakePVE{console: newFakeConsole(), upid: "UPID:x"}
	r, _ := consoleRouter(t, fake)
	_, wsURL := panelConsoleServer(t, r)

	_, resp, err := websocket.DefaultDialer.Dial(wsURL+"?node=lxc/204&token=anyjwt", nil)
	if err == nil {
		t.Fatal("the upgrade with a token in the URL was accepted")
	}
	if resp == nil || resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %v, want 400", resp)
	}
	if len(fake.console.seen()) > 0 || fake.calledConsole {
		t.Error("it went as far as opening a console on the hypervisor even with a token in the URL")
	}
}

func TestConsoleWithoutCredentialExplainsInsteadOfFailing(t *testing.T) {
	fake := &fakePVE{console: newFakeConsole(), upid: "UPID:x"}
	r, _ := consoleRouter(t, fake)
	if err := r.inventoryStore.Replace(func(iv *inventory.Inventory) {
		iv.Nodes = append(iv.Nodes, testNode("lxc/202", "pbs", 202, testNow-10))
	}); err != nil {
		t.Fatal(err)
	}
	_, wsURL := panelConsoleServer(t, r)

	_, resp, err := websocket.DefaultDialer.Dial(wsURL+"?node=lxc/202", nil)
	if err == nil {
		t.Fatal("the upgrade was accepted for a guest with no credential")
	}
	if resp == nil || resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %v, want 409", resp)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "pve_token_node_pbs") {
		t.Errorf("body = %s, want it to name the missing key (pve_token_node_pbs)", body)
	}
	if fake.calledConsole {
		t.Error("dialed the hypervisor with no credential")
	}
}

func TestConsoleAuditsOpenAndClose(t *testing.T) {
	fake := &fakePVE{console: newFakeConsole(), upid: "UPID:pve:x:vncproxy:204:panel@pve!node-lab:"}
	r, al := consoleRouter(t, fake)
	_, wsURL := panelConsoleServer(t, r)

	c, _, err := websocket.DefaultDialer.Dial(wsURL+"?node=lxc/204", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	c.Close()

	var evs []auth.Event
	deadline := time.Now().Add(3 * time.Second)
	for {
		if evs = consoleEvents(al); len(evs) >= 2 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(evs) != 2 {
		t.Fatalf("pve.console events = %d (%+v), want 2: opened and closed", len(evs), evs)
	}
	if !strings.Contains(evs[0].Target, "opened") || !strings.Contains(evs[0].Target, "lxc/204") {
		t.Errorf("open event = %q", evs[0].Target)
	}
	if !strings.Contains(evs[1].Target, "closed") {
		t.Errorf("close event = %q", evs[1].Target)
	}
	for _, e := range evs {
		if e.User != "sam" {
			t.Errorf("event missing the user: %+v", e)
		}
	}
}

func TestConsoleNeverLeaksSecretToBrowser(t *testing.T) {
	fake := &fakePVE{console: newFakeConsole(), upid: "UPID:x"}
	r, _ := consoleRouter(t, fake)
	_, wsURL := panelConsoleServer(t, r)

	c, resp, err := websocket.DefaultDialer.Dial(wsURL+"?node=lxc/204", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()
	fake.console.output <- []byte("some output")

	var all strings.Builder
	for k, v := range resp.Header {
		all.WriteString(k + strings.Join(v, ""))
	}
	_ = c.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	for i := 0; i < 3; i++ {
		_, data, err := c.ReadMessage()
		if err != nil {
			break
		}
		all.Write(data)
	}
	for _, forbidden := range []string{"s3cr3t", "panel@pve!node-lab=s3cr3t", "PVEAPIToken"} {
		if strings.Contains(all.String(), forbidden) {
			t.Errorf("the browser received %q", forbidden)
		}
	}
}

func TestConsoleRejectsNonGuestTarget(t *testing.T) {
	fake := &fakePVE{console: newFakeConsole(), upid: "UPID:x"}
	r, _ := consoleRouter(t, fake)
	if err := r.inventoryStore.Replace(func(iv *inventory.Inventory) {
		iv.Nodes = append(iv.Nodes,
			inventory.Node{ID: "node/pve", Name: "pve", Kind: inventory.NodeKindHost, Transport: inventory.TransportPVEAPI},
			inventory.Node{ID: "vps-187", Name: "vps", Kind: inventory.NodeKindExternal, Transport: inventory.TransportSSH})
	}); err != nil {
		t.Fatal(err)
	}
	_, wsURL := panelConsoleServer(t, r)

	for _, tc := range []struct {
		target string
		want   int
	}{
		{"vps-187", http.StatusBadRequest},
		{"lxc/999", http.StatusNotFound},
		{"", http.StatusBadRequest},
	} {
		_, resp, err := websocket.DefaultDialer.Dial(wsURL+"?node="+tc.target, nil)
		if err == nil {
			t.Fatalf("%q: upgrade accepted", tc.target)
		}
		if resp == nil || resp.StatusCode != tc.want {
			t.Errorf("%q: status = %v, want %d", tc.target, resp, tc.want)
		}
	}
	if fake.calledConsole {
		t.Error("dialed the hypervisor for a target that is not a guest")
	}
}

func TestConsoleUsesNodeToken(t *testing.T) {
	vault := defaultVault()
	fake := &fakePVE{console: newFakeConsole(), upid: "UPID:x"}
	r, _ := newProxmoxRouter(t, vault, fake)
	al, _ := auth.NewAuditLog(filepath.Join(t.TempDir(), "audit.log"))
	r.audit = al
	_, wsURL := panelConsoleServer(t, r)

	c, _, err := websocket.DefaultDialer.Dial(wsURL+"?node=lxc/204", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	c.Close()

	if !vault.wasRead("pve_token_node_lab") {
		t.Errorf("keys read = %v, want it to contain pve_token_node_lab", vault.reads)
	}
	if vault.wasRead(pveSecretAudit) {
		t.Errorf("read the AUDIT token (%v) — it gets a 403 on VM.Console", vault.reads)
	}
}

func TestNoHandlerReturnsConsoleTicket(t *testing.T) {
	forbiddenBins := []string{"vncticket", "vncwebsocket", "PVEVNC"}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range forbiddenBins {
			if strings.Contains(string(raw), p) {
				t.Errorf("%s mentions %q — the console's ticket and port must NOT cross the internal/pve boundary", name, p)
			}
		}
	}
}

func TestConsoleTellsBrowserWhenHypervisorRefuses(t *testing.T) {
	fake := &fakePVE{console: newFakeConsole(), consoleErr: &pve.Error{Kind: pve.KindForbidden, Status: 403,
		Path: "/api2/json/nodes/pve/lxc/204/termproxy", Body: "Permission check failed (/vms/204, VM.Console)"}}
	r, _ := consoleRouter(t, fake)
	_, wsURL := panelConsoleServer(t, r)

	c, _, err := websocket.DefaultDialer.Dial(wsURL+"?node=lxc/204", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	mt, data, err := c.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
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
