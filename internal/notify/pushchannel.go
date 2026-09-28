package notify

import (
	"context"
	"encoding/json"
	"strings"

	"server-control-panel/internal/notify/fcmpush"
	"server-control-panel/internal/webpush"

	pushvendor "github.com/SherClockHolmes/webpush-go"
)

const TypePush = "push"

type SendOptions struct {
	TTL      int
	Critical bool
}

type pushSender interface {
	SendToUser(ctx context.Context, user string, payload []byte, opts SendOptions, allowDevice func(deviceID string) bool) int
	SendToAll(ctx context.Context, payload []byte, opts SendOptions, allowDevice func(deviceID string) bool) int
}

type webpushSender struct{ store *webpush.Store }

func NewWebpushSender(store *webpush.Store) pushSender { return &webpushSender{store: store} }

func (w *webpushSender) SendToUser(ctx context.Context, user string, payload []byte, opts SendOptions, allowDevice func(string) bool) int {
	if w.store == nil {
		return 0
	}
	return w.store.SendToUser(ctx, user, payload, opts.toWebpush(), allowDevice)
}

func (w *webpushSender) SendToAll(ctx context.Context, payload []byte, opts SendOptions, allowDevice func(string) bool) int {
	if w.store == nil {
		return 0
	}
	return w.store.SendToAll(ctx, payload, opts.toWebpush(), allowDevice)
}

func (o SendOptions) toWebpush() webpush.SendOptions {
	urgency := pushvendor.UrgencyNormal
	if o.Critical {
		urgency = pushvendor.UrgencyHigh
	}
	return webpush.SendOptions{TTL: o.TTL, Urgency: urgency}
}

type fcmSenderAdapter struct{ sender *fcmpush.Sender }

func NewFCMSender(sender *fcmpush.Sender) pushSender { return &fcmSenderAdapter{sender: sender} }

func (f *fcmSenderAdapter) SendToUser(ctx context.Context, user string, payload []byte, opts SendOptions, allowDevice func(string) bool) int {
	if f.sender == nil {
		return 0
	}
	return f.sender.SendToUser(ctx, user, payload, opts.toFCM(), allowDevice)
}

func (f *fcmSenderAdapter) SendToAll(ctx context.Context, payload []byte, opts SendOptions, allowDevice func(string) bool) int {
	if f.sender == nil {
		return 0
	}
	return f.sender.SendToAll(ctx, payload, opts.toFCM(), allowDevice)
}

func (o SendOptions) toFCM() fcmpush.SendOptions {
	priority := "normal"
	if o.Critical {
		priority = "high"
	}
	return fcmpush.SendOptions{Priority: priority}
}

type multiSender struct{ senders []pushSender }

func FanOutSenders(senders ...pushSender) pushSender { return &multiSender{senders: senders} }

func (m *multiSender) SendToUser(ctx context.Context, user string, payload []byte, opts SendOptions, allowDevice func(string) bool) int {
	total := 0
	for _, s := range m.senders {
		if s == nil {
			continue
		}
		total += s.SendToUser(ctx, user, payload, opts, allowDevice)
	}
	return total
}

func (m *multiSender) SendToAll(ctx context.Context, payload []byte, opts SendOptions, allowDevice func(string) bool) int {
	total := 0
	for _, s := range m.senders {
		if s == nil {
			continue
		}
		total += s.SendToAll(ctx, payload, opts, allowDevice)
	}
	return total
}

type DevicePrefsResolver interface {
	Allowed(deviceID, ruleID string) bool
}

type PushChannel struct {
	sender pushSender
	prefs  DevicePrefsResolver
}

func NewPushChannel(sender pushSender, prefs DevicePrefsResolver) *PushChannel {
	return &PushChannel{sender: sender, prefs: prefs}
}

func (c *PushChannel) Name() string { return TypePush }

func (c *PushChannel) Send(ctx context.Context, ev Event, cfg ChannelConfig) error {
	if c.sender == nil {
		return errMissing("push", "sender")
	}
	payload, err := json.Marshal(buildPushPayload(ev))
	if err != nil {
		return err
	}
	opts := SendOptions{TTL: 300, Critical: ev.Severity == SeverityCritical}
	allow := c.allowDevice(ev, cfg)
	if cfg.ToUser != "" {
		c.sender.SendToUser(ctx, cfg.ToUser, payload, opts, allow)
	} else {
		c.sender.SendToAll(ctx, payload, opts, allow)
	}
	return nil
}

func (c *PushChannel) allowDevice(ev Event, cfg ChannelConfig) func(deviceID string) bool {
	return func(deviceID string) bool {
		if cfg.ToDevice != "" && deviceID != cfg.ToDevice {
			return false
		}
		if c.prefs == nil || ev.RuleID == "" {
			return true
		}
		return c.prefs.Allowed(deviceID, ev.RuleID)
	}
}

func buildPushPayload(ev Event) map[string]any {
	if strings.HasPrefix(ev.Type, "metric.") {
		return map[string]any{
			"type":       "alert-fired",
			"rule":       ev.Labels["rule"],
			"body":       ev.Body,
			"severity":   ev.Severity,
			"event_type": ev.Type,
		}
	}
	return map[string]any{
		"title":      ev.Title,
		"body":       ev.Body,
		"tag":        "panel-" + strings.ReplaceAll(ev.Type, ".", "-"),
		"event_type": ev.Type,
		"severity":   ev.Severity,
		"job_id":     ev.Labels["job_id"],
	}
}
