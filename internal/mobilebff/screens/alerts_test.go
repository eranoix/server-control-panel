package screens

import (
	"encoding/json"
	"strings"
	"testing"

	"server-control-panel/internal/config"
	"server-control-panel/internal/mobilebff/sdui"
	"server-control-panel/internal/notify"
)

// testAlertsCfg/testAlertsViewers mirror testDockerCfg/testDockerViewers — a
// real *config.Config through the real ViewerFrom/httpx.IsAdmin path, never a
// Viewer{} literal.
func testAlertsCfg() *config.Config {
	return &config.Config{
		SchemaVersion: config.CurrentSchemaVersion,
		Primary:       "alerts-admin",
		Users: []config.User{
			{Username: "alerts-admin", PasswordHash: "h"},
			{Username: "alerts-user", PasswordHash: "h"},
		},
	}
}

func testAlertsViewers() (admin, nonAdmin sdui.Viewer) {
	cfg := testAlertsCfg()
	return sdui.ViewerFrom(cfg, "alerts-admin"), sdui.ViewerFrom(cfg, "alerts-user")
}

// testAlertsDeps returns an AlertsDeps sufficient to build the screen —
// only EventOptions/ChannelOptions matter for buildAlertsRulesScreen (the
// rows endpoint, which uses ListAlertRules, is covered separately below).
func testAlertsDeps() AlertsDeps {
	return AlertsDeps{
		EventOptions: func() []EventOption {
			return []EventOption{
				{Value: "job.failed", Label: "Job failed"},
				{Value: "metric.threshold", Label: "Metric threshold crossed"},
			}
		},
		ChannelOptions: func() []ChannelOption {
			return []ChannelOption{
				{Value: "chan-webhook-1", Label: "Webhook principal"},
				{Value: "chan-telegram-1", Label: "Telegram ops"},
			}
		},
	}
}

// Test 1 (structure): the built screen has one table (rule name/condition/
// channel/enabled) with a delete row action (Destructive: true), and one
// form (create/edit) with fields for name, condition, threshold, channel and
// an enabled toggle.
func TestAlertsRulesScreen_Structure(t *testing.T) {
	admin, _ := testAlertsViewers()
	env, err := buildAlertsRulesScreenForViewer(testAlertsDeps(), admin)
	if err != nil {
		t.Fatalf("buildAlertsRulesScreenForViewer: %v", err)
	}

	counts := map[sdui.ComponentType]int{}
	for _, c := range env.Screen.Components {
		counts[c.ComponentType()]++
	}
	want := map[sdui.ComponentType]int{
		sdui.ComponentTypeTable:              1,
		sdui.ComponentTypeForm:               1,
		sdui.ComponentTypeConfirmDestructive: 1,
	}
	for ct, n := range want {
		if counts[ct] != n {
			t.Errorf("components of type %q = %d, want %d (full count: %v)", ct, counts[ct], n, counts)
		}
	}

	table, ok := findComponent(t, env, "rules-table").(sdui.TableComponent)
	if !ok {
		t.Fatalf("rules-table is not a TableComponent")
	}
	wantColumns := []string{"name", "condition", "channel", "enabled"}
	if len(table.Columns) != len(wantColumns) {
		t.Fatalf("rules-table.columns = %v, want keys %v", table.Columns, wantColumns)
	}
	for i, key := range wantColumns {
		if table.Columns[i].Key != key {
			t.Errorf("rules-table.columns[%d].key = %q, want %q", i, table.Columns[i].Key, key)
		}
	}

	gotRowActions := map[string]bool{}
	for _, ra := range table.RowActions {
		gotRowActions[ra.ActionID] = true
	}
	if !gotRowActions[alertsActionDelete] {
		t.Errorf("rules-table.row_actions does not reference %q: %v", alertsActionDelete, table.RowActions)
	}
	if gotRowActions["alerts.rule.edit"] {
		t.Errorf("rules-table.row_actions should not have a separate \"edit\" action — editing is a save with the ID filled in")
	}

	form, ok := findComponent(t, env, "rule-form").(sdui.FormComponent)
	if !ok {
		t.Fatalf("rule-form is not a FormComponent")
	}
	wantFields := map[string]bool{"name": true, "condition": true, "threshold": true, "channel": true, "enabled": true}
	gotFields := map[string]bool{}
	for _, f := range form.Fields {
		gotFields[f.Key] = true
	}
	for key := range wantFields {
		if !gotFields[key] {
			t.Errorf("rule-form does not have field %q: %v", key, form.Fields)
		}
	}
	if form.SubmitAction.ActionID != alertsActionSave {
		t.Errorf("rule-form.submit_action = %q, want %q", form.SubmitAction.ActionID, alertsActionSave)
	}

	confirm, ok := findComponent(t, env, "rule-delete-confirm").(sdui.ConfirmDestructiveComponent)
	if !ok {
		t.Fatalf("rule-delete-confirm is not a ConfirmDestructiveComponent")
	}
	if confirm.ActionID != alertsActionDelete {
		t.Errorf("rule-delete-confirm.action_id = %q, want %q", confirm.ActionID, alertsActionDelete)
	}
}

