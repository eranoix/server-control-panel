package mobilebff

// ops_health.go registers GET /ops/status: one aggregated,
// screen-shaped snapshot of server health, queue depth, currently-fired
// alert rules and machine resources (CPU/memory/disk/uptime/network, see
// ops_metrics.go — which also explains why the resources live HERE and not
// on a route of their own), admin-only. The same OpsStatus shape is also published live
// on the "ops.health" channel by events_bridge_ops.go's ticker whenever at
// least one connection is subscribed to it — one shape, two delivery paths,
// never two aggregation implementations.
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
		Summary:     "Saúde, fila, alertas ativos e recursos do servidor em uma chamada (admin)",
		Tags:        []string{"mobile", "ops"},
		Middlewares: huma.Middlewares{requireAuth},
		Errors:      []int{http.StatusForbidden},
	}, getOpsStatusHandler(deps))
}

// AlertSummary is a screen-shaped projection of metrics.RuleStatus — only
// the fields a mobile alert card needs (the BFF shapes, it never
// passes an internal struct through verbatim).
type AlertSummary struct {
	Name         string  `json:"name"`
	Severity     string  `json:"severity"`
	State        string  `json:"state"`
	CurrentValue float64 `json:"current_value"`
	Threshold    float64 `json:"threshold"`
	Unit         string  `json:"unit,omitempty"`
	FiredSince   int64   `json:"fired_since,omitempty"`
}

// OpsStatus is the body of GET /ops/status and the payload published live on
// the "ops.health" channel.
type OpsStatus struct {
	Health       map[string]string `json:"health"`
	HealthOK     bool              `json:"health_ok"`
	QueueRunning int               `json:"queue_running"`
	QueueQueued  int               `json:"queue_queued"`
	Alerts       []AlertSummary    `json:"alerts"`
	// System carries CPU/memory/disk/uptime/network — the resources the home
	// screen dashboard paints alongside health and the queue, in the SAME
	// call. A pointer with omitempty (and not a value): when deps.SysStats is
	// not wired (cmd/mobile-openapi-gen, tests that do not exercise
	// resources) or the collection fails, the field DISAPPEARS from the
	// serialized bytes instead of becoming a block of zeros the app would
	// paint as "CPU 0%, disk 0/0". See ops_metrics.go for shape, source and cost.
	System *SystemMetrics `json:"system,omitempty"`
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

// buildOpsStatus assembles OpsStatus from the existing subsystems
// (deps.HealthDetailed, deps.Queue.Counts, deps.Alerts.Status,
// deps.SysStats) — no health/alert/collection logic is re-derived here, only
// shaped for the mobile screen. Every field defaults to its zero value when
// the matching dep is nil, so this never panics (cmd/mobile-openapi-gen's
// empty Deps{} in particular).
//
// The ctx exists because of deps.SysStats, whose collection is cancellable and
// can block (see ops_metrics.go): an HTTP client that gives up must not leave a
// /proc sweep running, and the ops.health ticker passes a context with a
// deadline so that a stuck collection never stops the publisher.
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
