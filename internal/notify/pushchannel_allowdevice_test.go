package notify

import (
	"context"
	"testing"
)

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

type fakePrefsResolver struct {
	known map[string]map[string]bool
}

func (f *fakePrefsResolver) Allowed(deviceID, ruleID string) bool {
	rules, ok := f.known[deviceID]
	if !ok {
		return true
	}
	return rules[ruleID]
}

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
