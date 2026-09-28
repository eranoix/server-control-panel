package screens

import (
	"context"
	"encoding/json"
	"strings"

	"server-control-panel/internal/mobilebff/sdui"
	"server-control-panel/internal/scheduler"
)

const (
	schedulerActionSave    = "scheduler.job.save"
	schedulerActionRunNow  = "scheduler.job.run_now"
	schedulerActionDelete  = "scheduler.job.delete"
	schedulerActionRefresh = "scheduler.jobs.refresh"
)

func authenticatedViewer(v sdui.Viewer) bool { return v.Username != "" }

func registerSchedulerActions(deps SchedulerDeps) {
	sdui.RegisterAction(
		sdui.ActionDescriptor{
			ActionID:   schedulerActionSave,
			Method:     "POST",
			Endpoint:   "/api/mobile/v1/actions/" + schedulerActionSave,
			Permission: "authenticated",
		},
		authenticatedViewer,
		handleSchedulerJobSave(deps),
	)
	sdui.RegisterAction(
		sdui.ActionDescriptor{
			ActionID:   schedulerActionRunNow,
			Method:     "POST",
			Endpoint:   "/api/mobile/v1/actions/" + schedulerActionRunNow,
			Permission: "authenticated",
		},
		authenticatedViewer,
		handleSchedulerJobRunNow(deps),
	)
	sdui.RegisterAction(
		sdui.ActionDescriptor{
			ActionID:    schedulerActionDelete,
			Method:      "POST",
			Endpoint:    "/api/mobile/v1/actions/" + schedulerActionDelete,
			Permission:  "authenticated",
			Destructive: true,
		},
		authenticatedViewer,
		handleSchedulerJobDelete(deps),
	)
	sdui.RegisterAction(
		sdui.ActionDescriptor{
			ActionID:   schedulerActionRefresh,
			Method:     "POST",
			Endpoint:   "/api/mobile/v1/actions/" + schedulerActionRefresh,
			Permission: "authenticated",
		},
		authenticatedViewer,
		handleSchedulerJobsRefresh(),
	)
}

type saveJobInput struct {
	ID        string `json:"id,omitempty"`
	Name      string `json:"name"`
	Schedule  string `json:"schedule"`
	Kind      string `json:"kind"`
	Enabled   bool   `json:"enabled"`
	RunAsRoot *bool  `json:"run_as_root,omitempty"`
}

func handleSchedulerJobSave(deps SchedulerDeps) sdui.ActionHandler {
	return func(_ context.Context, v sdui.Viewer, _ map[string]string, input json.RawMessage) (sdui.ActionResult, error) {
		var in saveJobInput
		if len(input) > 0 {
			if err := json.Unmarshal(input, &in); err != nil {
				return sdui.ActionResult{}, sdui.FieldErrors{}.Add("name", "invalid request body")
			}
		}

		var cur *scheduler.Job
		if in.ID != "" {
			existing, err := deps.GetJob(in.ID)
			if err != nil {
				return sdui.ActionResult{}, sdui.ErrActionNotFound
			}
			if !v.IsAdmin() && existing.Owner != v.Username {
				return sdui.ActionResult{}, sdui.ErrActionNotFound
			}
			cur = existing
		}

		var fe sdui.FieldErrors
		if strings.TrimSpace(in.Name) == "" {
			fe = fe.Add("name", "required")
		}
		if strings.TrimSpace(in.Schedule) == "" {
			fe = fe.Add("schedule", "required")
		} else if _, err := deps.NextFires(in.Schedule, 1); err != nil {
			fe = fe.Add("schedule", "invalid cron expression")
		}

		kindAuthorized := false
		for _, k := range deps.AuthorizedKinds(v.Username, v.IsAdmin()) {
			if k.Value == in.Kind {
				kindAuthorized = true
				break
			}
		}
		if !kindAuthorized {
			fe = fe.Add("kind", "kind not available to this user")
		}

		runAsRoot := false
		if cur != nil {
			runAsRoot = cur.RunAsRoot
		}
		if in.RunAsRoot != nil {
			if *in.RunAsRoot && !v.IsAdmin() {
				fe = fe.Add("run_as_root", "only administrators can run as root")
				if deps.AuditEvent != nil {
					target := in.Name
					if cur != nil {
						target = cur.ID + ":" + cur.Name
					}
					deps.AuditEvent(v.Username, "scheduler.run_as_root_denied", target)
				}
			} else {
				runAsRoot = *in.RunAsRoot
			}
		}

		if fe != nil {
			return sdui.ActionResult{}, fe
		}

		job := scheduler.Job{
			ID:        in.ID,
			Name:      in.Name,
			Schedule:  in.Schedule,
			Kind:      in.Kind,
			Enabled:   in.Enabled,
			RunAsRoot: runAsRoot,
		}
		if cur != nil {
			job.Owner = cur.Owner
			job.Created = cur.Created
		} else {
			job.Owner = v.Username
		}

		saved, err := deps.SaveJob(job)
		if err != nil {
			return sdui.ActionResult{}, err
		}

		action := "scheduler.update"
		if cur == nil {
			action = "scheduler.create"
		}
		if deps.AuditEvent != nil {
			deps.AuditEvent(v.Username, action, saved.ID+":"+saved.Name)
		}

		return sdui.ActionResult{Invalidate: []string{"jobs-table"}}, nil
	}
}

