package pty

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

const (
	bracketedPasteStart = "\x1b[200~"
	bracketedPasteEnd   = "\x1b[201~"
)

// fakeRWC is the io.ReadWriteCloser proxy() sees in place of the real PTY.
// Read blocks until Close (the proxy's PTY->WS pump only advances when there is
// data or a close) — so the only observable activity in the test is what WS->PTY
// writes, which is exactly the path this test is verifying.
type fakeRWC struct {
	mu      sync.Mutex
	writes  [][]byte
	wrote   chan struct{}
	closeCh chan struct{}
	once    sync.Once
}

func newFakeRWC() *fakeRWC {
	return &fakeRWC{wrote: make(chan struct{}, 64), closeCh: make(chan struct{})}
}

func (f *fakeRWC) Read(p []byte) (int, error) {
	<-f.closeCh
	return 0, io.EOF
}

func (f *fakeRWC) Write(p []byte) (int, error) {
	cp := append([]byte(nil), p...)
	f.mu.Lock()
	f.writes = append(f.writes, cp)
	f.mu.Unlock()
	select {
	case f.wrote <- struct{}{}:
	default:
	}
	return len(p), nil
}

func (f *fakeRWC) Close() error {
	f.once.Do(func() { close(f.closeCh) })
	return nil
}

func (f *fakeRWC) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.writes)
}

func (f *fakeRWC) last() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.writes) == 0 {
		return nil
	}
	return f.writes[len(f.writes)-1]
}

func waitForWriteCount(t *testing.T, f *fakeRWC, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		if f.count() >= want {
			return
		}
		select {
		case <-f.wrote:
		case <-deadline:
			t.Fatalf("timeout waiting for %d writes on fakeRWC, had %d", want, f.count())
		}
	}
}

// dialProxy brings up an httptest.Server that does a real Upgrade and calls the
// production proxy() with the fakeRWC in place of the PTY, exactly as
// restart_test.go does for NotifyRestart. It is the only honest way to prove
// that the "paste" frame is parsed on the real WS->PTY path, and not in a
// stand-in function the test calls directly.
func dialProxy(t *testing.T, rwc *fakeRWC) *websocket.Conn {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		proxy(c, rwc, nil, nil, nil, nil, nil)
	}))
	t.Cleanup(srv.Close)

	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	cli, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = cli.Close() })
	return cli
}

// A {"type":"paste","data":"..."} sent by a real WS client reaches the PTY
// as the bytes ESC[200~<data>ESC[201~ in a SINGLE Write — not two, not the raw
// data without the envelope, and not silently nothing (the original bug: there
// was no "case paste" at all and the frame was dropped in the empty fallthrough).
func TestPasteCtrlMsgEscreveBracketedEmUmaUnicaWrite(t *testing.T) {
	rwc := newFakeRWC()
	cli := dialProxy(t, rwc)

	payload := "line one\nline two\n"
	frame, err := json.Marshal(ctrlMsg{Type: "paste", Data: payload})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := cli.WriteMessage(websocket.TextMessage, frame); err != nil {
		t.Fatalf("write: %v", err)
	}

	waitForWriteCount(t, rwc, 1, 2*time.Second)

	want := bracketedPasteStart + payload + bracketedPasteEnd
	if got := string(rwc.last()); got != want {
		t.Fatalf("paste write = %q, want %q", got, want)
	}
	if n := rwc.count(); n != 1 {
		t.Fatalf("proxy made %d writes for the paste, wanted exactly 1 (otherwise it loses atomicity and can interleave with concurrent output)", n)
	}
}

// A real clipboard has quotes, backslashes, accents — not just the trivial
// "a\nb". The round trip through encoding/json (marshal on the client,
// unmarshal into the server's ctrlMsg) has to preserve the literal content
// inside the envelope, without escaping/unescaping it wrongly.
func TestPasteCtrlMsgConteudoComAspasEBarras(t *testing.T) {
	rwc := newFakeRWC()
	cli := dialProxy(t, rwc)

	payload := "ela disse \"oi\" e mandou C:\\Users\\ana\\notas.txt de brinde\ntchau"
	frame, err := json.Marshal(ctrlMsg{Type: "paste", Data: payload})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := cli.WriteMessage(websocket.TextMessage, frame); err != nil {
		t.Fatalf("write: %v", err)
	}

	waitForWriteCount(t, rwc, 1, 2*time.Second)

	want := bracketedPasteStart + payload + bracketedPasteEnd
	if got := string(rwc.last()); got != want {
		t.Fatalf("paste with quotes/slashes = %q, wanted %q", got, want)
	}
}

// The invariant that already existed (the comment in proxy(): valid JSON, with a
// known type or not, NEVER falls through to the raw Write) has to keep holding
// after "paste" joins the switch — otherwise adding the new case would
// accidentally widen the fallthrough to ANY unknown type.
func TestPasteCtrlMsgTipoDesconhecidoNaoEscreve(t *testing.T) {
	rwc := newFakeRWC()
	cli := dialProxy(t, rwc)

	bogus, _ := json.Marshal(struct {
		Type string `json:"type"`
		Data string `json:"data"`
	}{Type: "bogus", Data: "isto não pode chegar ao pty"})
	if err := cli.WriteMessage(websocket.TextMessage, bogus); err != nil {
		t.Fatalf("write bogus: %v", err)
	}

	// proxy() processes messages in order, one at a time, on the same goroutine
	// (WS->PTY is the main loop). Sending a "ping" AFTERWARDS and waiting for the
	// binary pong back proves the "bogus" one was fully processed on the server
	// side before we check the count — with no sleep needed.
	ping, _ := json.Marshal(ctrlMsg{Type: "ping"})
	if err := cli.WriteMessage(websocket.TextMessage, ping); err != nil {
		t.Fatalf("write ping: %v", err)
	}
	_ = cli.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := cli.ReadMessage(); err != nil {
		t.Fatalf("expected the binary pong for the ping (proof the bogus one was already processed), got error: %v", err)
	}

	if n := rwc.count(); n != 0 {
		t.Fatalf("unknown type leaked into the pty's Write: %d writes, wanted 0", n)
	}
}
