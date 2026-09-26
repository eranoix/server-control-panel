package notify

import (
	"context"
	"encoding/json"
	"strings"

	"server-control-panel/internal/notify/fcmpush"
	"server-control-panel/internal/webpush"

	// pushvendor is the underlying Web Push library. internal/webpush's own
	// SendOptions.Urgency field is typed against this package (it does not
	// re-export UrgencyNormal/UrgencyHigh), so toWebpush needs it directly.
	// This is the only symbol imported from it — everything else about Web
	// Push stays behind internal/webpush.
	pushvendor "github.com/SherClockHolmes/webpush-go"
)

// TypePush is the channel type id for OS-level push notifications delivered
// while the panel tab is closed.
//
// Before this file, internal/webassets/serviceworker.go already handled an
// "alert-fired" push payload that no Go code ever published — the only
// sender in the codebase was videocall's incoming-call ring, which only
// fires for calls. PushChannel makes that payload real for any event a Rule
// routes here, reusing the Router's existing rule/throttle/dedup/breaker
// engine instead of a bespoke path.
//
// SendOptions belongs to notify, not to webpush — that is what lets an FCM
// sender plug in here without rewriting this file.
const TypePush = "push"

// SendOptions parametrizes one push send, owned entirely by notify — no
// third-party push-vendor types leak into the pushSender interface.
type SendOptions struct {
	// TTL is how many seconds the message may be queued by the delivery
	// service before being dropped.
	TTL int
	// Critical requests best-effort immediate/high-priority delivery when
	// the underlying sender supports a priority tier (Web Push: Urgency:
	// high; a future FCM sender: AndroidConfig.Priority: "high").
	Critical bool
}

// pushSender is the seam PushChannel delivers through. Every parameter type
// here is either a stdlib type or SendOptions — nothing from
// internal/webpush appears in this interface, so a future FCM sender can
// implement it directly without faking Web Push vocabulary.
type pushSender interface {
	SendToUser(ctx context.Context, user string, payload []byte, opts SendOptions, allowDevice func(deviceID string) bool) int
	SendToAll(ctx context.Context, payload []byte, opts SendOptions, allowDevice func(deviceID string) bool) int
}

// webpushSender adapts a *webpush.Store to pushSender, translating
// notify.SendOptions into webpush.SendOptions only at this boundary. It is
// the ONLY place in this package that references webpush-go types.
type webpushSender struct{ store *webpush.Store }

// NewWebpushSender returns a pushSender backed by store. store may be nil
// (e.g. VAPID key generation failed at boot) — calls then degrade to
// delivering nothing instead of panicking, matching how other channels
// degrade on missing per-channel config.
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

// toWebpush translates the generic SendOptions into Web Push's vocabulary.
func (o SendOptions) toWebpush() webpush.SendOptions {
	urgency := pushvendor.UrgencyNormal
	if o.Critical {
		urgency = pushvendor.UrgencyHigh
	}
	return webpush.SendOptions{TTL: o.TTL, Urgency: urgency}
}

// fcmSenderAdapter adapts a *fcmpush.Sender to pushSender, translating
// notify.SendOptions into fcmpush.SendOptions only at this boundary — the
// same division fcmpush's own doc comment describes for webpushSender
// above.
type fcmSenderAdapter struct{ sender *fcmpush.Sender }

// NewFCMSender returns a pushSender backed by sender (built via
// fcmpush.NewSender from the FCM service-account JSON — see
// internal/api/notify_wire.go). sender may be nil (e.g. no FCM credential
// provisioned yet), degrading to zero deliveries instead of panicking,
// matching NewWebpushSender(nil)'s behavior.
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

// toFCM translates the generic SendOptions into FCM's vocabulary — the
// AndroidConfig.Priority equivalent of toWebpush's Urgency.
func (o SendOptions) toFCM() fcmpush.SendOptions {
	priority := "normal"
	if o.Critical {
		priority = "high"
	}
	return fcmpush.SendOptions{Priority: priority}
}

// multiSender fans one Send out to every constituent pushSender, summing
// delivered counts. Exists because notify.Channel is registered in a
// map[string]Channel keyed by TYPE string ("push") — webpush and FCM
// cannot each be a separate Channel under that one key, so PushChannel
// holds ONE sender that is itself a fan-out over both backends.
type multiSender struct{ senders []pushSender }

