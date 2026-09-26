package pve

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// console_test.go — the pins of the remote console.
//
// The fake server here is NOT an invented Proxmox: it repeats, step by step,
// what was MEASURED against the home hypervisor, on both kinds of guest
// (lxc/204, lxc/205, lxc/207 and qemu/208, all 200 on termproxy):
//
//	POST /nodes/pve/{type}/{vmid}/termproxy   → {port, user, ticket, upid}
//	GET  .../vncwebsocket?port=&vncticket=    → 101, subprotocol "binary"
//	1st frame from the client: "user:ticket\n" → the server answers "OK"
//
// The "OK" is the detail only the measurement hands over, and it is what these
// tests protect: it is a HANDSHAKE answer, not terminal output. Passing it on
// to the browser would write a phantom "OK" on the first line of every console.

const (
	fakeConsoleTicket = "PVEVNC:AAAAAA==::ticket-measured-at-361-bytes"
	fakeConsoleUser   = "panel@pve!node-lab"
	fakeConsoleUPID   = "UPID:pve:003D8997:0492E623:6A868978:vncproxy:204:panel@pve!node-lab:"
)

// consoleServer builds the complete fake hypervisor (termproxy +
// vncwebsocket). repliesOK says whether the server confirms the auth frame the
// way the real hypervisor confirms it.
type consoleServer struct {
	srv *httptest.Server

	// mu guards the fields the server's goroutine writes and the test's reads.
	// Without it -race fails — and rightly so: the console bridge is precisely
	// two-goroutine code, so the test of it cannot be the example of the opposite.
	mu sync.Mutex

	consoleOptions

	authReceived     string
	authHeaderWS     string
	framesReceived   []string
	upgradeAttempted bool
}

func (s *consoleServer) seen() (auth, header string, frames []string, upgrade bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.authReceived, s.authHeaderWS, append([]string(nil), s.framesReceived...), s.upgradeAttempted
}

// consoleOptions is the fake server's configuration, SEPARATE from the state
// it accumulates. Merging the two into a single struct would pass a sync.Mutex
// by value — which go vet flags, and rightly so.
type consoleOptions struct {
	statusTermproxy int    // 0 = 200
	repliesOK       bool   // sends "OK" after the auth frame
	initialOutput   string // what the "terminal" spits out after the handshake
}

func newConsoleServer(t *testing.T, cfg consoleOptions) (*Client, *consoleServer) {
	t.Helper()
	s := &consoleServer{consoleOptions: cfg}
	// The "binary" subprotocol is what the hypervisor announces on the 101 —
	// measured.
	up := websocket.Upgrader{Subprotocols: []string{"binary"}}

	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/termproxy"):
			if r.Method != http.MethodPost {
				t.Errorf("termproxy called with %s, want POST", r.Method)
			}
			if s.statusTermproxy != 0 {
				w.WriteHeader(s.statusTermproxy)
				_, _ = w.Write([]byte(`{"data":null,"errors":{"message":"Permission check failed (/vms/204, VM.Console)"}}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"port":"5900","ticket":"` + fakeConsoleTicket +
				`","user":"` + fakeConsoleUser + `","upid":"` + fakeConsoleUPID + `"}}`))

		case strings.HasSuffix(r.URL.Path, "/vncwebsocket"):
			s.mu.Lock()
			s.upgradeAttempted = true
			s.authHeaderWS = r.Header.Get("Authorization")
			s.mu.Unlock()
			if q := r.URL.Query(); q.Get("port") != "5900" || q.Get("vncticket") != fakeConsoleTicket {
				t.Errorf("vncwebsocket without the right port/vncticket: %q", r.URL.RawQuery)
			}
			c, err := up.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer c.Close()
			_, first, err := c.ReadMessage()
			if err != nil {
				return
			}
			s.mu.Lock()
			s.authReceived = string(first)
			s.mu.Unlock()
			if s.repliesOK {
				_ = c.WriteMessage(websocket.BinaryMessage, []byte("OK"))
			}
			if s.initialOutput != "" {
				_ = c.WriteMessage(websocket.BinaryMessage, []byte(s.initialOutput))
			}
			for {
				_, m, err := c.ReadMessage()
				if err != nil {
					return
				}
				s.mu.Lock()
				s.framesReceived = append(s.framesReceived, string(m))
				s.mu.Unlock()
			}

		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.srv.Close)

	cli, err := New(Config{BaseURL: s.srv.URL, TokenID: testTokenID, Secret: testSecret})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return cli, s
}

