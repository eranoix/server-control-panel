package screens

import (
	"context"
	"encoding/json"

	"server-control-panel/internal/aimodel"
	"server-control-panel/internal/config"
	"server-control-panel/internal/deploy"
	"server-control-panel/internal/mobilebff/sdui"
)

// Action ids the four screens in this file reference from their tables'
// row_actions and forms' submit_action.
const (
	miscActionAISettingsSave = "ai.settings.save"

	miscActionJiraConnect         = "jira.connect"
	miscActionJiraIssueSelect     = "jira.issue.select"
	miscActionJiraIssueTransition = "jira.issue.transition"
	miscActionJiraIssueComment    = "jira.issue.comment"

	miscActionDeployAppCreate   = "deploy.app.create"
	miscActionDeployAppRedeploy = "deploy.app.redeploy"
	miscActionDeployAppDelete   = "deploy.app.delete"

	miscActionQueueJobRerun  = "queue.job.retry"
	miscActionQueueJobCancel = "queue.job.cancel"
)

// miscAdminViewer is the RegisterAction authorize gate for every admin-only
// mutation in this file (ai.settings.save, all three deploy.app.* actions):
// mirrors handleAIModelsConfig's r.isPrimary and handlers_deploy.go's
// r.mustPrimary exactly. A non-admin direct invocation gets
// ErrActionNotFound at RunAction's step 2, before the handler ever runs
func miscAdminViewer(v sdui.Viewer) bool { return v.IsAdmin() }

// registerMiscActions registers the ten mutations fanned out by this file.
// Called once by RegisterMisc (misc.go).
func registerMiscActions(deps MiscDeps) {
	sdui.RegisterAction(
		sdui.ActionDescriptor{
			ActionID:   miscActionAISettingsSave,
			Method:     "POST",
			Endpoint:   "/api/mobile/v1/actions/" + miscActionAISettingsSave,
			Permission: "admin",
		},
		miscAdminViewer,
		handleAISettingsSave(deps),
	)

	sdui.RegisterAction(
		sdui.ActionDescriptor{
			ActionID:   miscActionJiraConnect,
			Method:     "POST",
			Endpoint:   "/api/mobile/v1/actions/" + miscActionJiraConnect,
			Permission: "authenticated",
		},
		authenticatedViewer,
		handleJiraConnect(deps),
	)
	sdui.RegisterAction(
		sdui.ActionDescriptor{
			ActionID:   miscActionJiraIssueSelect,
			Method:     "POST",
			Endpoint:   "/api/mobile/v1/actions/" + miscActionJiraIssueSelect,
			Permission: "authenticated",
		},
		authenticatedViewer,
		handleJiraIssueSelect(),
	)
	sdui.RegisterAction(
		sdui.ActionDescriptor{
			ActionID:   miscActionJiraIssueTransition,
			Method:     "POST",
			Endpoint:   "/api/mobile/v1/actions/" + miscActionJiraIssueTransition,
			Permission: "authenticated",
		},
		authenticatedViewer,
		handleJiraIssueTransition(deps),
	)
	sdui.RegisterAction(
		sdui.ActionDescriptor{
			ActionID:   miscActionJiraIssueComment,
			Method:     "POST",
			Endpoint:   "/api/mobile/v1/actions/" + miscActionJiraIssueComment,
			Permission: "authenticated",
		},
		authenticatedViewer,
		handleJiraIssueComment(deps),
	)

	sdui.RegisterAction(
		sdui.ActionDescriptor{
			ActionID:   miscActionDeployAppCreate,
			Method:     "POST",
			Endpoint:   "/api/mobile/v1/actions/" + miscActionDeployAppCreate,
			Permission: "admin",
		},
		miscAdminViewer,
		handleDeployAppCreate(deps),
	)
	sdui.RegisterAction(
		sdui.ActionDescriptor{
			ActionID:   miscActionDeployAppRedeploy,
			Method:     "POST",
			Endpoint:   "/api/mobile/v1/actions/" + miscActionDeployAppRedeploy,
			Permission: "admin",
			// Non-destructive deliberately: the must-haves list
			// only "delete a deployed app" and "cancel a running
			// job" as requiring confirm_destructive. Redeploying erases no
			// state — it merely enqueues a new deploy.Spec
			// via deps.TriggerRedeploy (which delegates to r.queue.Enqueue,
			// the same mechanics as handlers_deploy.go's enqueueDeploy), so
			// it sits at the same rank as the five systemd verbs in
			// system_actions.go (admin, with no extra confirmation).
		},
		miscAdminViewer,
		handleDeployAppRedeploy(deps),
	)
	sdui.RegisterAction(
		sdui.ActionDescriptor{
			ActionID:    miscActionDeployAppDelete,
			Method:      "POST",
			Endpoint:    "/api/mobile/v1/actions/" + miscActionDeployAppDelete,
			Permission:  "admin",
			Destructive: true,
			// Destructive:true is the real SERVER-SIDE gate (see
			// RunAction's step 3) — the ConfirmDestructiveComponent
			// in misc.go is only the UI half of the same rule. Without
			// confirm.Confirmed, RunAction never gets as far as calling
			// handleDeployAppDelete, so deps.DestroyDeployApp runs zero
			// times.
		},
		miscAdminViewer,
		handleDeployAppDelete(deps),
	)

	sdui.RegisterAction(
		sdui.ActionDescriptor{
			ActionID:   miscActionQueueJobRerun,
			Method:     "POST",
			Endpoint:   "/api/mobile/v1/actions/" + miscActionQueueJobRerun,
			Permission: "authenticated",
			// Not admin-only via the binary gate: any authenticated user
			// can retry THEIR OWN job. The ownership check
			// (job.Owner == v.Username, or admin) and the per-kind
			// permission check (deps.AuthorizedForRerun) live inside the
			// handler itself — RunAction's authorize gate cannot express
			// "owner of the resource", only an isolated Viewer (see
			// deps.go's MiscDeps doc comment).
		},
		authenticatedViewer,
		handleQueueJobRerun(deps),
	)
	sdui.RegisterAction(
		sdui.ActionDescriptor{
			ActionID:    miscActionQueueJobCancel,
			Method:      "POST",
			Endpoint:    "/api/mobile/v1/actions/" + miscActionQueueJobCancel,
			Permission:  "authenticated",
			Destructive: true,
			// The same server-side gate as deploy.app.delete.
			// Also not admin-only: cancelling YOUR OWN job is allowed to
			// any authenticated user, with the ownership check inside the
			// handler (see handleQueueJobCancel) — the same asymmetry
			// that handlers_queue.go's handleQueueByID's "cancel" branch
			// already has today (ownership alone, no extra AuthorizedFor).
		},
		authenticatedViewer,
		handleQueueJobCancel(deps),
	)
}

