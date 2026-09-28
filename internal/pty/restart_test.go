package pty

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

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
	done()
	if LiveCount() != before {
		t.Fatalf("duplicate unregister messed up the count: %d", LiveCount())
	}
}

func TestRegisterLiveNilDoesNotBreak(t *testing.T) {
	before := LiveCount()
	done := registerLive(nil)
	done()
	if LiveCount() != before {
		t.Fatalf("nil conn touched the count")
	}
}

func TestNotifyRestartNoConnections(t *testing.T) {
	liveMu.Lock()
	liveConns = map[*websocket.Conn]struct{}{}
	liveMu.Unlock()
	if n := NotifyRestart(); n != 0 {
		t.Fatalf("NotifyRestart = %d with no connections, wanted 0", n)
	}
}
