package wachannel

import (
	"context"
	"testing"

	"server-control-panel/internal/notify"
	"server-control-panel/internal/whatsapp"
)

// A nil manager and missing config must fail closed (clear error), never panic.
// The Router's breaker then absorbs these as channel failures.
func TestSendFailsClosed(t *testing.T) {
	// Both a nil provider and a provider that returns nil must fail closed.
	for _, c := range []*Channel{New(nil), New(func() *whatsapp.Manager { return nil })} {
		if c.Name() != Type {
			t.Fatalf("Name=%q want %q", c.Name(), Type)
		}
		ev := notify.Event{Type: notify.TypeJobFailed, Title: "x"}
		// mgr-nil is checked before config, so it dominates: the channel is
		// fundamentally unavailable regardless of FromUser/ChatJID.
		if err := c.Send(context.Background(), ev, notify.ChannelConfig{FromUser: "a", ChatJID: "1@c.us"}); err != errWAUnavailable {
			t.Fatalf("nil mgr: got %v want errWAUnavailable", err)
		}
	}
}