// --- ai.settings --------------------------------------------------------

type aiSettingsSaveInput struct {
	Suggest string `json:"suggest"`
	JiraAI  string `json:"jira_ai"`
}

// aiModelFromWire reverses aiModelOptionValue (misc.go): the mobile client's
// "inherit" is persisted as "" — internal/aimodel's own normalize() already
// treats them as aliases, this is only about keeping config.json's stored
// form consistent with what handleAIModelsConfig already writes for the web
// panel.
func aiModelFromWire(m string) string {
	if m == "inherit" {
		return ""
	}
	return m
}

// handleAISettingsSave implements ai.settings.save. Validates suggest/jira_ai
// independently against aimodel.Allowed (mirroring handleAIModelsConfig's own
// POST validation), returning field-keyed FieldErrors — never a single
// generic error — so the client can highlight the exact select that failed.
// Never touches a secret: config.AIModels carries only model-tier strings.
func handleAISettingsSave(deps MiscDeps) sdui.ActionHandler {
	return func(_ context.Context, v sdui.Viewer, _ map[string]string, input json.RawMessage) (sdui.ActionResult, error) {
		var in aiSettingsSaveInput
		if len(input) > 0 {
			if err := json.Unmarshal(input, &in); err != nil {
				return sdui.ActionResult{}, sdui.FieldErrors{}.Add("suggest", "invalid request body")
			}
		}

		suggest := aiModelFromWire(in.Suggest)
		jiraAI := aiModelFromWire(in.JiraAI)

		fe := sdui.FieldErrors{}
		if !aimodel.Allowed(suggest) {
			fe = fe.Add("suggest", "invalid model")
		}
		if !aimodel.Allowed(jiraAI) {
			fe = fe.Add("jira_ai", "invalid model")
		}
		if len(fe) > 0 {
			return sdui.ActionResult{}, fe
		}

		if err := deps.SaveAIModels(config.AIModels{Suggest: suggest, JiraAI: jiraAI}); err != nil {
			return sdui.ActionResult{}, err
		}
		if deps.AuditEvent != nil {
			deps.AuditEvent(v.Username, "ai_models.update", "")
		}
		return sdui.ActionResult{Invalidate: []string{"ai-settings-form"}}, nil
	}
}

