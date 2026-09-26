package notify

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"server-control-panel/internal/webpush"
)

// newTestWebpushStore opens a real *webpush.Store rooted in a t.TempDir(), so
// TestWebpushSender_TranslatesSendOptions exercises the actual adapter
// boundary (webpush-go types, VAPID keygen) instead of a fake.
func newTestWebpushStore(t *testing.T) *webpush.Store {
	t.Helper()
	store, err := webpush.Open(filepath.Join(t.TempDir(), "webpush"))
	if err != nil {
		t.Fatalf("webpush.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// fakePushSender is a pushSender test double that records the last call. It
// never imports internal/webpush — proving the interface it satisfies is
// notify-owned, not Web-Push vocabulary.
type fakePushSender struct {
	lastPayload []byte
	lastOpts    SendOptions
	lastUser    string
	sendToAll   bool
	sendToUser  bool
	delivered   int
}

func (f *fakePushSender) SendToUser(ctx context.Context, user string, payload []byte, opts SendOptions, allowDevice func(string) bool) int {
	f.sendToUser = true
	f.lastUser = user
	f.lastPayload = payload
	f.lastOpts = opts
	return f.delivered
}

func (f *fakePushSender) SendToAll(ctx context.Context, payload []byte, opts SendOptions, allowDevice func(string) bool) int {
	f.sendToAll = true
	f.lastPayload = payload
	f.lastOpts = opts
	return f.delivered
}

func TestPushChannel_MetricEvent_BuildsAlertFiredPayload(t *testing.T) {
	fake := &fakePushSender{}
	c := NewPushChannel(fake, nil)
	ev := Event{
		Type:   TypeMetricThreshold,
		Title:  "Metric: cpu",
		Body:   "valor 95.00 cruzou o limite",
		Labels: map[string]string{"rule": "cpu"},
	}
	if err := c.Send(context.Background(), ev, ChannelConfig{}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !fake.sendToAll || fake.sendToUser {
		t.Fatalf("expected SendToAll (ToUser empty), got sendToAll=%v sendToUser=%v", fake.sendToAll, fake.sendToUser)
	}
	var payload map[string]any
	if err := json.Unmarshal(fake.lastPayload, &payload); err != nil {
		t.Fatalf("payload not valid JSON: %v", err)
	}
	if payload["type"] != "alert-fired" {
		t.Fatalf("type = %v, want alert-fired", payload["type"])
	}
	if payload["rule"] != "cpu" {
		t.Fatalf("rule = %v, want cpu", payload["rule"])
	}
	if payload["body"] != "valor 95.00 cruzou o limite" {
		t.Fatalf("body = %v", payload["body"])
	}
}

func TestPushChannel_JobEvent_BuildsGenericPayload(t *testing.T) {
	fake := &fakePushSender{}
	c := NewPushChannel(fake, nil)
	ev := Event{
		Type:  "job.failed",
		Title: "Job falhou: shell",
		Body:  "exit 1",
	}
	if err := c.Send(context.Background(), ev, ChannelConfig{}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(fake.lastPayload, &payload); err != nil {
		t.Fatalf("payload not valid JSON: %v", err)
	}
	if _, hasType := payload["type"]; hasType {
		t.Fatalf("generic payload must not have a \"type\" field, got %v", payload["type"])
	}
	if payload["title"] != "Job falhou: shell" {
		t.Fatalf("title = %v", payload["title"])
	}
	if payload["body"] != "exit 1" {
		t.Fatalf("body = %v", payload["body"])
	}
	if payload["tag"] == nil || payload["tag"] == "" {
		t.Fatalf("expected a non-empty tag derived from event type, got %v", payload["tag"])
	}
}

func TestPushChannel_ToUser_RoutesToSendToUser(t *testing.T) {
	fake := &fakePushSender{}
	c := NewPushChannel(fake, nil)
	ev := Event{Type: "job.done", Title: "ok", Body: "ok"}
	if err := c.Send(context.Background(), ev, ChannelConfig{ToUser: "sam"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !fake.sendToUser || fake.sendToAll {
		t.Fatalf("expected SendToUser only, got sendToAll=%v sendToUser=%v", fake.sendToAll, fake.sendToUser)
	}
	if fake.lastUser != "sam" {
		t.Fatalf("user = %q, want sam", fake.lastUser)
	}
}

func TestPushChannel_CriticalSeverity_SetsCritical(t *testing.T) {
	fake := &fakePushSender{}
	c := NewPushChannel(fake, nil)
	ev := Event{Type: "job.failed", Title: "x", Body: "y", Severity: SeverityCritical}
	if err := c.Send(context.Background(), ev, ChannelConfig{}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !fake.lastOpts.Critical {
		t.Fatalf("expected SendOptions.Critical = true for a critical event")
	}
}

func TestPushChannel_NilSender_ReturnsError(t *testing.T) {
	c := NewPushChannel(nil, nil)
	err := c.Send(context.Background(), Event{Type: "job.done"}, ChannelConfig{})
	if err == nil {
		t.Fatalf("expected a non-nil error for a nil sender")
	}
}

func TestPushChannel_ZeroDeliveries_IsNotAnError(t *testing.T) {
	fake := &fakePushSender{delivered: 0}
	c := NewPushChannel(fake, nil)
	ev := Event{Type: "job.done", Title: "x", Body: "y"}
	if err := c.Send(context.Background(), ev, ChannelConfig{}); err != nil {
		t.Fatalf("zero subscribers must not be a channel error, got %v", err)
	}
}

func TestWebpushSender_TranslatesSendOptions(t *testing.T) {
	store := newTestWebpushStore(t)
	sender := NewWebpushSender(store)
	n := sender.SendToAll(context.Background(), []byte(`{"title":"x"}`), SendOptions{TTL: 300, Critical: true}, nil)
	if n != 0 {
		t.Fatalf("expected 0 deliveries against an empty store, got %d", n)
	}
}
