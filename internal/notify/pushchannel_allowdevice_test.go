package notify

import (
	"context"
	"testing"
)

// recordingPushSender captures the allowDevice closure PushChannel.Send
// built for the call and, on demand, reports which of a fixed device set it
// actually let through — this is how these tests observe allowDevice's
// per-device decisions without a real transport.
type recordingPushSender struct {
	lastAllow func(deviceID string) bool
}

func (r *recordingPushSender) SendToUser(_ context.Context, _ string, _ []byte, _ SendOptions, allowDevice func(string) bool) int {
	r.lastAllow = allowDevice
	return 0
}

func (r *recordingPushSender) SendToAll(_ context.Context, _ []byte, _ SendOptions, allowDevice func(string) bool) int {
	r.lastAllow = allowDevice
	return 0
}

// fakePrefsResolver is an in-memory DevicePrefsResolver test double: known
// maps deviceID->ruleID->allowed explicitly; any deviceID not present in
// known at all is "unknown" and must fail OPEN (see DevicePrefsResolver's
// doc comment) — recorded here as a separate set so tests can prove both
// branches.
type fakePrefsResolver struct {
	known map[string]map[string]bool
}

func (f *fakePrefsResolver) Allowed(deviceID, ruleID string) bool {
	rules, ok := f.known[deviceID]
	if !ok {
		return true // unknown device: fail open
	}
	return rules[ruleID]
}

// TestAllowDevice_PerRuleFiltering proves the filter is genuinely per-Rule
// (via ev.RuleID), not a channel-wide or event-type-only gate: the same
// device is allowed for one Rule and blocked for another.
func TestAllowDevice_PerRuleFiltering(t *testing.T) {
	sender := &recordingPushSender{}
	prefs := &fakePrefsResolver{known: map[string]map[string]bool{
		"dev-1": {"rule-critical": true, "rule-chatty": false},
	}}
	c := NewPushChannel(sender, prefs)

	if err := c.Send(context.Background(), Event{Type: "job.failed", RuleID: "rule-critical"}, ChannelConfig{}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !sender.lastAllow("dev-1") {
		t.Fatalf("dev-1 must be allowed for rule-critical (explicitly enabled)")
	}

	if err := c.Send(context.Background(), Event{Type: "job.done", RuleID: "rule-chatty"}, ChannelConfig{}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if sender.lastAllow("dev-1") {
		t.Fatalf("dev-1 must be BLOCKED for rule-chatty (explicitly disabled) — filter must be per-Rule, not per-device-only")
	}
}

// TestAllowDevice_PerDeviceFiltering proves the filter is genuinely
// per-device: for the SAME Rule, one device is allowed and another is not.
func TestAllowDevice_PerDeviceFiltering(t *testing.T) {
	sender := &recordingPushSender{}
	prefs := &fakePrefsResolver{known: map[string]map[string]bool{
		"dev-allowed": {"rule-x": true},
		"dev-blocked": {"rule-x": false},
	}}
	c := NewPushChannel(sender, prefs)

	if err := c.Send(context.Background(), Event{Type: "job.done", RuleID: "rule-x"}, ChannelConfig{}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !sender.lastAllow("dev-allowed") {
		t.Fatalf("dev-allowed must be allowed for rule-x")
	}
	if sender.lastAllow("dev-blocked") {
		t.Fatalf("dev-blocked must be blocked for rule-x")
	}
}

// TestAllowDevice_UnknownDeviceFailsOpen proves a device the resolver has
// never heard of (e.g. every pre-existing internal/webpush browser
// subscription, which this feature never registers a DevicePrefsStore row
// for) is NOT silently defaulted to critical-only — it must keep receiving
// every push exactly like before per-device preferences shipped.
func TestAllowDevice_UnknownDeviceFailsOpen(t *testing.T) {
	sender := &recordingPushSender{}
	prefs := &fakePrefsResolver{known: map[string]map[string]bool{
		"dev-known": {"rule-x": false},
	}}
	c := NewPushChannel(sender, prefs)

	if err := c.Send(context.Background(), Event{Type: "job.done", RuleID: "rule-x", Severity: SeverityInfo}, ChannelConfig{}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !sender.lastAllow("dev-never-registered") {
		t.Fatalf("an unknown device must fail OPEN (allowed) for a non-critical event, not default to critical-only")
	}
	if sender.lastAllow("dev-known") {
		t.Fatalf("dev-known has an explicit disable for rule-x and must stay blocked")
	}
}

// TestAllowDevice_NilPrefsAllowsEveryone proves that with no
// DevicePrefsResolver wired at all (nil), PushChannel keeps the earlier
// broadcast behavior — every device is allowed unconditionally.
func TestAllowDevice_NilPrefsAllowsEveryone(t *testing.T) {
	sender := &recordingPushSender{}
	c := NewPushChannel(sender, nil)
	if err := c.Send(context.Background(), Event{Type: "job.done", RuleID: "rule-x"}, ChannelConfig{}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !sender.lastAllow("any-device") {
		t.Fatalf("nil prefs must allow every device")
	}
}

// TestAllowDevice_EmptyRuleIDFailsOpen proves a direct/test-only Send call
// that bypasses Router.handle() (and therefore never got a RuleID) does not
// silently drop a legitimate alert — it fails open rather than closed.
func TestAllowDevice_EmptyRuleIDFailsOpen(t *testing.T) {
	sender := &recordingPushSender{}
	prefs := &fakePrefsResolver{known: map[string]map[string]bool{
		"dev-1": {"rule-x": false},
	}}
	c := NewPushChannel(sender, prefs)
	if err := c.Send(context.Background(), Event{Type: "job.done"}, ChannelConfig{}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !sender.lastAllow("dev-1") {
		t.Fatalf("empty ev.RuleID must fail open even for a device with an explicit disable on another rule")
	}
}

// TestAllowDevice_ToDeviceRestrictsToOneDevice proves ChannelConfig.ToDevice
// narrows delivery to exactly one device_id, independent of DevicePrefs.
func TestAllowDevice_ToDeviceRestrictsToOneDevice(t *testing.T) {
	sender := &recordingPushSender{}
	c := NewPushChannel(sender, nil)
	if err := c.Send(context.Background(), Event{Type: "job.done", RuleID: "rule-x"}, ChannelConfig{ToDevice: "only-this-one"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !sender.lastAllow("only-this-one") {
		t.Fatalf("the ToDevice-targeted device must be allowed")
	}
	if sender.lastAllow("some-other-device") {
		t.Fatalf("a device other than ToDevice must be blocked")
	}
}
