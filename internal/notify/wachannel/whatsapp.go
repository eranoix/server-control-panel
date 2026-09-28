package wachannel

import (
	"context"

	"server-control-panel/internal/notify"
	"server-control-panel/internal/scope"
	"server-control-panel/internal/whatsapp"
)

const Type = "whatsapp"

type Channel struct {
	provider func() *whatsapp.Manager
}

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
