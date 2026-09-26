package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestChannelsFailClosed(t *testing.T) {
	ctx := context.Background()
	ev := Event{Type: "x", Title: "t"}
	cases := []struct {
		name string
		ch   Channel
		cfg  ChannelConfig
	}{
		{"telegram-no-token", NewTelegramChannel(), ChannelConfig{ChatID: "1"}},
		{"telegram-no-chat", NewTelegramChannel(), ChannelConfig{BotToken: "x"}},
		{"email-no-host", NewEmailChannel(), ChannelConfig{From: "a@b.c", To: "d@e.f"}},
		{"email-no-to", NewEmailChannel(), ChannelConfig{SMTPHost: "h", From: "a@b.c"}},
		{"webhook-no-url", NewWebhookChannel(), ChannelConfig{}},
		{"webhook-bad-scheme", NewWebhookChannel(), ChannelConfig{URL: "file:///etc/passwd"}},
	}
	for _, c := range cases {
		if err := c.ch.Send(ctx, ev, c.cfg); err == nil {
			t.Errorf("%s: expected error, got nil", c.name)
		}
	}
}

func TestInAppChannelPushesToSink(t *testing.T) {
	var got []Event
	ch := NewInAppChannel(func(e Event) { got = append(got, e) })
	if ch.Name() != TypeInApp {
		t.Fatalf("name=%q", ch.Name())
	}
	if err := ch.Send(context.Background(), Event{Type: "job.failed", Title: "x"}, ChannelConfig{}); err != nil {
		t.Fatalf("send: %v", err)
	}
	if len(got) != 1 || got[0].Type != "job.failed" {
		t.Fatalf("sink not called: %+v", got)
	}
	// nil sink must not panic.
	if err := (&InAppChannel{}).Send(context.Background(), Event{}, ChannelConfig{}); err != nil {
		t.Fatalf("nil sink: %v", err)
	}
}

func TestInboxFlowThroughRouter(t *testing.T) {
	rt, err := New(Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	done := make(chan Event, 8)
	rt.onHandled = func(e Event) { done <- e }
	rt.AddChannelImpl(TypeInApp, NewInAppChannel(rt.InboxAdd))

	ch, _ := rt.UpsertChannel(ChannelDef{Name: "site", Type: TypeInApp, Enabled: true})
	rt.UpsertRule(Rule{Name: "all", Enabled: true, Channels: []string{ch.ID}})

	rt.Dispatch(Event{Type: "job.failed", Severity: SeverityCritical, Title: "Failed", DedupKey: "job:1"})
	<-done
	box := rt.Inbox(10)
	if len(box) != 1 || box[0].Title != "Failed" {
		t.Fatalf("inbox not populated: %+v", box)
	}
}

func TestSecretRedactionAndPreservation(t *testing.T) {
	rt, err := New(Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	saved, _ := rt.UpsertChannel(ChannelDef{Name: "tg", Type: TypeTelegram, Enabled: true,
		Config: ChannelConfig{BotToken: "secret-token", ChatID: "99"}})

	// GET redacts the secret.
	red := rt.ChannelDefsRedacted()
	if len(red) != 1 || red[0].Config.BotToken != "" {
		t.Fatalf("bot_token not redacted: %+v", red)
	}
	if red[0].Config.ChatID != "99" {
		t.Fatalf("non-secret lost in redaction: %+v", red)
	}

	// Edit with blank secret preserves the old one.
	rt.UpsertChannel(ChannelDef{ID: saved.ID, Name: "tg2", Type: TypeTelegram, Enabled: true,
		Config: ChannelConfig{ChatID: "100"}}) // BotToken blank
	full := rt.ChannelDefs()
	if full[0].Config.BotToken != "secret-token" {
		t.Fatalf("secret not preserved on blank edit: %q", full[0].Config.BotToken)
	}
	if full[0].Config.ChatID != "100" || full[0].Name != "tg2" {
		t.Fatalf("non-secret edit not applied: %+v", full[0])
	}
}

// TestWebhookWithFixedBodyLeaksNothing: a free ntfy topic is PUBLIC, so a
// webhook with a fixed body must not leak any event field. The test plants
// recognisable data in every field and requires none of it in the POST body.
func TestWebhookWithFixedBodyLeaksNothing(t *testing.T) {
	var seen []byte
	var contentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen, _ = io.ReadAll(r.Body)
		contentType = r.Header.Get("Content-Type")
	}))
	defer srv.Close()

	ev := Event{
		Type:     "hypervisor.unreachable",
		Severity: "critical",
		Source:   "sentinel:hypervisor",
		Owner:    "sam",
		Title:    "Home hypervisor unreachable",
		Body:     "WHERE: https://hypervisor.local:8006 — rpool DEGRADED on /dev/sdb",
		Labels:   map[string]string{"target": "hypervisor", "ip": "192.168.100.50"},
		DedupKey: "hypervisor:reach",
	}
	const fixedBody = "home: something needs attention. check the private channel."

	ch := NewWebhookChannel()
	if err := ch.Send(context.Background(), ev, ChannelConfig{URL: srv.URL, FixedBody: fixedBody}); err != nil {
		t.Fatal(err)
	}
	if string(seen) != fixedBody {
		t.Fatalf("body = %q, want exactly the fixed text", seen)
	}
	for _, secret := range []string{
		"hypervisor.unreachable", "critical", "sentinel", "sam", "unreachable",
		"hypervisor.local", "rpool", "sdb", "192.168.100.50", "hypervisor:reach",
	} {
		if strings.Contains(string(seen), secret) {
			t.Errorf("🔴 %q LEAKED to the public destination: %s", secret, seen)
		}
	}
	if !strings.HasPrefix(contentType, "text/plain") {
		t.Errorf("content-type = %q", contentType)
	}
}

// 🔴 THE NEGATIVE CONTROL: without a fixed body, NOTHING changes. A webhook
// pointed at a private destination (n8n, an internal Discord) keeps receiving
// the whole Event — which is exactly what it is for. Without this test,
// "fixing the leak" could have turned into "breaking every webhook".
func TestWebhookWithoutFixedBodyStillSendsEvent(t *testing.T) {
	var seen []byte
	var contentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen, _ = io.ReadAll(r.Body)
		contentType = r.Header.Get("Content-Type")
	}))
	defer srv.Close()

	ev := Event{Type: "job.failed", Title: "deploy failed", Body: "detail"}
	ch := NewWebhookChannel()
	if err := ch.Send(context.Background(), ev, ChannelConfig{URL: srv.URL}); err != nil {
		t.Fatal(err)
	}
	var received Event
	if err := json.Unmarshal(seen, &received); err != nil {
		t.Fatalf("the body stopped being the Event as JSON: %s", seen)
	}
	if received.Type != "job.failed" || received.Title != "deploy failed" {
		t.Errorf("the event arrived incomplete: %+v", received)
	}
	if !strings.HasPrefix(contentType, "application/json") {
		t.Errorf("content-type = %q", contentType)
	}
}
