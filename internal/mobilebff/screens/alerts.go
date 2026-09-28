package screens

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/config"
	"server-control-panel/internal/httpx"
	"server-control-panel/internal/mobilebff"
	"server-control-panel/internal/mobilebff/sdui"
	"server-control-panel/internal/notify"
)

const alertsRulesScreenID = "alerts.rules"

const alertsRulesRowsEndpoint = mobilebff.Prefix + "/alerts/rules"

const alertsAnyCondition = ""

func RegisterAlerts(deps AlertsDeps) {
	sdui.Register(alertsRulesScreenID, func(_ context.Context, v sdui.Viewer) (*sdui.Envelope, error) {
		return buildAlertsRulesScreenForViewer(deps, v)
	})
	sdui.RegisterCatalog(alertsRulesScreenID, sdui.GroupAutomation, "Alert rules", adminOnly)
	registerAlertsActions(deps)
	mobilebff.Register("alerts.rules.rows", func(api huma.API, mbDeps mobilebff.Deps) {
		registerAlertsRulesRows(api, deps, mbDeps)
	})
	sdui.RegisterForbiddenForNonAdmin(alertsRulesScreenID, func() []string {
		return []string{alertsActionSave, alertsActionDelete}
	})
}

func buildAlertsRulesScreenForViewer(deps AlertsDeps, v sdui.Viewer) (*sdui.Envelope, error) {
	if !v.IsAdmin() {
		return nil, sdui.ErrScreenNotFound
	}
	return buildAlertsRulesScreen(deps, v), nil
}

func buildAlertsRulesScreen(deps AlertsDeps, v sdui.Viewer) *sdui.Envelope {
	table := sdui.TableComponent{
		ComponentBase: sdui.ComponentBase{Type: sdui.ComponentTypeTable, ID: "rules-table"},
		Columns: []sdui.TableColumn{
			{Key: "name", Label: "Name", Kind: "text"},
			{Key: "condition", Label: "Condition", Kind: "text"},
			{Key: "channel", Label: "Channel", Kind: "text"},
			{Key: "enabled", Label: "Enabled", Kind: "bool"},
		},
		RowsSource: sdui.DataSource{Endpoint: alertsRulesRowsEndpoint},
		RowActions: []sdui.ActionRef{
			{ActionID: alertsActionDelete, Label: "Delete", Style: "destructive"},
		},
		EmptyState: &sdui.EmptyState{Text: "Alert rules decide what notifies you and through which channel: high CPU, a failed deploy, a filling disk. Empty is the factory state — until there is a rule, no event becomes a notification. Build the first one in the form below (condition, minimum severity and channel) and save it."},
	}

	eventOptions := deps.EventOptions()
	conditionValues := make([]string, 0, len(eventOptions)+1)
	conditionValues = append(conditionValues, alertsAnyCondition)
	for _, e := range eventOptions {
		conditionValues = append(conditionValues, e.Value)
	}

	channelOptions := deps.ChannelOptions()
	channelValues := make([]string, len(channelOptions))
	for i, c := range channelOptions {
		channelValues[i] = c.Value
	}

	form := sdui.FormComponent{
		ComponentBase: sdui.ComponentBase{Type: sdui.ComponentTypeForm, ID: "rule-form"},
		Fields: []sdui.FormField{
			{Key: "name", Label: "Name", Kind: "text", Required: true},
			{Key: "condition", Label: "Condition", Kind: "select", Required: true, Options: conditionValues},
			{Key: "threshold", Label: "Minimum severity", Kind: "select", Required: true,
				Options: []string{notify.SeverityInfo, notify.SeverityWarning, notify.SeverityCritical}},
			{Key: "channel", Label: "Notification channel", Kind: "select", Required: true, Options: channelValues},
			{Key: "enabled", Label: "Enabled", Kind: "bool"},
		},
		SubmitAction: sdui.ActionRef{ActionID: alertsActionSave, Label: "Save", Style: "primary"},
	}

	confirm := sdui.ConfirmDestructiveComponent{
		ComponentBase: sdui.ComponentBase{Type: sdui.ComponentTypeConfirmDestructive, ID: "rule-delete-confirm"},
		ActionID:      alertsActionDelete,
		Message:       "This alert rule will be deleted. The events it covered stop notifying until a new rule covers them.",
	}

	screen := sdui.Screen{
		ID:         alertsRulesScreenID,
		Title:      "Alerts",
		Components: []sdui.Component{table, form, confirm},
	}
	_ = v
	return &sdui.Envelope{Screen: screen}
}

func registerAlertsRulesRows(api huma.API, deps AlertsDeps, mbDeps mobilebff.Deps) {
	cfg := mbDeps.Cfg
	huma.Register(api, huma.Operation{
		OperationID: "getAlertsRuleRows",
		Method:      http.MethodGet,
		Path:        "/alerts/rules",
		Summary:     "Rows of the alerts.rules table (admin only)",
		Tags:        []string{"mobile", "sdui", "alerts"},
		Middlewares: huma.Middlewares{mobilebff.RequireAuth, serveAlertsRulesRows(cfg, deps)},
	}, alertsRulesRowsDocHandler)
}

type alertsRulesRowsInput struct{}

type alertsRulesRowsOutput struct {
	Body json.RawMessage
}

func alertsRulesRowsDocHandler(ctx context.Context, in *alertsRulesRowsInput) (*alertsRulesRowsOutput, error) {
	return &alertsRulesRowsOutput{Body: json.RawMessage(`{"rows":[]}`)}, nil
}

func serveAlertsRulesRows(cfg *config.Config, deps AlertsDeps) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		req, w := humago.Unwrap(ctx)

		username := auth.UserFrom(req)
		v := sdui.ViewerFrom(cfg, username)
		if !v.IsAdmin() {
			httpx.WriteErr(w, http.StatusNotFound, "not_found")
			return
		}

		rules := deps.ListAlertRules(v)
		rows := make([]map[string]any, 0, len(rules))
		for _, r := range rules {
			rows = append(rows, alertsRuleRow(r))
		}

		body, err := json.Marshal(map[string]any{"rows": rows})
		if err != nil {
			log.Printf("mobilebff/screens: error serializing the rows of %q: %v", alertsRulesScreenID, err)
			httpx.WriteErr(w, http.StatusInternalServerError, "internal_error")
			return
		}

		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}
}

func alertsRuleRow(r AlertRuleRow) map[string]any {
	condition := r.TypePrefix
	if condition == alertsAnyCondition {
		condition = "Any event"
	}
	return map[string]any{
		"id":        r.ID,
		"name":      r.Name,
		"condition": condition,
		"channel":   strings.Join(r.Channels, ", "),
		"enabled":   r.Enabled,
	}
}