// --- jira.issues ----------------------------------------------------------

type jiraConnectInput struct {
	Site    string `json:"site"`
	Email   string `json:"email"`
	Token   string `json:"token"`
	Project string `json:"project"`
}

// handleJiraConnect implements jira.connect. Field-keyed validation on the
// three required inputs; the token itself is passed straight into
// deps.JiraConnect (which stores it in the per-user vault) and is
// NEVER echoed back in the ActionResult — Invalidate re-fetches jira.issues'
// screen, which will report "connected" without the token value.
func handleJiraConnect(deps MiscDeps) sdui.ActionHandler {
	return func(_ context.Context, v sdui.Viewer, _ map[string]string, input json.RawMessage) (sdui.ActionResult, error) {
		var in jiraConnectInput
		if len(input) > 0 {
			if err := json.Unmarshal(input, &in); err != nil {
				return sdui.ActionResult{}, sdui.FieldErrors{}.Add("site", "invalid request body")
			}
		}

		fe := sdui.FieldErrors{}
		if in.Site == "" {
			fe = fe.Add("site", "required")
		}
		if in.Email == "" {
			fe = fe.Add("email", "required")
		}
		if in.Token == "" {
			fe = fe.Add("token", "required")
		}
		if len(fe) > 0 {
			return sdui.ActionResult{}, fe
		}

		if err := deps.JiraConnect(v.Username, in.Site, in.Email, in.Token, in.Project); err != nil {
			return sdui.ActionResult{}, err
		}
		if deps.AuditEvent != nil {
			deps.AuditEvent(v.Username, "jira.connect", in.Site)
		}
		return sdui.ActionResult{Invalidate: []string{"issues-table"}}, nil
	}
}

type jiraIssueSelectInput struct {
	Key string `json:"key"`
}

// handleJiraIssueSelect implements jira.issue.select — writes the tapped
// issue's key into the per-viewer state misc.go declares
// (jiraSelectedIssueByUser), then invalidates the three components whose
// fixed endpoints read that state at fetch time. See misc.go's doc comment
// on jiraSelectedIssueMu for the full rationale (mirrors system.go's
// systemMetricsWindowByUser).
func handleJiraIssueSelect() sdui.ActionHandler {
	return func(_ context.Context, v sdui.Viewer, params map[string]string, input json.RawMessage) (sdui.ActionResult, error) {
		key := params["id"]
		if key == "" {
			var in jiraIssueSelectInput
			if len(input) > 0 {
				_ = json.Unmarshal(input, &in)
			}
			key = in.Key
		}
		if key == "" {
			return sdui.ActionResult{}, sdui.FieldErrors{}.Add("key", "required")
		}

		setJiraSelectedIssue(v.Username, key)
		return sdui.ActionResult{Invalidate: []string{"issue-detail", "issue-transition-form", "issue-comment-form"}}, nil
	}
}

type jiraIssueTransitionInput struct {
	TransitionID string `json:"transition_id"`
}

// handleJiraIssueTransition implements jira.issue.transition. Re-fetches the
// CURRENT set of valid transitions for the selected issue and checks the
// submitted transition_id against it before ever calling deps.JiraTransition
// — an invalid/stale transition id returns FieldErrors, never a raw Jira API
// error passthrough (Task 2's transition-validity test). This also closes a
// TOCTOU window where the transition picker's own options had gone stale
// between fetch and submit.
func handleJiraIssueTransition(deps MiscDeps) sdui.ActionHandler {
	return func(ctx context.Context, v sdui.Viewer, _ map[string]string, input json.RawMessage) (sdui.ActionResult, error) {
		var in jiraIssueTransitionInput
		if len(input) > 0 {
			if err := json.Unmarshal(input, &in); err != nil {
				return sdui.ActionResult{}, sdui.FieldErrors{}.Add("transition_id", "invalid request body")
			}
		}
		if in.TransitionID == "" {
			return sdui.ActionResult{}, sdui.FieldErrors{}.Add("transition_id", "required")
		}

		key := jiraSelectedIssueFor(v.Username)
		if key == "" {
			return sdui.ActionResult{}, sdui.FieldErrors{}.Add("transition_id", "no issue selected")
		}

		transitions, err := deps.JiraTransitions(ctx, v.Username, key)
		if err != nil {
			return sdui.ActionResult{}, err
		}
		valid := false
		for _, t := range transitions {
			if t.ID == in.TransitionID {
				valid = true
				break
			}
		}
		if !valid {
			return sdui.ActionResult{}, sdui.FieldErrors{}.Add("transition_id", "invalid transition for the current status")
		}

		if err := deps.JiraTransition(ctx, v.Username, key, in.TransitionID); err != nil {
			return sdui.ActionResult{}, err
		}
		if deps.AuditEvent != nil {
			deps.AuditEvent(v.Username, "jira.issue.transition", key)
		}
		return sdui.ActionResult{Invalidate: []string{"issues-table", "issue-detail", "issue-transition-form"}}, nil
	}
}