func handleSchedulerJobRunNow(deps SchedulerDeps) sdui.ActionHandler {
	return func(_ context.Context, v sdui.Viewer, params map[string]string, _ json.RawMessage) (sdui.ActionResult, error) {
		id := params["id"]
		cur, err := deps.GetJob(id)
		if err != nil {
			return sdui.ActionResult{}, sdui.ErrActionNotFound
		}
		if !v.IsAdmin() && cur.Owner != v.Username {
			return sdui.ActionResult{}, sdui.ErrActionNotFound
		}

		kindAuthorized := false
		for _, k := range deps.AuthorizedKinds(v.Username, v.IsAdmin()) {
			if k.Value == cur.Kind {
				kindAuthorized = true
				break
			}
		}
		if !kindAuthorized {
			return sdui.ActionResult{}, sdui.ErrActionNotFound
		}

		qid, err := deps.RunNow(id)
		if err != nil {
			return sdui.ActionResult{}, err
		}

		if deps.AuditEvent != nil {
			deps.AuditEvent(v.Username, "scheduler.run_now", id+" -> "+qid)
		}

		updated, err := deps.GetJob(id)
		if err != nil {
			updated = cur
		}
		return sdui.ActionResult{Patch: schedulerJobRow(updated, v.IsAdmin())}, nil
	}
}

func handleSchedulerJobDelete(deps SchedulerDeps) sdui.ActionHandler {
	return func(_ context.Context, v sdui.Viewer, params map[string]string, _ json.RawMessage) (sdui.ActionResult, error) {
		id := params["id"]
		cur, err := deps.GetJob(id)
		if err != nil {
			return sdui.ActionResult{}, sdui.ErrActionNotFound
		}
		if !v.IsAdmin() && cur.Owner != v.Username {
			return sdui.ActionResult{}, sdui.ErrActionNotFound
		}

		if err := deps.DeleteJob(id); err != nil {
			return sdui.ActionResult{}, err
		}

		if deps.AuditEvent != nil {
			deps.AuditEvent(v.Username, "scheduler.delete", cur.ID+":"+cur.Name)
		}

		return sdui.ActionResult{Invalidate: []string{"jobs-table"}}, nil
	}
}

func handleSchedulerJobsRefresh() sdui.ActionHandler {
	return func(_ context.Context, _ sdui.Viewer, _ map[string]string, _ json.RawMessage) (sdui.ActionResult, error) {
		return sdui.ActionResult{Invalidate: []string{"jobs-table"}}, nil
	}
}
