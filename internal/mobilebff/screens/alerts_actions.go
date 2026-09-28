package screens

import (
	"context"
	"encoding/json"
	"strings"

	"server-control-panel/internal/mobilebff/sdui"
	"server-control-panel/internal/notify"
)

const (
	alertsActionSave   = "alerts.rule.save"
	alertsActionDelete = "alerts.rule.delete"
)

func alertsAdminViewer(v sdui.Viewer) bool { return v.IsAdmin() }

func registerAlertsActions(deps AlertsDeps) {
	sdui.RegisterAction(
		sdui.ActionDescriptor{
			ActionID:   alertsActionSave,
			Method:     "POST",
			Endpoint:   "/api/mobile/v1/actions/" + alertsActionSave,
			Permission: "admin",
		},
		alertsAdminViewer,
		handleAlertsRuleSave(deps),
	)
	sdui.RegisterAction(
		sdui.ActionDescriptor{
			ActionID:    alertsActionDelete,
			Method:      "POST",
			Endpoint:    "/api/mobile/v1/actions/" + alertsActionDelete,
			Permission:  "admin",
			Destructive: true,
		},
		alertsAdminViewer,
		handleAlertsRuleDelete(deps),
	)
}

type saveAlertRuleInput struct {
	ID        string `json:"id,omitempty"`
	Name      string `json:"name"`
	Condition string `json:"condition"`
	Threshold string `json:"threshold"`
	Channel   string `json:"channel"`
	Enabled   bool   `json:"enabled"`
}

func handleAlertsRuleSave(deps AlertsDeps) sdui.ActionHandler {
	return func(_ context.Context, v sdui.Viewer, _ map[string]string, input json.RawMessage) (sdui.ActionResult, error) {
		var in saveAlertRuleInput
		if len(input) > 0 {
			if err := json.Unmarshal(input, &in); err != nil {
				return sdui.ActionResult{}, sdui.FieldErrors{}.Add("name", "invalid request body")
			}
		}

		var fe sdui.FieldErrors
		if strings.TrimSpace(in.Name) == "" {
			fe = fe.Add("name", "required")
		}

		conditionKnown := in.Condition == alertsAnyCondition
		if !conditionKnown {
			for _, e := range deps.EventOptions() {
				if e.Value == in.Condition {
					conditionKnown = true
					break
				}
			}
		}
		if !conditionKnown {
			fe = fe.Add("condition", "unknown condition")
		}

		switch in.Threshold {
		case notify.SeverityInfo, notify.SeverityWarning, notify.SeverityCritical:
		default:
			fe = fe.Add("threshold", "unknown severity")
		}

		channelKnown := false
		for _, c := range deps.ChannelOptions() {
			if c.Value == in.Channel {
				channelKnown = true
				break
			}
		}
		if strings.TrimSpace(in.Channel) == "" {
			fe = fe.Add("channel", "required")
		} else if !channelKnown {
			fe = fe.Add("channel", "channel not configured")
		}

		if fe != nil {
			return sdui.ActionResult{}, fe
		}

		channels := []string{}
		if in.Channel != "" {
			channels = []string{in.Channel}
		}
		rule := AlertRuleInput{
			ID:          in.ID,
			Name:        in.Name,
			Enabled:     in.Enabled,
			TypePrefix:  in.Condition,
			MinSeverity: in.Threshold,
			Channels:    channels,
		}

		saved, err := deps.SaveAlertRule(rule)
		if err != nil {
			return sdui.ActionResult{}, err
		}

		action := "alerts.rule.update"
		if in.ID == "" {
			action = "alerts.rule.create"
		}
		if deps.AuditEvent != nil {
			deps.AuditEvent(v.Username, action, saved.ID+":"+saved.Name)
		}

		return sdui.ActionResult{Invalidate: []string{"rules-table"}}, nil
	}
}

func handleAlertsRuleDelete(deps AlertsDeps) sdui.ActionHandler {
	return func(_ context.Context, v sdui.Viewer, params map[string]string, _ json.RawMessage) (sdui.ActionResult, error) {
		id := params["id"]

		var target *AlertRuleRow
		for _, r := range deps.ListAlertRules(v) {
			if r.ID == id {
				cp := r
				target = &cp
				break
			}
		}
		if target == nil {
			return sdui.ActionResult{}, sdui.ErrActionNotFound
		}

		if err := deps.DeleteAlertRule(id); err != nil {
			return sdui.ActionResult{}, err
		}

		if deps.AuditEvent != nil {
			deps.AuditEvent(v.Username, "alerts.rule.delete", target.ID+":"+target.Name)
		}

		return sdui.ActionResult{Invalidate: []string{"rules-table"}}, nil
	}
}
