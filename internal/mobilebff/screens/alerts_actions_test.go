package screens

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"server-control-panel/internal/mobilebff/sdui"
	"server-control-panel/internal/notify"
)

type fakeAlertsBackend struct {
	mu sync.Mutex

	rules map[string]AlertRuleRow

	saveCalls   int
	deleteCalls int
	audit       []alertsAuditRecord
}

type alertsAuditRecord struct{ user, action, target string }

func newFakeAlertsBackend() *fakeAlertsBackend {
	return &fakeAlertsBackend{
		rules: map[string]AlertRuleRow{
			"rule-1": {
				ID: "rule-1", Name: "Failing jobs", Enabled: true,
				TypePrefix: "job.failed", MinSeverity: notify.SeverityWarning,
				Channels: []string{"chan-webhook-1"},
			},
		},
	}
}

func (b *fakeAlertsBackend) deps() AlertsDeps {
	return AlertsDeps{
		ListAlertRules: func(_ sdui.Viewer) []AlertRuleRow {
			b.mu.Lock()
			defer b.mu.Unlock()
			out := make([]AlertRuleRow, 0, len(b.rules))
			for _, r := range b.rules {
				out = append(out, r)
			}
			return out
		},
		ChannelOptions: func() []ChannelOption {
			return []ChannelOption{
				{Value: "chan-webhook-1", Label: "Webhook principal"},
				{Value: "chan-telegram-1", Label: "Telegram ops"},
			}
		},
		EventOptions: func() []EventOption {
			return []EventOption{
				{Value: "job.failed", Label: "Job failed"},
				{Value: "metric.threshold", Label: "Metric threshold crossed"},
			}
		},
		SaveAlertRule: func(in AlertRuleInput) (*AlertRuleRow, error) {
			b.mu.Lock()
			defer b.mu.Unlock()
			b.saveCalls++
			id := in.ID
			if id == "" {
				id = "rule-new"
			}
			row := AlertRuleRow{
				ID: id, Name: in.Name, Enabled: in.Enabled,
				TypePrefix: in.TypePrefix, MinSeverity: in.MinSeverity, Channels: in.Channels,
			}
			b.rules[id] = row
			return &row, nil
		},
		DeleteAlertRule: func(id string) error {
			b.mu.Lock()
			defer b.mu.Unlock()
			b.deleteCalls++
			delete(b.rules, id)
			return nil
		},
		AuditEvent: func(user, action, target string) {
			b.mu.Lock()
			defer b.mu.Unlock()
			b.audit = append(b.audit, alertsAuditRecord{user: user, action: action, target: target})
		},
	}
}

func (b *fakeAlertsBackend) counts() (save, del int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.saveCalls, b.deleteCalls
}

func (b *fakeAlertsBackend) rule(id string) (AlertRuleRow, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	r, ok := b.rules[id]
	return r, ok
}

