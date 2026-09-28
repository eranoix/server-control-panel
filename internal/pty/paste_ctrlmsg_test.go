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

func TestPasteCtrlMsgWritesBracketedInSingleWrite(t *testing.T) {
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

func TestPasteCtrlMsgContentWithQuotesAndSlashes(t *testing.T) {
	rwc := newFakeRWC()
	cli := dialProxy(t, rwc)

	payload := "she said \"hi\" and sent C:\\Users\\ana\\notes.txt as a bonus\nbye"
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

func TestPasteCtrlMsgUnknownTypeDoesNotWrite(t *testing.T) {
	rwc := newFakeRWC()
	cli := dialProxy(t, rwc)

	bogus, _ := json.Marshal(struct {
		Type string `json:"type"`
		Data string `json:"data"`
	}{Type: "bogus", Data: "this must not reach the pty"})
	if err := cli.WriteMessage(websocket.TextMessage, bogus); err != nil {
		t.Fatalf("write bogus: %v", err)
	}

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
