package pty

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// This test brings up a REAL websocket and checks the close code that reaches the
// client. It is the only honest way to prove the fix: the promise here is not
// "the function was called", it is "the browser learns it was a restart".
//
// Without the notice, the process dies and the hijacked connection vanishes with
// no close frame — the browser reports 1006 (abnormal closure), identical to a
// network drop.
func TestNotifyRestartEntregaCodigo1012(t *testing.T) {
	pronto := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		desregistra := registerLive(c)
		defer desregistra()
		close(pronto)
		// Hold the connection open until the test has finished reading.
		time.Sleep(2 * time.Second)
	}))
	defer srv.Close()

	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	cli, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer cli.Close()
	<-pronto

	if n := LiveCount(); n != 1 {
		t.Fatalf("LiveCount = %d, wanted 1 (connection was not registered)", n)
	}

	visto := make(chan int, 1)
	cli.SetCloseHandler(func(code int, text string) error { visto <- code; return nil })

	if n := NotifyRestart(); n != 1 {
		t.Fatalf("NotifyRestart notified %d connections, wanted 1", n)
	}
	// The close arrives on the next read.
	_ = cli.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, _ = cli.ReadMessage()

	select {
	case code := <-visto:
		if code != websocket.CloseServiceRestart {
			t.Fatalf("the client got close %d, want 1012 (Service Restart)", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("client did not receive a close frame — it would fall into 1006 and read the deploy as a network failure")
	}
}

// A leaked registration would hold the connection alive in memory and make the
// notice write into a dead socket on every deploy after that.
func TestRegisterLiveDesregistra(t *testing.T) {
	antes := LiveCount()
	c := &websocket.Conn{}
	fim := registerLive(c)
	if LiveCount() != antes+1 {
		t.Fatalf("registration was not counted")
	}
	fim()
	if LiveCount() != antes {
		t.Fatalf("LiveCount = %d after unregistering, wanted %d", LiveCount(), antes)
	}
	// Idempotent: a double defer must not break the count.
	fim()
	if LiveCount() != antes {
		t.Fatalf("duplicate unregister messed up the count: %d", LiveCount())
	}
}

// nil must not take down the shutdown path — the deploy has to happen.
func TestRegisterLiveNilNaoQuebra(t *testing.T) {
	antes := LiveCount()
	fim := registerLive(nil)
	fim()
	if LiveCount() != antes {
		t.Fatalf("nil conn touched the count")
	}
}

// With nobody connected, the notice is a silent no-op.
func TestNotifyRestartSemConexoes(t *testing.T) {
	liveMu.Lock()
	liveConns = map[*websocket.Conn]struct{}{}
	liveMu.Unlock()
	if n := NotifyRestart(); n != 0 {
		t.Fatalf("NotifyRestart = %d with no connections, wanted 0", n)
	}
}