func mustJSONAlerts(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func TestAlertsAction_Save_InvalidFieldsKeyed(t *testing.T) {
	backend := newFakeAlertsBackend()
	handle := handleAlertsRuleSave(backend.deps())
	admin, _ := testAlertsViewers()

	_, err := handle(context.Background(), admin, nil, mustJSONAlerts(t, saveAlertRuleInput{
		Name: "x", Condition: "nonexistent-condition", Threshold: "catastrophic", Channel: "ghost-channel",
	}))

	var fe sdui.FieldErrors
	if !errors.As(err, &fe) {
		t.Fatalf("error = %v (%T), want sdui.FieldErrors", err, err)
	}
	for _, key := range []string{"condition", "threshold", "channel"} {
		if _, ok := fe[key]; !ok {
			t.Errorf("FieldErrors does not have the key %q: %v", key, fe)
		}
	}
	if !errors.Is(err, sdui.ErrValidation) {
		t.Errorf("errors.Is(err, sdui.ErrValidation) = false")
	}
	if save, _ := backend.counts(); save != 0 {
		t.Errorf("SaveAlertRule was called %d time(s), want 0", save)
	}

	env := buildAlertsRulesScreen(backend.deps(), admin)
	form := findComponent(t, env, "rule-form").(sdui.FormComponent)
	if matchErr := fe.MatchesForm(&form); matchErr != nil {
		t.Errorf("MatchesForm: %v", matchErr)
	}
}

func TestAlertsAction_Save_EmptyNameRequired(t *testing.T) {
	backend := newFakeAlertsBackend()
	handle := handleAlertsRuleSave(backend.deps())
	admin, _ := testAlertsViewers()

	_, err := handle(context.Background(), admin, nil, mustJSONAlerts(t, saveAlertRuleInput{
		Name: "", Condition: "", Threshold: notify.SeverityInfo, Channel: "chan-webhook-1",
	}))
	var fe sdui.FieldErrors
	if !errors.As(err, &fe) {
		t.Fatalf("empty name: error = %v, want FieldErrors", err)
	}
	if _, ok := fe["name"]; !ok {
		t.Errorf("empty name: FieldErrors does not have \"name\": %v", fe)
	}
	if _, ok := fe["condition"]; ok {
		t.Errorf("empty condition (wildcard) should not be an error: %v", fe)
	}
}

func TestAlertsAction_Save_ValidRuleInvalidatesAndAudits(t *testing.T) {
	backend := newFakeAlertsBackend()
	handle := handleAlertsRuleSave(backend.deps())
	admin, _ := testAlertsViewers()

	result, err := handle(context.Background(), admin, nil, mustJSONAlerts(t, saveAlertRuleInput{
		Name: "New rule", Condition: "job.failed", Threshold: notify.SeverityWarning,
		Channel: "chan-telegram-1", Enabled: true,
	}))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if len(result.Invalidate) == 0 || result.Patch != nil {
		t.Errorf("result = %+v, want only Invalidate filled in", result)
	}
	if valErr := result.Validate(); valErr != nil {
		t.Errorf("ActionResult.Validate: %v", valErr)
	}
	if save, _ := backend.counts(); save != 1 {
		t.Errorf("SaveAlertRule was called %d time(s), want 1", save)
	}
	log := backend.audit
	if len(log) != 1 || log[0].action != "alerts.rule.create" {
		t.Errorf("audit log = %v, want 1 alerts.rule.create entry", log)
	}
}

var (
	registerAlertsActionsTestOnce sync.Once
	registerAlertsActionsTestDeps *fakeAlertsBackend
)

func registerAlertsActionsForTest() *fakeAlertsBackend {
	registerAlertsActionsTestOnce.Do(func() {
		registerAlertsActionsTestDeps = newFakeAlertsBackend()
		registerAlertsActions(registerAlertsActionsTestDeps.deps())
	})
	return registerAlertsActionsTestDeps
}

func TestAlertsAction_DeleteRequiresConfirmation(t *testing.T) {
	backend := registerAlertsActionsForTest()
	admin, _ := testAlertsViewers()

	before, ok := backend.rule("rule-1")
	if !ok {
		t.Fatal("fixture rule-1 missing before the test")
	}
	_ = before

	_, err := sdui.RunAction(context.Background(), alertsActionDelete, admin,
		map[string]string{"id": "rule-1"}, nil, sdui.Confirmation{Confirmed: false})

	var fe sdui.FieldErrors
	if !errors.As(err, &fe) {
		t.Fatalf("without confirmation: error = %v, want FieldErrors", err)
	}
	if _, ok := fe[sdui.ConfirmationFieldKey]; !ok {
		t.Errorf("without confirmation: FieldErrors does not have %q: %v", sdui.ConfirmationFieldKey, fe)
	}
	if _, del := backend.counts(); del != 0 {
		t.Errorf("DeleteAlertRule was called %d time(s) without confirmation, want 0", del)
	}
	if _, ok := backend.rule("rule-1"); !ok {
		t.Fatal("rule was removed even without confirmation")
	}

	result, err := sdui.RunAction(context.Background(), alertsActionDelete, admin,
		map[string]string{"id": "rule-1"}, nil, sdui.Confirmation{Confirmed: true})
	if err != nil {
		t.Fatalf("with confirmation: error = %v", err)
	}
	if len(result.Invalidate) == 0 {
		t.Errorf("with confirmation: result = %+v, want Invalidate filled in", result)
	}
	if _, del := backend.counts(); del != 1 {
		t.Errorf("DeleteAlertRule was called %d time(s), want 1", del)
	}
	if _, ok := backend.rule("rule-1"); ok {
		t.Error("rule still present after confirmation")
	}
}

func TestAlertsAction_NonAdminNotFound(t *testing.T) {
	backend := registerAlertsActionsForTest()
	_, nonAdmin := testAlertsViewers()

	baseSave, baseDel := backend.counts()

	_, err := sdui.RunAction(context.Background(), alertsActionSave, nonAdmin, nil,
		mustJSONAlerts(t, saveAlertRuleInput{Name: "x", Condition: "job.failed", Threshold: notify.SeverityInfo, Channel: "chan-webhook-1"}),
		sdui.Confirmation{})
	if !errors.Is(err, sdui.ErrActionNotFound) {
		t.Errorf("save non-admin: error = %v, want sdui.ErrActionNotFound", err)
	}

	_, err = sdui.RunAction(context.Background(), alertsActionDelete, nonAdmin,
		map[string]string{"id": "rule-1"}, nil, sdui.Confirmation{Confirmed: true})
	if !errors.Is(err, sdui.ErrActionNotFound) {
		t.Errorf("delete non-admin: error = %v, want sdui.ErrActionNotFound", err)
	}

	save, del := backend.counts()
	if save != baseSave || del != baseDel {
		t.Errorf("mutation was reached by non-admin: save=%d(base %d) delete=%d(base %d)", save, baseSave, del, baseDel)
	}
}

func TestAlertsAction_DeleteUnknownRuleNotFound(t *testing.T) {
	backend := newFakeAlertsBackend()
	handle := handleAlertsRuleDelete(backend.deps())
	admin, _ := testAlertsViewers()

	_, err := handle(context.Background(), admin, map[string]string{"id": "rule-does-not-exist"}, nil)
	if !errors.Is(err, sdui.ErrActionNotFound) {
		t.Errorf("error = %v, want sdui.ErrActionNotFound", err)
	}
	if _, del := backend.counts(); del != 0 {
		t.Errorf("DeleteAlertRule was called %d time(s), want 0", del)
	}
}