// Test 2 (RBAC, on bytes): a non-admin's Build call returns
// sdui.ErrScreenNotFound — internal/notify's real HTTP handlers
// (handleNotifyRules/handleNotifyRuleDelete) are mustPrimary-gated on every
// method including GET, so this screen has no read-only non-admin view to
// fall back to; the whole screen is admin-only, matching that real backend
// posture exactly (see alerts.go's package doc comment).
func TestAlertsRulesScreen_AdminOnly(t *testing.T) {
	admin, nonAdmin := testAlertsViewers()
	deps := testAlertsDeps()

	if _, err := buildAlertsRulesScreenForViewer(deps, admin); err != nil {
		t.Fatalf("admin: buildAlertsRulesScreenForViewer: %v", err)
	}
	env, err := buildAlertsRulesScreenForViewer(deps, nonAdmin)
	if err != sdui.ErrScreenNotFound {
		t.Fatalf("non-admin: err = %v, want sdui.ErrScreenNotFound", err)
	}
	if env != nil {
		t.Fatalf("non-admin: envelope is not nil: %v", env)
	}
}

// Test 3 (non-vacuity): the admin payload has the full table, form and both
// actions actually present in the serialized bytes.
func TestAlertsRulesScreen_NonVacuity(t *testing.T) {
	admin, _ := testAlertsViewers()
	env, err := buildAlertsRulesScreenForViewer(testAlertsDeps(), admin)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	body := mustMarshalAlerts(t, env)
	for _, want := range []string{alertsActionSave, alertsActionDelete, "rules-table", "rule-form", "job.failed", "chan-webhook-1"} {
		if !strings.Contains(body, want) {
			t.Errorf("admin envelope does not contain %q: %s", want, body)
		}
	}
}

// Test 4 (destructive gate): alerts.rule.delete carries Destructive: true —
// the actual zero-calls-without-confirmation proof lives in
// alerts_actions_test.go (RunAction's confirmation gate is exercised there
// against the real registry).
func TestAlertsRulesAction_DeleteIsDestructive(t *testing.T) {
	registerAlertsActionsForTest()
	found := false
	for _, a := range sdui.ActionsFor(mustAlertsAdmin(t)) {
		if a.ActionID == alertsActionDelete {
			found = true
			if !a.Destructive {
				t.Errorf("%s.destructive = false, want true", alertsActionDelete)
			}
			if a.Permission != "admin" {
				t.Errorf("%s.permission = %q, want \"admin\"", alertsActionDelete, a.Permission)
			}
		}
	}
	if !found {
		t.Fatalf("%s is not registered", alertsActionDelete)
	}
}

func mustAlertsAdmin(t *testing.T) sdui.Viewer {
	t.Helper()
	admin, _ := testAlertsViewers()
	return admin
}

func mustMarshalAlerts(t *testing.T, env *sdui.Envelope) string {
	t.Helper()
	b, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

var _ = notify.SeverityInfo // keeps the import documenting where the threshold values come from