type jiraIssueCommentInput struct {
	Comment string `json:"comment"`
}

// handleJiraIssueComment implements jira.issue.comment.
func handleJiraIssueComment(deps MiscDeps) sdui.ActionHandler {
	return func(ctx context.Context, v sdui.Viewer, _ map[string]string, input json.RawMessage) (sdui.ActionResult, error) {
		var in jiraIssueCommentInput
		if len(input) > 0 {
			if err := json.Unmarshal(input, &in); err != nil {
				return sdui.ActionResult{}, sdui.FieldErrors{}.Add("comment", "invalid request body")
			}
		}
		if in.Comment == "" {
			return sdui.ActionResult{}, sdui.FieldErrors{}.Add("comment", "required")
		}

		key := jiraSelectedIssueFor(v.Username)
		if key == "" {
			return sdui.ActionResult{}, sdui.FieldErrors{}.Add("comment", "no issue selected")
		}

		if _, err := deps.JiraAddComment(ctx, v.Username, key, in.Comment); err != nil {
			return sdui.ActionResult{}, err
		}
		if deps.AuditEvent != nil {
			deps.AuditEvent(v.Username, "jira.issue.comment", key)
		}
		return sdui.ActionResult{Invalidate: []string{"issue-detail"}}, nil
	}
}

// --- deploy.apps ------------------------------------------------------------

type deployAppCreateInput struct {
	Name        string `json:"name"`
	Domain      string `json:"domain"`
	Branch      string `json:"branch"`
	ComposeFile string `json:"compose_file"`
}

// handleDeployAppCreate implements deploy.app.create. Field-keyed validation
// on name (the one truly required field — mirrors handlers_deploy.go's own
// handleDeployApps POST, which defaults Branch/ComposeFile server-side too).
// Manages internal/deploy's PaaS app catalog — an entry here is an arbitrary
// OTHER app this VPS hosts, never this process's own self-deploy trigger
// (see this file's package doc comment in misc.go).
func handleDeployAppCreate(deps MiscDeps) sdui.ActionHandler {
	return func(_ context.Context, v sdui.Viewer, _ map[string]string, input json.RawMessage) (sdui.ActionResult, error) {
		var in deployAppCreateInput
		if len(input) > 0 {
			if err := json.Unmarshal(input, &in); err != nil {
				return sdui.ActionResult{}, sdui.FieldErrors{}.Add("name", "invalid request body")
			}
		}
		if in.Name == "" {
			return sdui.ActionResult{}, sdui.FieldErrors{}.Add("name", "required")
		}

		branch := in.Branch
		if branch == "" {
			branch = "main"
		}

		if _, err := deps.CreateDeployApp(deploy.App{
			Name:        in.Name,
			Domain:      in.Domain,
			Branch:      branch,
			ComposeFile: in.ComposeFile,
		}); err != nil {
			return sdui.ActionResult{}, err
		}
		if deps.AuditEvent != nil {
			deps.AuditEvent(v.Username, "deploy.app.create", in.Name)
		}
		return sdui.ActionResult{Invalidate: []string{"deploy-apps-table"}}, nil
	}
}

// handleDeployAppRedeploy implements deploy.app.redeploy. params["id"] is the
// row's app name. Enqueues a new deploy via deps.TriggerRedeploy (which
// mirrors enqueueDeploy — internal/queue's "app_deploy" job kind, never
// this process's own self-deploy job).
func handleDeployAppRedeploy(deps MiscDeps) sdui.ActionHandler {
	return func(_ context.Context, v sdui.Viewer, params map[string]string, _ json.RawMessage) (sdui.ActionResult, error) {
		name := params["id"]
		if name == "" {
			return sdui.ActionResult{}, sdui.ErrActionNotFound
		}
		if _, err := deps.TriggerRedeploy(v.Username, name); err != nil {
			return sdui.ActionResult{}, err
		}
		if deps.AuditEvent != nil {
			deps.AuditEvent(v.Username, "deploy.app.redeploy", name)
		}
		return sdui.ActionResult{Invalidate: []string{"deploy-apps-table"}}, nil
	}
}

