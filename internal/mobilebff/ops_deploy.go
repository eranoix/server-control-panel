package mobilebff

// ops_deploy.go registers the deploy trigger/status endpoints:
// POST /ops/deploy enqueues the existing self_deploy queue job
// (internal/queue/runners_selfdeploy.go), which runs the operator's deploy
// command. This file never runs a deploy itself and never re-derives any of
// that command's build, health-gate or rollback steps. GET /ops/deploy/{jobID} gives
// the screen its initial paint before the /ws/mobile-events subscription
// (events_bridge_ops.go) catches up, mirroring why handleQueueWS also
// replays last-known status on connect.
import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/httpx"
)

func init() { Register("ops_deploy", registerOpsDeploy) }

func registerOpsDeploy(api huma.API, deps Deps) {
	huma.Register(api, huma.Operation{
		OperationID: "triggerSelfDeploy",
		Method:      http.MethodPost,
		Path:        "/ops/deploy",
		Summary:     "Triggers the configured deploy command (primary only, confirmation required)",
		Tags:        []string{"mobile", "ops"},
		Middlewares: huma.Middlewares{requireAuth},
		Errors:      []int{http.StatusBadRequest, http.StatusForbidden, http.StatusServiceUnavailable},
		// SkipValidateBody: the schema still marks `confirm` required (so the
		// generated Kotlin client has a non-optional Boolean, not one a caller
		// can silently forget) but huma's own required-field validator would
		// reject an ABSENT confirm with a generic 422 before this handler's
		// own truthiness check ever ran — and a bool has no way to distinguish
		// "absent" from "false" on the wire either way. Skipping schema
		// validation for the body lets the handler give ONE consistent 400
		// message for both an absent field and an explicit `false`, while the
		// schema contract (required) stays honest for codegen.
		SkipValidateBody: true,
	}, triggerSelfDeployHandler(deps))

	huma.Register(api, huma.Operation{
		OperationID: "getSelfDeployStatus",
		Method:      http.MethodGet,
		Path:        "/ops/deploy/{jobID}",
		Summary:     "Current status of a self_deploy job (initial paint before the WS)",
		Tags:        []string{"mobile", "ops"},
		Middlewares: huma.Middlewares{requireAuth},
		Errors:      []int{http.StatusForbidden, http.StatusNotFound, http.StatusServiceUnavailable},
	}, getSelfDeployStatusHandler(deps))
}

// triggerDeployRequest is the body of POST /ops/deploy. Confirm must be
// LITERALLY true — this is the server-side half of the explicit
// confirmation requirement (belt-and-suspenders alongside the primary-only
// gate below): a client with no confirmation dialog, or one that omits or
// defaults the field, is refused with 400 before self_deploy is ever
// enqueued. Plan 06-04 builds the client-side dialog that produces this.
type triggerDeployRequest struct {
	// Confirm has NO omitempty: the generated OpenAPI schema must mark this
	// field required (huma treats every field as required unless omitempty/
	// omitzero/an explicit `required:"false"` tag says otherwise), so the
	// Kotlin client generated from mobile-v1.yaml sees a non-optional
	// Boolean and a caller that forgets to pass it fails to COMPILE, not
	// silently at runtime. The operation sets SkipValidateBody so huma's own
	// required-field body validator doesn't intercept an absent field with a
	// generic 422 before this handler's explicit truthiness check runs below
	// — that check is what actually enforces "must be true", for both an
	// absent field and an explicit false, with one consistent 400 message.
	Confirm bool `json:"confirm"`
}

type triggerDeployInput struct {
	Body triggerDeployRequest
}

type triggerDeployOutput struct {
	Body struct {
		JobID string `json:"job_id"`
	}
}

func triggerSelfDeployHandler(deps Deps) func(ctx context.Context, in *triggerDeployInput) (*triggerDeployOutput, error) {
	return func(ctx context.Context, in *triggerDeployInput) (*triggerDeployOutput, error) {
		if !in.Body.Confirm {
			return nil, huma.Error400BadRequest("confirm must be true — the app must show an explicit confirmation before calling this endpoint")
		}
		user := auth.UserFromContext(ctx)
		// Primary-only: a deploy restarts the running binary — same RBAC
		// tier as RebootRunner/AptUpgradeRunner. Checked here
		// AND independently by SelfDeployRunner.AuthorizedFor before the
		// queue ever runs it.
		if !httpx.IsAdmin(deps.Cfg, user) {
			return nil, huma.Error403Forbidden("only the primary account can trigger a deploy")
		}
		if deps.Queue == nil {
			return nil, huma.Error503ServiceUnavailable("job queue unavailable")
		}
		job, err := deps.Queue.Enqueue("self_deploy", nil, user, "mobile")
		if err != nil {
			return nil, huma.Error500InternalServerError(err.Error())
		}
		bridgeSelfDeployJob(deps, job.ID, user)
		out := &triggerDeployOutput{}
		out.Body.JobID = job.ID
		return out, nil
	}
}

type deployStatusInput struct {
	JobID string `path:"jobID"`
}

type deployStatusOutput struct {
	Body struct {
		Status   string `json:"status"`
		Progress int    `json:"progress"`
		Step     string `json:"step,omitempty"`
		Error    string `json:"error,omitempty"`
	}
}

func getSelfDeployStatusHandler(deps Deps) func(ctx context.Context, in *deployStatusInput) (*deployStatusOutput, error) {
	return func(ctx context.Context, in *deployStatusInput) (*deployStatusOutput, error) {
		user := auth.UserFromContext(ctx)
		if !httpx.IsAdmin(deps.Cfg, user) {
			return nil, huma.Error403Forbidden("only the primary account can see a deploy's status")
		}
		if deps.Queue == nil {
			return nil, huma.Error503ServiceUnavailable("job queue unavailable")
		}
		job, err := deps.Queue.Get(in.JobID)
		if err != nil || job.Kind != "self_deploy" {
			return nil, huma.Error404NotFound("job not found")
		}
		out := &deployStatusOutput{}
		out.Body.Status = string(job.Status)
		out.Body.Progress = job.Progress
		out.Body.Step = job.Step
		out.Body.Error = job.Error
		return out, nil
	}
}
