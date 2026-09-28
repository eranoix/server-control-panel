package mobilebff

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/httpx"
)

func init() { Register("ops_health", registerOpsHealth) }

func registerOpsHealth(api huma.API, deps Deps) {
	huma.Register(api, huma.Operation{
		OperationID: "getOpsStatus",
		Method:      http.MethodGet,
		Path:        "/ops/status",
		Summary:     "Server health, queue, active alerts and resources in one call (admin)",
		Tags:        []string{"mobile", "ops"},
		Middlewares: huma.Middlewares{requireAuth},
		Errors:      []int{http.StatusForbidden},
	}, getOpsStatusHandler(deps))
}

type AlertSummary struct {
	Name         string  `json:"name"`
	Severity     string  `json:"severity"`
	State        string  `json:"state"`
	CurrentValue float64 `json:"current_value"`
	Threshold    float64 `json:"threshold"`
	Unit         string  `json:"unit,omitempty"`
	FiredSince   int64   `json:"fired_since,omitempty"`
}

type OpsStatus struct {
	Health       map[string]string `json:"health"`
	HealthOK     bool              `json:"health_ok"`
	QueueRunning int               `json:"queue_running"`
	QueueQueued  int               `json:"queue_queued"`
	Alerts       []AlertSummary    `json:"alerts"`
	System       *SystemMetrics    `json:"system,omitempty"`
}

type opsStatusOutput struct {
	Body OpsStatus
}

func getOpsStatusHandler(deps Deps) func(ctx context.Context, in *struct{}) (*opsStatusOutput, error) {
	return func(ctx context.Context, _ *struct{}) (*opsStatusOutput, error) {
		user := auth.UserFromContext(ctx)
		if !httpx.IsAdmin(deps.Cfg, user) {
			return nil, huma.Error403Forbidden("only an admin can see the operational status")
		}
		return &opsStatusOutput{Body: buildOpsStatus(ctx, deps)}, nil
	}
}

func buildOpsStatus(ctx context.Context, deps Deps) OpsStatus {
	status := OpsStatus{Health: map[string]string{}, Alerts: []AlertSummary{}}
	if deps.HealthDetailed != nil {
		status.HealthOK, status.Health = deps.HealthDetailed()
	}
	if deps.Queue != nil {
		status.QueueRunning, status.QueueQueued = deps.Queue.Counts()
	}
	if deps.Alerts != nil {
		for _, rs := range deps.Alerts.Status() {
			if rs.State != "firing" {
				continue
			}
			status.Alerts = append(status.Alerts, AlertSummary{
				Name:         rs.Name,
				Severity:     rs.Severity,
				State:        rs.State,
				CurrentValue: rs.CurrentValue,
				Threshold:    rs.Threshold,
				Unit:         rs.Unit,
				FiredSince:   rs.PendingSince,
			})
		}
	}
	status.System = buildSystemMetrics(ctx, deps)
	return status
}