// handleDeployAppDelete implements deploy.app.delete. params["id"] is the
// row's app name. Destructive:true already keeps RunAction from
// ever calling this handler without confirm.Confirmed — see
// registerMiscActions' doc comment on this action's ActionDescriptor.
func handleDeployAppDelete(deps MiscDeps) sdui.ActionHandler {
	return func(ctx context.Context, v sdui.Viewer, params map[string]string, _ json.RawMessage) (sdui.ActionResult, error) {
		name := params["id"]
		if name == "" {
			return sdui.ActionResult{}, sdui.ErrActionNotFound
		}
		if err := deps.DestroyDeployApp(ctx, name); err != nil {
			return sdui.ActionResult{}, err
		}
		if deps.AuditEvent != nil {
			deps.AuditEvent(v.Username, "deploy.app.destroy", name)
		}
		return sdui.ActionResult{Invalidate: []string{"deploy-apps-table"}}, nil
	}
}

// --- queue.jobs --------------------------------------------------------------

// handleQueueJobRerun implements queue.job.retry. params["id"] is the job id.
// Resource-level ownership (job.Owner == v.Username, or admin) is checked
// HERE, not via RegisterAction's binary authorize gate, which cannot express
// per-resource ownership (see registerMiscActions' doc comment on this
// action). deps.AuthorizedForRerun re-applies the same per-kind
// authorization handlers_queue.go's "rerun" branch already enforces before
// calling deps.RerunQueueJob (the real internal/queue.Queue.Rerun).
func handleQueueJobRerun(deps MiscDeps) sdui.ActionHandler {
	return func(_ context.Context, v sdui.Viewer, params map[string]string, _ json.RawMessage) (sdui.ActionResult, error) {
		id := params["id"]
		if id == "" {
			return sdui.ActionResult{}, sdui.ErrActionNotFound
		}
		job, err := deps.GetQueueJob(id)
		if err != nil {
			return sdui.ActionResult{}, err
		}
		if !v.IsAdmin() && job.Owner != v.Username {
			return sdui.ActionResult{}, sdui.ErrActionNotFound
		}
		if !deps.AuthorizedForRerun(v.Username, v.IsAdmin(), job.Kind) {
			if deps.AuditEvent != nil {
				deps.AuditEvent(v.Username, "queue.rerun.denied", id)
			}
			return sdui.ActionResult{}, sdui.ErrActionNotFound
		}

		if _, err := deps.RerunQueueJob(id); err != nil {
			return sdui.ActionResult{}, err
		}
		if deps.AuditEvent != nil {
			deps.AuditEvent(v.Username, "queue.rerun", id)
		}
		return sdui.ActionResult{Invalidate: []string{"queue-jobs-table"}}, nil
	}
}

// handleQueueJobCancel implements queue.job.cancel. Destructive:true
// already keeps RunAction from ever calling this handler without
// confirm.Confirmed. The ownership check below mirrors handleQueueByID's own
// posture for its "cancel" branch: possession alone, no additional
// AuthorizedFor-by-kind check (unlike rerun) — matching
// handlers_queue.go exactly.
func handleQueueJobCancel(deps MiscDeps) sdui.ActionHandler {
	return func(_ context.Context, v sdui.Viewer, params map[string]string, _ json.RawMessage) (sdui.ActionResult, error) {
		id := params["id"]
		if id == "" {
			return sdui.ActionResult{}, sdui.ErrActionNotFound
		}
		job, err := deps.GetQueueJob(id)
		if err != nil {
			return sdui.ActionResult{}, err
		}
		if !v.IsAdmin() && job.Owner != v.Username {
			return sdui.ActionResult{}, sdui.ErrActionNotFound
		}

		if err := deps.CancelQueueJob(id); err != nil {
			return sdui.ActionResult{}, err
		}
		if deps.AuditEvent != nil {
			deps.AuditEvent(v.Username, "queue.cancel", id)
		}
		return sdui.ActionResult{Invalidate: []string{"queue-jobs-table"}}, nil
	}
}
