package mobilebff

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
		OperationID:      "triggerSelfDeploy",
		Method:           http.MethodPost,
		Path:             "/ops/deploy",
		Summary:          "Triggers the configured deploy command (primary only, confirmation required)",
		Tags:             []string{"mobile", "ops"},
		Middlewares:      huma.Middlewares{requireAuth},
		Errors:           []int{http.StatusBadRequest, http.StatusForbidden, http.StatusServiceUnavailable},
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

type triggerDeployRequest struct {
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