// 🔴 TestConsoleAttachDoesThreeSteps is the pin of the happy path, and it
// asserts what the MEASUREMENT saw, not what would be convenient: the auth
// frame is exactly "user:ticket\n", the token header travels ON THE UPGRADE
// (the hypervisor authenticates the handshake with it — measured: a complete
// handshake with PVEAPIToken stopped at the ACL, 403, not at authentication),
// and the UPID comes back to become audit trail.
func TestConsoleAttachDoesThreeSteps(t *testing.T) {
	cli, s := newConsoleServer(t, consoleOptions{repliesOK: true, initialOutput: "lab login: "})

	conn, upid, err := cli.ConsoleAttach(context.Background(), "pve", 204, "lxc")
	if err != nil {
		t.Fatalf("ConsoleAttach: %v", err)
	}
	defer conn.Close()

	authReceived, headerWS, _, _ := s.seen()
	if want := fakeConsoleUser + ":" + fakeConsoleTicket + "\n"; authReceived != want {
		t.Errorf("auth frame = %q, want %q", authReceived, want)
	}
	if !strings.HasPrefix(headerWS, "PVEAPIToken="+testTokenID+"=") {
		t.Errorf("upgrade without the token header (%q) — the PVE authenticates the handshake through it", headerWS)
	}
	if upid != fakeConsoleUPID {
		t.Errorf("upid = %q, want %q — without it the trail does not name the task", upid, fakeConsoleUPID)
	}

	// 🔴 The FIRST message the caller reads is the TERMINAL, never the "OK".
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("first read: %v", err)
	}
	if string(data) == "OK" {
		t.Fatal("the handshake's \"OK\" leaked to the caller — it would write a phantom OK on the console's 1st line")
	}
	if string(data) != "lab login: " {
		t.Errorf("first read = %q, want the terminal output", data)
	}
}

// 🔴 TestConsoleAttachWithoutPrivilegeSkipsUpgrade: a 403 on termproxy has to
// stop right there, typed. Trying the upgrade after a 403 would spend a
// connection to receive another 403 — and the Kind that reaches the screen
// would be the upgrade's, not that of the operation the operator asked for.
func TestConsoleAttachWithoutPrivilegeSkipsUpgrade(t *testing.T) {
	cli, s := newConsoleServer(t, consoleOptions{statusTermproxy: http.StatusForbidden})

	_, _, err := cli.ConsoleAttach(context.Background(), "pve", 204, "lxc")
	if err == nil {
		t.Fatal("a 403 on termproxy was accepted as success")
	}
	var pe *Error
	if !errors.As(err, &pe) || pe.Kind != KindForbidden {
		t.Fatalf("error = %v (%T), want KindForbidden", err, err)
	}
	if _, _, _, upgrade := s.seen(); upgrade {
		t.Error("there was an upgrade after the 403 — the error reaching the screen would no longer be the one from the requested operation")
	}
}

// 🔴 TestConsoleAttachRequiresHandshakeOK: without the "OK", what exists is an
// open socket that will never deliver any terminal at all. Handing it back
// would make the screen spin forever instead of saying what happened — the
// symptom the operator reported on CT 204's vncproxy.
func TestConsoleAttachRequiresHandshakeOK(t *testing.T) {
	cli, _ := newConsoleServer(t, consoleOptions{repliesOK: false, initialOutput: "ERROR"})

	conn, _, err := cli.ConsoleAttach(context.Background(), "pve", 204, "lxc")
	if err == nil {
		conn.Close()
		t.Fatal("a handshake with no \"OK\" was accepted — the screen would spin forever")
	}
	if !strings.Contains(err.Error(), "OK") {
		t.Errorf("error = %q, it has to say the handshake did not confirm", err)
	}
}

// TestConsoleAttachRejectsInvalidType: the type comes from the inventory ID
// ("lxc/204"); a value outside lxc/qemu would build a path that does not exist
// and spend a call to take a 501.
func TestConsoleAttachRejectsInvalidType(t *testing.T) {
	cli, s := newConsoleServer(t, consoleOptions{repliesOK: true})
	if _, _, err := cli.ConsoleAttach(context.Background(), "pve", 204, "container"); err == nil {
		t.Fatal("type \"container\" was accepted")
	}
	if _, _, _, upgrade := s.seen(); upgrade {
		t.Error("there was an upgrade with an invalid type")
	}
}

