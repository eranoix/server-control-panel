// alerts.go registers alerts.rules — the notification-routing rule editor
// SDUI screen. This screen adapts internal/notify.Router (Rule/
// UpsertRule/DeleteRule/ChannelDefsRedacted), NOT internal/metrics.Engine's
// threshold rules: metrics.Engine has no Channels field and its HTTP
// handlers (internal/api/handlers_alerting.go) enforce zero RBAC on any
// authenticated caller, while internal/notify's handlers —
// handleNotifyRules/handleNotifyRuleDelete in
// internal/api/handlers_notify.go — are mustPrimary-gated on every method,
// including GET. That real, end-to-end admin gate is exactly why this
// screen is whole-screen admin-only (buildAlertsRulesScreenForViewer below
// returns sdui.ErrScreenNotFound for a non-admin, the same 404-never-403
// posture docker.prune and every Security screen already use) rather than a
// per-row/per-field RBAC-by-omission screen like scheduler.jobs.
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

// alertsRulesScreenID is the id the app requests via
// GET /api/mobile/v1/screens/{id}.
const alertsRulesScreenID = "alerts.rules"

// alertsRulesRowsEndpoint is the absolute path the rules-table rows_source
// resolves to, matching every other table screen's DataSource.Endpoint
// convention (see scheduler.go).
const alertsRulesRowsEndpoint = mobilebff.Prefix + "/alerts/rules"

// alertsAnyCondition is the wildcard value the "condition" form field offers
// alongside the real event-type catalog (deps.EventOptions) — an empty
// notify.Rule.TypePrefix legitimately matches every event type (see
// internal/notify/rule.go's matches doc comment), so this is a real,
// explicitly-chosen option, never a smuggled default.
const alertsAnyCondition = ""

// RegisterAlerts wires the alerts.rules screen, its two actions and its rows
// endpoint. Called explicitly by internal/api/api.go, mirroring
// scheduler.go's Register / docker.go's RegisterDocker.
func RegisterAlerts(deps AlertsDeps) {
	sdui.Register(alertsRulesScreenID, func(_ context.Context, v sdui.Viewer) (*sdui.Envelope, error) {
		return buildAlertsRulesScreenForViewer(deps, v)
	})
	// Catalog entry: adminOnly, mirroring the gate in
	// buildAlertsRulesScreenForViewer. "Alert rules" and not just
	// "Alerts" — the screen edits the RULES, it does not list fired alerts,
	// and the label has to say so for whoever reads it outside the group.
	sdui.RegisterCatalog(alertsRulesScreenID, sdui.GroupAutomation, "Alert rules", adminOnly)
	registerAlertsActions(deps)
	mobilebff.Register("alerts.rules.rows", func(api huma.API, mbDeps mobilebff.Deps) {
		registerAlertsRulesRows(api, deps, mbDeps)
	})
	// The whole screen is admin-only (ErrScreenNotFound below), so there is
	// no non-admin envelope to omit anything FROM — this forbidden set only
	// needs to cover the case where the golden harness still probes these
	// action ids directly against a non-admin viewer (mirrors
	// docker.prune's identical reasoning in docker.go).
	sdui.RegisterForbiddenForNonAdmin(alertsRulesScreenID, func() []string {
		return []string{alertsActionSave, alertsActionDelete}
	})
}

// buildAlertsRulesScreenForViewer applies the admin-only gate — a non-admin
// Build call returns sdui.ErrScreenNotFound, never an emptied-but-present
// envelope. Split out, like buildDockerPruneScreenForViewer, so tests can
// exercise the gate directly without going through the global registry.
func buildAlertsRulesScreenForViewer(deps AlertsDeps, v sdui.Viewer) (*sdui.Envelope, error) {
	if !v.IsAdmin() {
		return nil, sdui.ErrScreenNotFound
	}
	return buildAlertsRulesScreen(deps, v), nil
}

// buildAlertsRulesScreen builds the table+form+confirm_destructive shape
// this phase has used repeatedly (closest analog: docker.go's containers
// screen — a table with a destructive row action plus a create/edit form,
// per PLAN.md's own guidance).
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
			// There is deliberately no "edit" row action: exactly like
			// scheduler.job.save/security.user.save, editing is the
			// client's generic tap-row-to-populate-form behavior feeding
			// alerts.rule.save, which branches create-vs-edit on ID
			// presence alone (see alerts_actions.go).
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
			// "name" is not in PLAN.md's literal field list (condition,
			// threshold, channel, enabled) but is added here under
			// deviation Rule 2: notify.Rule.Name is a real, persisted
			// field, and a rule saved with no name is indistinguishable
			// from every other unnamed rule in the panel's own Alertas
			// tab — an operational correctness gap for a domain whose
			// whole point is knowing which rule did what.
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
		// RequireTypedConfirmation deliberately empty — a notification rule
		// is a recreatable config object, not irreversible data loss, same
		// reasoning as scheduler.job.delete (see scheduler.go).
	}

	screen := sdui.Screen{
		ID:         alertsRulesScreenID,
		Title:      "Alerts",
		Components: []sdui.Component{table, form, confirm},
	}
	_ = v // admin-only screen: v is not consulted further inside the builder
	return &sdui.Envelope{Screen: screen}
}

// registerAlertsRulesRows registers GET /alerts/rules — the rows_source the
// rules-table binds to. Admin-only, mirroring
// security.go's serveSecuritySessionsRows exactly (httpx.WriteErr 404 for a
// non-admin caller, never a filtered-but-served row set), since this screen
// has no non-admin view at all.
func registerAlertsRulesRows(api huma.API, deps AlertsDeps, mbDeps mobilebff.Deps) {
	cfg := mbDeps.Cfg
	huma.Register(api, huma.Operation{
		OperationID: "getAlertsRuleRows",
		Method:      http.MethodGet,
		Path:        "/alerts/rules",
		Summary:     "Linhas da tabela alerts.rules — somente admin",
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

// alertsRuleRow shapes one AlertRuleRow into the row wire format:
// {"id","name","condition","channel","enabled"}. condition renders the
// wildcard TypePrefix as display text (never a raw empty string, which would
// look like a rendering bug rather than an intentional "any event" choice).
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