// FanOutSenders combines multiple pushSenders (e.g. webpush + FCM) behind
// one pushSender, so a single PushChannel instance delivers to both
// backends for every Send. nil entries are skipped (e.g. NewFCMSender(nil)
// when no FCM credential is provisioned — still safe to include, since its
// own methods already degrade to 0, but callers may also omit it entirely).
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

// DevicePrefsResolver is the seam PushChannel consults to decide whether a
// given device may receive events matched by a given Rule.
// Defined here (consumer side), exactly like pushSender itself: satisfied
// structurally by mobilebff.DevicePrefsStore via an adapter constructed in
// internal/api/notify_wire.go (which already imports both packages) — this
// package never imports internal/mobilebff.
//
// Allowed answers false ONLY for a device this resolver has an explicit,
// on-record preference for (seeded at FCM device-registration time —
// mobilebff's push_devices.go — with severity>=critical Rules enabled and
// everything else off by default, then mutable via PUT
// /api/mobile/v1/notify/preferences). A deviceID this resolver has never
// heard of at all — e.g. every pre-existing internal/webpush browser
// subscription, which carries its own, unrelated DeviceID (the
// per-device MUTE toggle, not a preference row) — is NOT the same
// as "known device, no explicit choice yet": Allowed must return true for
// it. Doing otherwise would silently flip every already-registered browser
// subscription to critical-only the moment this feature ships, since none
// of them will ever gain a DevicePrefsStore row through the FCM-only
// registration endpoint this feature adds. This mirrors an existing, explicit
// precedent in this codebase for the exact same kind of migration
// (internal/webpush.PushSubscription.DeviceID's own doc comment: "empty on
// subscriptions predating the field → never silenced" — unknown/absent
// device state fails OPEN, not closed).
type DevicePrefsResolver interface {
	Allowed(deviceID, ruleID string) bool
}

// PushChannel delivers Events as OS-level push notifications via pushSender.
// prefs may be nil (no per-device preference store wired) — Send then
// allows every device unconditionally, i.e. the original broadcast behavior.
type PushChannel struct {
	sender pushSender
	prefs  DevicePrefsResolver
}

// NewPushChannel constructs a PushChannel. sender may be nil (e.g. built
// from NewWebpushSender(nil)) — Send then fails closed via errMissing rather
// than panicking. prefs may be nil — see PushChannel's doc comment.
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
	// Zero deliveries just means nobody is currently subscribed — normal,
	// not a channel malfunction, so it must not trip the Router's breaker.
	return nil
}

// allowDevice builds the per-device filter for one Send call. It
// combines cfg.ToDevice (a config-level single-device restriction) with
// ev.RuleID + c.prefs (the per-device, per-Rule opt-in/opt-out) — see
// DevicePrefsResolver's doc comment for the fail-open semantics on unknown
// devices and PushChannel's for nil prefs. ev.RuleID empty (should never
// happen via Router.handle(), but a direct/test-only Send call could bypass
// it) also fails open: never silently drop a legitimate alert because the
// Rule identity did not make it through.
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

// buildPushPayload shapes the JSON both push backends fan out: the Service
// Worker's "push" listener (webpush) AND the Android FCM data payload
// (internal/notify/fcmpush, flattened to string values by buildMessageBody).
// metric.* events use the "alert-fired" shape the Service Worker has handled
// since internal/webassets/serviceworker.go:151 was written; everything else
// uses the generic title/body/tag fallback. `severity` (both branches) and
// `event_type`/`job_id` (job branch only) are additive fields the Service
// Worker's listener ignores (it only reads the keys it already checks for)
// but the Android client's ActionableNotificationBuilder
// needs to deep-link to the exact job and to gate inline actions by severity
// — do not repurpose the existing "type" key for job events' real
// notify.Event.Type ("job.failed" etc.), since the Service Worker never
// reads "type" outside the metric branch and Android reads the raw type from
// this new "event_type" key instead, keeping the metric branch's "type":
// "alert-fired" contract untouched.
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
		"tag":        "vpsm-" + strings.ReplaceAll(ev.Type, ".", "-"),
		"event_type": ev.Type,
		"severity":   ev.Severity,
		"job_id":     ev.Labels["job_id"],
	}
}
