// Package wachannel adapts the multi-tenant WhatsApp manager into a
// notify.Channel. It deliberately lives in its OWN package so that
// internal/notify never imports internal/whatsapp: a later producer (e.g.
// emitting whatsapp.disconnected events) will import notify to Dispatch, and if
// notify imported whatsapp that would be an import cycle. The Channel adapter
// is the one place both packages meet, wired together in api.go.
package wachannel

import (
	"context"

	"server-control-panel/internal/notify"
	"server-control-panel/internal/scope"
	"server-control-panel/internal/whatsapp"
)

// Type is the channel type id registered with the Router.
const Type = "whatsapp"

// Channel sends notify Events as WhatsApp text messages, mirroring the
// established alert/handler.go path: scope.New(FromUser) -> mgr.ForUser ->
// Service.Client.SendText(ChatJID, text, "").
type Channel struct {
	// provider resolves the manager LAZILY at send time. The Router is
	// constructed early in boot (right after the queue, so SetNotifier can wire
	// before the boot window closes) while whatsappMgr is initialised a few
	// lines later — capturing the pointer at construction would freeze a nil.
	// A getter sidesteps that ordering entirely.
	provider func() *whatsapp.Manager
}

// New returns a WhatsApp notify.Channel whose manager is resolved on each Send
// via provider. provider (or its result) may be nil when WhatsApp is disabled;
// Send then fails closed with a clear error rather than panicking, and the
// Router's breaker absorbs it.
func New(provider func() *whatsapp.Manager) *Channel { return &Channel{provider: provider} }

func (c *Channel) Name() string { return Type }

func (c *Channel) Send(ctx context.Context, ev notify.Event, cfg notify.ChannelConfig) error {
	var mgr *whatsapp.Manager
	if c.provider != nil {
		mgr = c.provider()
	}
	if mgr == nil {
		return errWAUnavailable
	}
	if cfg.FromUser == "" {
		return errNoFromUser
	}
	if cfg.ChatJID == "" {
		return errNoChatJID
	}
	user, err := scope.New(cfg.FromUser)
	if err != nil {
		return err
	}
	svc, err := mgr.ForUser(user)
	if err != nil {
		return err
	}
	// ctx carries the Router's 5s timeout. SendText is a blocking HTTP call to
	// WAHA with its own client timeout; we honor cancellation by bailing early
	// if the deadline already passed before we dial.
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err = svc.Client.SendText(cfg.ChatJID, notify.FormatText(ev), "")
	return err
}

type waErr string

func (e waErr) Error() string { return string(e) }

const (
	errWAUnavailable = waErr("whatsapp: manager unavailable")
	errNoFromUser    = waErr("whatsapp: from_user not configured")
	errNoChatJID     = waErr("whatsapp: chat_jid not configured")
)
