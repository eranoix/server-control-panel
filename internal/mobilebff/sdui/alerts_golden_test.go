package sdui_test

import (
	"server-control-panel/internal/mobilebff/screens"
	"server-control-panel/internal/mobilebff/sdui"
)

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

func init() {
	screens.RegisterAlerts(alertsGoldenBackend{}.deps())
}
