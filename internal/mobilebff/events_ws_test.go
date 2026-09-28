package mobilebff

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"server-control-panel/internal/auth"
)

func wsTestServer(t *testing.T) (baseURL string, hub *Hub, authSvc *auth.Service) {
	t.Helper()
	hub = NewHub()
	authSvc = auth.New("test-secret", nil)
	mux := http.NewServeMux()
	mux.HandleFunc("/ws/mobile-events", HandleMobileEventsWS(authSvc, hub))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http"), hub, authSvc
}

type wsReader struct {
	msgs chan map[string]any
}

func newWSReader(conn *websocket.Conn) *wsReader {
	r := &wsReader{msgs: make(chan map[string]any, 16)}
	go func() {
		defer close(r.msgs)
		for {
			var v map[string]any
			if err := conn.ReadJSON(&v); err != nil {
				return
			}
			r.msgs <- v
		}
	}()
	return r
}

func (r *wsReader) next(t *testing.T, timeout time.Duration) map[string]any {
	t.Helper()
	select {
	case v, ok := <-r.msgs:
		if !ok {
			t.Fatalf("connection closed while waiting for a message")
		}
		return v
	case <-time.After(timeout):
		t.Fatalf("timed out waiting for a message")
		return nil
	}
}

func (r *wsReader) expectNone(t *testing.T, timeout time.Duration) {
	t.Helper()
	select {
	case v, ok := <-r.msgs:
		if ok {
			t.Fatalf("expected no message, got: %#v", v)
		}
	case <-time.After(timeout):
	}
}

func dialWithTicket(t *testing.T, baseURL, user string) (*websocket.Conn, *wsReader) {
	t.Helper()
	ticket := auth.IssueWSTicket(user, "jti-"+user)
	conn, resp, err := websocket.DefaultDialer.Dial(baseURL+"/ws/mobile-events?ticket="+ticket, nil)
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("dial: %v (status=%d)", err, status)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn, newWSReader(conn)
}

func TestMobileEventsWS_Unauthenticated(t *testing.T) {
	baseURL, _, _ := wsTestServer(t)
	_, resp, err := websocket.DefaultDialer.Dial(baseURL+"/ws/mobile-events", nil)
	if err == nil {
		t.Fatalf("expected dial to fail without a ticket/token")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("status = %d, want 401", status)
	}
}

func TestMobileEventsWS_SubscribeAckAndPublish(t *testing.T) {
	baseURL, hub, _ := wsTestServer(t)
	conn, rd := dialWithTicket(t, baseURL, "sam")

	if err := conn.WriteJSON(controlFrame{Op: "subscribe", Channel: "x"}); err != nil {
		t.Fatalf("WriteJSON subscribe: %v", err)
	}
	ack := rd.next(t, time.Second)
	if ack["op"] != "subscribed" || ack["channel"] != "x" {
		t.Fatalf("ack = %#v, want {op:subscribed channel:x}", ack)
	}

	hub.Publish("x", Envelope{Type: "t", Data: json.RawMessage(`{"pct":42}`)}, nil)

	ev := rd.next(t, time.Second)
	if ev["channel"] != "x" || ev["type"] != "t" {
		t.Fatalf("event = %#v, want channel=x type=t", ev)
	}
	if data, _ := ev["data"].(map[string]any); data == nil || data["pct"] != float64(42) {
		t.Fatalf("event data = %#v, want {pct:42}", ev["data"])
	}
}

func TestMobileEventsWS_ChannelIsolation(t *testing.T) {
	baseURL, hub, _ := wsTestServer(t)
	connA, rdA := dialWithTicket(t, baseURL, "sam")
	connB, rdB := dialWithTicket(t, baseURL, "sam")

	if err := connA.WriteJSON(controlFrame{Op: "subscribe", Channel: "a"}); err != nil {
		t.Fatalf("subscribe a: %v", err)
	}
	rdA.next(t, time.Second)

	if err := connB.WriteJSON(controlFrame{Op: "subscribe", Channel: "b"}); err != nil {
		t.Fatalf("subscribe b: %v", err)
	}
	rdB.next(t, time.Second)

	hub.Publish("a", Envelope{Type: "only-a"}, nil)

	ev := rdA.next(t, time.Second)
	if ev["channel"] != "a" || ev["type"] != "only-a" {
		t.Fatalf("connA event = %#v", ev)
	}
	rdB.expectNone(t, 200*time.Millisecond)
}

func TestMobileEventsWS_ReconnectDoesNotDuplicate(t *testing.T) {
	baseURL, hub, _ := wsTestServer(t)

	conn1, rd1 := dialWithTicket(t, baseURL, "sam")
	if err := conn1.WriteJSON(controlFrame{Op: "subscribe", Channel: "notify.inbox"}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	rd1.next(t, time.Second)

	hub.Publish("notify.inbox", Envelope{Type: "job.done", Data: json.RawMessage(`{"id":"1"}`)}, nil)
	first := rd1.next(t, time.Second)
	if first["type"] != "job.done" {
		t.Fatalf("first event = %#v", first)
	}

	_ = conn1.Close()
	hub.Publish("notify.inbox", Envelope{Type: "job.failed", Data: json.RawMessage(`{"id":"2"}`)}, nil)

	conn2, rd2 := dialWithTicket(t, baseURL, "sam")
	if err := conn2.WriteJSON(controlFrame{Op: "subscribe", Channel: "notify.inbox"}); err != nil {
		t.Fatalf("resubscribe: %v", err)
	}
	ack := rd2.next(t, time.Second)
	if ack["op"] != "subscribed" {
		t.Fatalf("resubscribe ack = %#v", ack)
	}

	rd2.expectNone(t, 200*time.Millisecond)

	hub.Publish("notify.inbox", Envelope{Type: "job.cancelled", Data: json.RawMessage(`{"id":"3"}`)}, nil)
	third := rd2.next(t, time.Second)
	if third["type"] != "job.cancelled" {
		t.Fatalf("third event = %#v, want job.cancelled (not a duplicate of job.done/job.failed)", third)
	}
	rd2.expectNone(t, 200*time.Millisecond)
}

func TestMobileEventsWS_DisconnectCleansUpSubscription(t *testing.T) {
	baseURL, hub, _ := wsTestServer(t)
	conn, rd := dialWithTicket(t, baseURL, "sam")
	if err := conn.WriteJSON(controlFrame{Op: "subscribe", Channel: "x"}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	rd.next(t, time.Second)

	if got := hub.Len(); got != 1 {
		t.Fatalf("hub.Len() = %d, want 1 before disconnect", got)
	}

	_ = conn.Close()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if hub.Len() == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("hub.Len() = %d, want 0 after disconnect (subscription/connection leaked)", hub.Len())
}
