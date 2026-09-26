// Package sdui_test wires the alerts.rules screen into the sdui package's
// own golden-fixture test binary — same rationale as docker_golden_test.go's
// header comment: golden_test.go's harness only sees screens registered
// inside the SAME test binary process, and being an external test package
// (sdui_test) is what lets this file import internal/mobilebff/screens
// without an import cycle.
package sdui_test

import (
	"server-control-panel/internal/mobilebff/screens"
	"server-control-panel/internal/mobilebff/sdui"
)

// alertsGoldenBackend is a small in-memory stand-in for AlertsDeps — enough
// to build the alerts.rules screen and its rows deterministically for the
// golden corpus. It never touches a real internal/notify.Router; the golden
// harness only calls Build (via the sdui.Screen builder), never RunAction,
// so the mutating closures below are unreachable from the harness and exist
// only to satisfy AlertsDeps' shape.
type alertsGoldenBackend struct{}

func (alertsGoldenBackend) deps() screens.AlertsDeps {
	return screens.AlertsDeps{
		ListAlertRules: func(_ sdui.Viewer) []screens.AlertRuleRow {
			return []screens.AlertRuleRow{
				{
					ID: "rule-golden-1", Name: "Job failed", Enabled: true,
					TypePrefix: "job.failed", MinSeverity: "warning",
					Channels: []string{"chan-golden-webhook"},
				},
			}
		},
		ChannelOptions: func() []screens.ChannelOption {
			return []screens.ChannelOption{
				{Value: "chan-golden-webhook", Label: "Webhook golden"},
			}
		},
		EventOptions: func() []screens.EventOption {
			return []screens.EventOption{
				{Value: "job.failed", Label: "Job failed"},
			}
		},
		SaveAlertRule:   func(in screens.AlertRuleInput) (*screens.AlertRuleRow, error) { return nil, nil },
		DeleteAlertRule: func(string) error { return nil },
		AuditEvent:      func(_, _, _ string) {},
	}
}

// init registers the alerts.rules screen into this test binary's
// process-global sdui registries exactly once — the same
// screens.RegisterAlerts(deps) internal/api/api.go calls in production, fed
// synthetic data instead of a real *notify.Router. This is what makes
// RegisteredScreens() (used by TestGoldenScreens and its two role-omission
// checks) see alerts.rules at all when running
// `go test ./internal/mobilebff/sdui/...`.
func init() {
	screens.RegisterAlerts(alertsGoldenBackend{}.deps())
}
