package api

// notify_mobile_bridge_test.go proves the mobile bridge's real payoff:
// dispatching a notify.Event through the SAME Router.notify a job/
// metric/rule fires reaches a live /ws/mobile-events subscriber on the
// "notify.inbox" channel — not just the in-app inbox poll. Uses the real
// NewRouter (newSmokeRouter) end-to-end: notify.Router -> InAppChannel sink
// -> bridgeNotifyInboxToHub -> mobilebff.Hub -> websocket frame.

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/notify"
)

func TestNotifyDispatch_ReachesMobileEventsWSInboxChannel(t *testing.T) {
	r := newSmokeRouter(t)
	if r.notify == nil {
		t.Fatalf("newSmokeRouter: r.notify is nil, want a live Router")
	}
	if r.mobileHub == nil {
		t.Fatalf("newSmokeRouter: r.mobileHub is nil, want a live Hub")
	}

	// Wire a rule that routes every event to the in-app channel — the same
	// channel initNotify's AddChannelImpl wraps with bridgeNotifyInboxToHub.
	ch, err := r.notify.UpsertChannel(notify.ChannelDef{Name: "inbox", Type: notify.TypeInApp, Enabled: true})
	if err != nil {
		t.Fatalf("UpsertChannel: %v", err)
	}
	if _, err := r.notify.UpsertRule(notify.Rule{Name: "tudo", Enabled: true, Channels: []string{ch.ID}}); err != nil {
		t.Fatalf("UpsertRule: %v", err)
	}

	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	ticket := auth.IssueWSTicket("sam", "jti-bridge-test")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL+"/ws/mobile-events?ticket="+ticket, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Subscribe to notify.inbox; the server's "subscribed" ack is the sync
	// point proving the subscription is registered before Dispatch below
	// (mirrors internal/mobilebff/events_ws_test.go's own pattern).
	if err := conn.WriteJSON(map[string]string{"op": "subscribe", "channel": "notify.inbox"}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	var subAck map[string]any
	if err := conn.ReadJSON(&subAck); err != nil {
		t.Fatalf("subscribe ack: %v", err)
	}
	if subAck["op"] != "subscribed" || subAck["channel"] != "notify.inbox" {
		t.Fatalf("subAck = %#v, want {op:subscribed channel:notify.inbox}", subAck)
	}

	r.notify.Dispatch(notify.Event{
		Type:     "job.done",
		Severity: notify.SeverityInfo,
		Source:   "user",
		Owner:    "sam",
		Title:    "prova de ponte",
		TS:       time.Now().Unix(),
		DedupKey: "bridge-test-1",
	})

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var frame struct {
		Channel string          `json:"channel"`
		Type    string          `json:"type"`
		Data    json.RawMessage `json:"data"`
	}
	if err := conn.ReadJSON(&frame); err != nil {
		t.Fatalf("expected a notify.inbox frame after Dispatch, got error: %v", err)
	}
	if frame.Channel != "notify.inbox" {
		t.Fatalf("frame.Channel = %q, want notify.inbox", frame.Channel)
	}
	var ev notify.Event
	if err := json.Unmarshal(frame.Data, &ev); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}
	if ev.Title != "prova de ponte" || ev.Owner != "sam" {
		t.Fatalf("event = %#v, want the dispatched job.done event", ev)
	}
}
