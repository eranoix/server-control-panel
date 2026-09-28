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

const (
	fakeConsoleTicket = "PVEVNC:AAAAAA==::ticket-measured-at-361-bytes"
	fakeConsoleUser   = "panel@pve!node-lab"
	fakeConsoleUPID   = "UPID:pve:003D8997:0492E623:6A868978:vncproxy:204:panel@pve!node-lab:"
)

type consoleServer struct {
	srv *httptest.Server

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

type consoleOptions struct {
	statusTermproxy int
	repliesOK       bool
	initialOutput   string
}

func newConsoleServer(t *testing.T, cfg consoleOptions) (*Client, *consoleServer) {
	t.Helper()
	s := &consoleServer{consoleOptions: cfg}
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

func TestConsoleAttachRejectsInvalidType(t *testing.T) {
	cli, s := newConsoleServer(t, consoleOptions{repliesOK: true})
	if _, _, err := cli.ConsoleAttach(context.Background(), "pve", 204, "container"); err == nil {
		t.Fatal("type \"container\" was accepted")
	}
	if _, _, _, upgrade := s.seen(); upgrade {
		t.Error("there was an upgrade with an invalid type")
	}
}

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

func TestResizeAndKeepaliveFrames(t *testing.T) {
	if got := string(ResizeFrame(80, 24)); got != "1:80:24:" {
		t.Errorf("ResizeFrame(80,24) = %q, want \"1:80:24:\"", got)
	}
	if got := string(KeepaliveFrame()); got != "2" {
		t.Errorf("KeepaliveFrame() = %q, want \"2\"", got)
	}
}

func TestResizeFrameRejectsAbsurdSize(t *testing.T) {
	for _, tc := range []struct{ cols, rows int }{{0, 24}, {80, 0}, {-1, 24}, {80, -1}, {100000, 24}, {80, 100000}} {
		if f := ResizeFrame(tc.cols, tc.rows); f != nil {
			t.Errorf("ResizeFrame(%d,%d) = %q, want nil (dimension out of range)", tc.cols, tc.rows, f)
		}
	}
}

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
