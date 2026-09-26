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

// wsTestServer wires HandleMobileEventsWS behind a real httptest.Server (a
// WS upgrade needs a genuine net.Conn, not an httptest.ResponseRecorder)
// and returns the ws:// base URL plus the shared Hub.
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

// wsReader pumps one WS connection's incoming JSON frames onto a channel
// from a single dedicated goroutine. gorilla/websocket's Conn permanently
// poisons itself after ANY read error — including a deliberate
// SetReadDeadline timeout used to assert "no message arrived" (see
// (*Conn).NextReader / hideTempErr: the first read error, timeout or not,
// is cached and replayed on every later read). Asserting "no message
// within N ms" therefore can never be done by arming a per-call deadline
// on the conn and reading again afterward — the connection would be
// unusable for the rest of the test. Funneling all reads through one
// long-lived goroutine and a channel lets "expect nothing for N ms" and
// "expect a message" both be plain channel selects, with the real
// gorilla Conn read loop never interrupted by an artificial timeout.
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
		// channel closed = connection closed, which trivially means "no
		// message was delivered on it either" — not a failure here.
	case <-time.After(timeout):
	}
}

// dialWithTicket mints a fresh one-shot ticket for user and dials
// /ws/mobile-events with it, matching the real client's "mint immediately
// before connecting, never reuse" flow from ARCHITECTURE.md.
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

// TestMobileEventsWS_SubscribeAckAndPublish is Task 1's literal <done>
// criterion: subscribe gets acked, and a second connection's Hub.Publish
// reaches the first.
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

	// The ack is only written AFTER hc.subscribe() runs server-side
	// (events_ws.go's read loop), so having read it back here IS the sync
	// point proving the subscription is registered — no sleep needed.
	hub.Publish("x", Envelope{Type: "t", Data: json.RawMessage(`{"pct":42}`)}, nil)

	ev := rd.next(t, time.Second)
	if ev["channel"] != "x" || ev["type"] != "t" {
		t.Fatalf("event = %#v, want channel=x type=t", ev)
	}
	if data, _ := ev["data"].(map[string]any); data == nil || data["pct"] != float64(42) {
		t.Fatalf("event data = %#v, want {pct:42}", ev["data"])
	}
}

// TestMobileEventsWS_ChannelIsolation proves the hub does not fan every
// event to every client: a connection subscribed only to "a" must never
// see an event published on "b". This is the property behind per-screen
// subscription isolation — each mobile screen subscribes only to the
// channel(s) it renders.
func TestMobileEventsWS_ChannelIsolation(t *testing.T) {
	baseURL, hub, _ := wsTestServer(t)
	connA, rdA := dialWithTicket(t, baseURL, "sam")
	connB, rdB := dialWithTicket(t, baseURL, "sam")

	if err := connA.WriteJSON(controlFrame{Op: "subscribe", Channel: "a"}); err != nil {
		t.Fatalf("subscribe a: %v", err)
	}
	rdA.next(t, time.Second) // ack

	if err := connB.WriteJSON(controlFrame{Op: "subscribe", Channel: "b"}); err != nil {
		t.Fatalf("subscribe b: %v", err)
	}
	rdB.next(t, time.Second) // ack

	hub.Publish("a", Envelope{Type: "only-a"}, nil)

	ev := rdA.next(t, time.Second)
	if ev["channel"] != "a" || ev["type"] != "only-a" {
		t.Fatalf("connA event = %#v", ev)
	}
	rdB.expectNone(t, 200*time.Millisecond)
}

// TestMobileEventsWS_ReconnectDoesNotDuplicate: the hub keeps no
// per-channel history, so an event published while a client is
// disconnected is simply never delivered to it — a reconnect that
// resubscribes therefore cannot ever replay/duplicate an event it (or its
// predecessor connection) already received.
func TestMobileEventsWS_ReconnectDoesNotDuplicate(t *testing.T) {
	baseURL, hub, _ := wsTestServer(t)

	conn1, rd1 := dialWithTicket(t, baseURL, "sam")
	if err := conn1.WriteJSON(controlFrame{Op: "subscribe", Channel: "notify.inbox"}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	rd1.next(t, time.Second) // ack

	hub.Publish("notify.inbox", Envelope{Type: "job.done", Data: json.RawMessage(`{"id":"1"}`)}, nil)
	first := rd1.next(t, time.Second)
	if first["type"] != "job.done" {
		t.Fatalf("first event = %#v", first)
	}

	// Disconnect (simulates the client backgrounding/losing network).
	_ = conn1.Close()
	// A second event fires while nobody is connected — must not be queued
	// anywhere for future delivery.
	hub.Publish("notify.inbox", Envelope{Type: "job.failed", Data: json.RawMessage(`{"id":"2"}`)}, nil)

	// Reconnect: mints a FRESH ticket (never reuses the consumed one) and
	// resubscribes, per ARCHITECTURE.md's reconnect state machine.
	conn2, rd2 := dialWithTicket(t, baseURL, "sam")
	if err := conn2.WriteJSON(controlFrame{Op: "subscribe", Channel: "notify.inbox"}); err != nil {
		t.Fatalf("resubscribe: %v", err)
	}
	ack := rd2.next(t, time.Second)
	if ack["op"] != "subscribed" {
		t.Fatalf("resubscribe ack = %#v", ack)
	}

	// The "job.failed" event published while disconnected must NOT show up
	// now (no replay) — only a NEW publish after resubscribing is seen, and
	// exactly once.
	rd2.expectNone(t, 200*time.Millisecond)

	hub.Publish("notify.inbox", Envelope{Type: "job.cancelled", Data: json.RawMessage(`{"id":"3"}`)}, nil)
	third := rd2.next(t, time.Second)
	if third["type"] != "job.cancelled" {
		t.Fatalf("third event = %#v, want job.cancelled (not a duplicate of job.done/job.failed)", third)
	}
	rd2.expectNone(t, 200*time.Millisecond)
}

// TestMobileEventsWS_DisconnectCleansUpSubscription proves the server
// survives an abrupt client close and actually removes the connection
// from the Hub (not just stops delivering — the registry entry itself
// goes away), so a long-lived process never accumulates dead sockets.
func TestMobileEventsWS_DisconnectCleansUpSubscription(t *testing.T) {
	baseURL, hub, _ := wsTestServer(t)
	conn, rd := dialWithTicket(t, baseURL, "sam")
	if err := conn.WriteJSON(controlFrame{Op: "subscribe", Channel: "x"}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	rd.next(t, time.Second) // ack

	if got := hub.Len(); got != 1 {
		t.Fatalf("hub.Len() = %d, want 1 before disconnect", got)
	}

	// Abrupt close: no WS close handshake, just drop the TCP connection —
	// the server's ReadMessage must error out and unregister.
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
