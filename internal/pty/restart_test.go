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
func TestNotifyRestartDeliversCode1012(t *testing.T) {
	ready := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		unregister := registerLive(c)
		defer unregister()
		close(ready)
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
	<-ready

	if n := LiveCount(); n != 1 {
		t.Fatalf("LiveCount = %d, wanted 1 (connection was not registered)", n)
	}

	seen := make(chan int, 1)
	cli.SetCloseHandler(func(code int, text string) error { seen <- code; return nil })

	if n := NotifyRestart(); n != 1 {
		t.Fatalf("NotifyRestart notified %d connections, wanted 1", n)
	}
	// The close arrives on the next read.
	_ = cli.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, _ = cli.ReadMessage()

	select {
	case code := <-seen:
		if code != websocket.CloseServiceRestart {
			t.Fatalf("the client got close %d, want 1012 (Service Restart)", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("client did not receive a close frame — it would fall into 1006 and read the deploy as a network failure")
	}
}

// A leaked registration would hold the connection alive in memory and make the
// notice write into a dead socket on every deploy after that.
func TestRegisterLiveUnregisters(t *testing.T) {
	before := LiveCount()
	c := &websocket.Conn{}
	done := registerLive(c)
	if LiveCount() != before+1 {
		t.Fatalf("registration was not counted")
	}
	done()
	if LiveCount() != before {
		t.Fatalf("LiveCount = %d after unregistering, wanted %d", LiveCount(), before)
	}
	// Idempotent: a double defer must not break the count.
	done()
	if LiveCount() != before {
		t.Fatalf("duplicate unregister messed up the count: %d", LiveCount())
	}
}

// nil must not take down the shutdown path — the deploy has to happen.
func TestRegisterLiveNilDoesNotBreak(t *testing.T) {
	before := LiveCount()
	done := registerLive(nil)
	done()
	if LiveCount() != before {
		t.Fatalf("nil conn touched the count")
	}
}

// With nobody connected, the notice is a silent no-op.
func TestNotifyRestartNoConnections(t *testing.T) {
	liveMu.Lock()
	liveConns = map[*websocket.Conn]struct{}{}
	liveMu.Unlock()
	if n := NotifyRestart(); n != 0 {
		t.Fatalf("NotifyRestart = %d with no connections, wanted 0", n)
	}
}
