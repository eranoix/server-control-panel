package screens

import (
	"context"
	"encoding/json"
	"strings"

	"server-control-panel/internal/mobilebff/sdui"
	"server-control-panel/internal/scheduler"
)

// Action ids the scheduler.jobs screen references from its table's
// row_actions, its form's submit_action and its standalone action — the
// only four mutations this screen exposes.
const (
	schedulerActionSave    = "scheduler.job.save"
	schedulerActionRunNow  = "scheduler.job.run_now"
	schedulerActionDelete  = "scheduler.job.delete"
	schedulerActionRefresh = "scheduler.jobs.refresh"
)

// authenticatedViewer is the RegisterAction authorize gate every scheduler
// action below shares: any signed-in user may attempt these actions — the
// real access control (ownership, kind authorization, admin-only fields) is
// enforced inside each handler, exactly mirroring internal/api/
// handlers_scheduler.go, which likewise gates on "authenticated" at the top
// and checks ownership/kind per operation, not per route.
func authenticatedViewer(v sdui.Viewer) bool { return v.Username != "" }

// registerSchedulerActions registers the four scheduler.jobs mutations.
// Called once by Register (deps.go) — RegisterAction panics on a duplicate
// ActionID, so this must never run twice against the same process registry.
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
			// RequireTypedConfirmation deliberately empty — see the
			// comment in scheduler.go about job-delete-confirm: a scheduler
			// job is recreatable, not a genuinely irreversible loss of
			// data, so the simple confirmation
			// (Destructive:true, no typed text) is already proportionate.
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

// saveJobInput is the body scheduler.job.save decodes from ActionHandler's
// input. RunAsRoot is a *bool (not bool) so the handler can tell "the client
// never offered this field" (nil — e.g. a non-admin's form, which drops
// run_as_root entirely, see scheduler.go) apart from "the client explicitly
// sent false". Without that distinction, a non-admin editing any other field
// of an admin-created run_as_root job would silently flip it back to false
// on every save, because the decoded zero value and an explicit "turn it
// off" are indistinguishable through a plain bool.
type saveJobInput struct {
	ID        string `json:"id,omitempty"`
	Name      string `json:"name"`
	Schedule  string `json:"schedule"`
	Kind      string `json:"kind"`
	Enabled   bool   `json:"enabled"`
	RunAsRoot *bool  `json:"run_as_root,omitempty"`
}

// handleSchedulerJobSave implements scheduler.job.save. It never
// duplicates internal/scheduler's own validation (cron parsing, required
// name/kind) — those still run inside deps.SaveJob (scheduler.Scheduler.Save)
// as the authoritative check. What this handler adds is attributing a
// rejection to the FormField.Key that caused it, by re-running the same
// cheap checks Save runs BEFORE calling it, so a 422 can point at "schedule"
// or "kind" instead of a single opaque form-level message.
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
				// A nonexistent job and another user's job fall into the same
				// ErrActionNotFound — 404-never-403, the same stance as
				// ErrScreenNotFound/ErrActionNotFound (see actionregistry.go).
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
			// Save only fails here on a condition the pre-checks above
			// should already have caught (a rare race, a cron that passed
			// NextFires but failed some other way in Save). It is not
			// FieldErrors because there is no safe FormField.Key to carry
			// it on without violating MatchesForm's contract — it becomes a
			// generic error, which handlers_actions.go already treats as a
			// 500 without leaking err.Error() to the client.
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

// handleSchedulerJobRunNow implements scheduler.job.run_now. Re-checks
// ownership AND kind authorization on every call — cur.Kind may have become
// primary-only since the job was saved, so the
// create/update-time gate is not enough on its own, exactly the reasoning
// internal/api/handlers_scheduler.go's run-now branch already documents.
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
			// Unlikely (the job has just run) — falls back to the
			// pre-execution snapshot instead of failing the whole action
			// over a display detail.
			updated = cur
		}
		return sdui.ActionResult{Patch: schedulerJobRow(updated, v.IsAdmin())}, nil
	}
}

// handleSchedulerJobDelete implements scheduler.job.delete. Destructive
// confirmation itself is enforced by sdui.RunAction BEFORE this
// handler ever runs (see actionregistry.go) — this handler only needs its
// own ownership check, exactly like every other per-job action here.
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

// handleSchedulerJobsRefresh implements scheduler.jobs.refresh — a pure
// re-fetch signal with no domain effect, so it is deliberately not audited:
// auditing a no-op read would only add noise to a trail meant to record
// (the concern is mutations becoming invisible, not reads).
func handleSchedulerJobsRefresh() sdui.ActionHandler {
	return func(_ context.Context, _ sdui.Viewer, _ map[string]string, _ json.RawMessage) (sdui.ActionResult, error) {
		return sdui.ActionResult{Invalidate: []string{"jobs-table"}}, nil
	}
}
