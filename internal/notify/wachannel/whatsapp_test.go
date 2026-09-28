package wachannel

import (
	"context"
	"testing"

	"server-control-panel/internal/notify"
	"server-control-panel/internal/whatsapp"
)

func TestSendFailsClosed(t *testing.T) {
	for _, c := range []*Channel{New(nil), New(func() *whatsapp.Manager { return nil })} {
		if c.Name() != Type {
			t.Fatalf("Name=%q want %q", c.Name(), Type)
		}
		ev := notify.Event{Type: notify.TypeJobFailed, Title: "x"}
		if err := c.Send(context.Background(), ev, notify.ChannelConfig{FromUser: "a", ChatJID: "1@c.us"}); err != errWAUnavailable {
			t.Fatalf("nil mgr: got %v want errWAUnavailable", err)
		}
	}
}
