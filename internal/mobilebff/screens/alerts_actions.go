package screens

import (
	"context"
	"encoding/json"
	"strings"

	"server-control-panel/internal/mobilebff/sdui"
	"server-control-panel/internal/notify"
)

// Action ids the alerts.rules screen references from its table's row_actions
// and its form's submit_action.
const (
	alertsActionSave   = "alerts.rule.save"
	alertsActionDelete = "alerts.rule.delete"
)

// alertsAdminViewer is the RegisterAction authorize gate for both alert-rule
// mutations. A non-admin invocation — even one that bypasses the missing UI
// affordance by calling the endpoint directly — gets ErrActionNotFound at
// RunAction's step 2, before any handler runs (actionregistry.go), matching
// dockerAdminViewer's identical role for docker.prune.run.
func alertsAdminViewer(v sdui.Viewer) bool { return v.IsAdmin() }

// registerAlertsActions registers the two alerts.rules mutations. Called
// once by RegisterAlerts (alerts.go).
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
			// RequireTypedConfirmation deliberately empty — see
			// rule-delete-confirm's comment in alerts.go: a notification
			// rule is recreatable, not irreversible data loss.
		},
		alertsAdminViewer,
		handleAlertsRuleDelete(deps),
	)
}

// saveAlertRuleInput is the body alerts.rule.save decodes from ActionHandler's
// input. Field keys mirror rule-form's FormField.Key exactly (alerts.go).
type saveAlertRuleInput struct {
	ID        string `json:"id,omitempty"`
	Name      string `json:"name"`
	Condition string `json:"condition"`
	Threshold string `json:"threshold"`
	Channel   string `json:"channel"`
	Enabled   bool   `json:"enabled"`
}

// handleAlertsRuleSave implements alerts.rule.save. internal/notify.Router.
// UpsertRule performs NO field validation of its own (see
// internal/notify/store.go) — this handler is the only place mobile-path
// validation can happen, so it re-implements the same minimal checks the
// panel's own Alertas tab form enforces client-side: a required name, a
// condition drawn from the real event catalog (or the explicit wildcard), a
// recognized severity, and a channel that actually exists. Validating the
// channel matters specifically because an alert rule drives real
// notifications shared by every configured channel: a rule referencing a
// channel id that does not exist would silently never fire (a missed
// alert), and an unvalidated condition/threshold combination could just as
// easily fire on everything (alert spam) — see the threat model in
// PLAN.md's threat model.
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
			// ok
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

// handleAlertsRuleDelete implements alerts.rule.delete. Destructive
// confirmation itself is enforced by sdui.RunAction BEFORE this
// handler ever runs (actionregistry.go) — this handler looks the rule up
// first (via ListAlertRules, the only read seam AlertsDeps exposes) so the
// audit trail records the rule's name, not just its id, and so an unknown id
// gets the same ErrActionNotFound every other action in this package uses
// for "does not exist", rather than passing an arbitrary id straight to
// DeleteAlertRule.
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
