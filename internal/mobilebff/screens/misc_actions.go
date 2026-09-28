package screens

import (
	"context"
	"encoding/json"

	"server-control-panel/internal/aimodel"
	"server-control-panel/internal/config"
	"server-control-panel/internal/deploy"
	"server-control-panel/internal/mobilebff/sdui"
)

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

func miscAdminViewer(v sdui.Viewer) bool { return v.IsAdmin() }

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
		},
		authenticatedViewer,
		handleQueueJobCancel(deps),
	)
}

type aiSettingsSaveInput struct {
	Suggest string `json:"suggest"`
	JiraAI  string `json:"jira_ai"`
}

func aiModelFromWire(m string) string {
	if m == "inherit" {
		return ""
	}
	return m
}

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

type jiraConnectInput struct {
	Site    string `json:"site"`
	Email   string `json:"email"`
	Token   string `json:"token"`
	Project string `json:"project"`
}

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

type deployAppCreateInput struct {
	Name        string `json:"name"`
	Domain      string `json:"domain"`
	Branch      string `json:"branch"`
	ComposeFile string `json:"compose_file"`
}

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