// 🔴 TestConsoleErrorLeaksNoTicketOrSecret. The ticket is a console credential:
// whoever holds it opens a shell. It cannot show up in an error, which turns
// into screen text and a log line.
func TestConsoleErrorLeaksNoTicketOrSecret(t *testing.T) {
	cli, _ := newConsoleServer(t, consoleOptions{repliesOK: false, initialOutput: "something that is not OK"})
	_, _, err := cli.ConsoleAttach(context.Background(), "pve", 204, "lxc")
	if err == nil {
		t.Fatal("expected an error")
	}
	msg := err.Error()
	if strings.Contains(msg, fakeConsoleTicket) {
		t.Error("the error carries the TICKET — that is a console credential")
	}
	if strings.Contains(msg, testSecret) {
		t.Error("the error carries the token secret")
	}
}

// 🔴 TestInputFrameCountsBYTES is the trap measured live: termproxy reads N
// BYTES after "0:N:". Measured against CT 204:
//
//	"0:2:ñ" (2 bytes) → the terminal received 0xC3 0xB1  ✅
//	"0:1:ñ" (1 char)  → the terminal received 0xC3       ❌ half a character
//
// That is why the translation lives on the SERVER: `data.length` in JavaScript
// counts UTF-16 units, so an "ñ" typed in the browser would arrive cut in half.
func TestInputFrameCountsBYTES(t *testing.T) {
	cases := []struct {
		name string
		text string
		want string
	}{
		{"ascii", "ls\n", "0:3:ls\n"},
		{"non-ascii", "ñ", "0:2:ñ"},
		{"emoji", "🔴", "0:4:🔴"},
		{"empty", "", "0:0:"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(InputFrame([]byte(tc.text))); got != tc.want {
				t.Errorf("InputFrame(%q) = %q, want %q", tc.text, got, tc.want)
			}
		})
	}
}

// TestResizeAndKeepaliveFrames pins the protocol's other two verbs, taken from
// /usr/share/pve-xtermjs/main.js and confirmed in the measurement (the CT
// accepted both without closing the connection).
func TestResizeAndKeepaliveFrames(t *testing.T) {
	if got := string(ResizeFrame(80, 24)); got != "1:80:24:" {
		t.Errorf("ResizeFrame(80,24) = %q, want \"1:80:24:\"", got)
	}
	if got := string(KeepaliveFrame()); got != "2" {
		t.Errorf("KeepaliveFrame() = %q, want \"2\"", got)
	}
}

// 🔴 TestResizeFrameRejectsAbsurdSize: cols/rows come from the browser. A
// negative or gigantic value would build a frame termproxy cannot read, and the
// symptom would be the connection dying with no explanation.
func TestResizeFrameRejectsAbsurdSize(t *testing.T) {
	for _, tc := range []struct{ cols, rows int }{{0, 24}, {80, 0}, {-1, 24}, {80, -1}, {100000, 24}, {80, 100000}} {
		if f := ResizeFrame(tc.cols, tc.rows); f != nil {
			t.Errorf("ResizeFrame(%d,%d) = %q, want nil (dimension out of range)", tc.cols, tc.rows, f)
		}
	}
}

// TestConsoleTranslatesFramesToHypervisor closes the loop: the frames built
// here reach the server exactly as they left.
func TestConsoleTranslatesFramesToHypervisor(t *testing.T) {
	cli, s := newConsoleServer(t, consoleOptions{repliesOK: true})
	conn, _, err := cli.ConsoleAttach(context.Background(), "pve", 204, "lxc")
	if err != nil {
		t.Fatalf("ConsoleAttach: %v", err)
	}
	for _, f := range [][]byte{ResizeFrame(120, 40), InputFrame([]byte("ñ\n")), KeepaliveFrame()} {
		if err := conn.WriteMessage(websocket.BinaryMessage, f); err != nil {
			t.Fatalf("writing %q: %v", f, err)
		}
	}
	conn.Close()

	// ACTIVE wait for an event (the 3 frames arriving), with a deadline — it is
	// not a clock wait: what is awaited is the server, and the test dies in 3 s.
	var frames []string
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, _, frames, _ = s.seen(); len(frames) >= 3 || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	want := []string{"1:120:40:", "0:3:ñ\n", "2"}
	if len(frames) < 3 {
		t.Fatalf("frames received = %q, want %q", frames, want)
	}
	for i := range want {
		if frames[i] != want[i] {
			t.Errorf("frame %d = %q, want %q", i, frames[i], want[i])
		}
	}
}
