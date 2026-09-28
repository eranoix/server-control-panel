package screens

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/config"
	"server-control-panel/internal/deploy"
	"server-control-panel/internal/httpx"
	"server-control-panel/internal/jira"
	"server-control-panel/internal/mobilebff"
	"server-control-panel/internal/mobilebff/sdui"
	"server-control-panel/internal/queue"
)

const (
	miscAISettingsScreenID = "ai.settings"
	miscJiraIssuesScreenID = "jira.issues"
	miscDeployAppsScreenID = "deploy.apps"
	miscQueueJobsScreenID  = "queue.jobs"
)

const (
	miscJiraIssuesRowsEndpoint       = mobilebff.Prefix + "/jira/issues"
	miscJiraIssueDetailEndpoint      = mobilebff.Prefix + "/jira/issue-detail"
	miscJiraIssueTransitionsEndpoint = mobilebff.Prefix + "/jira/issue-transitions"
	miscDeployAppsRowsEndpoint       = mobilebff.Prefix + "/deploy/apps"
	miscQueueJobsRowsEndpoint        = mobilebff.Prefix + "/queue/jobs"
)

var (
	jiraSelectedIssueMu     sync.RWMutex
	jiraSelectedIssueByUser = map[string]string{}
)

func jiraSelectedIssueFor(username string) string {
	jiraSelectedIssueMu.RLock()
	defer jiraSelectedIssueMu.RUnlock()
	return jiraSelectedIssueByUser[username]
}

func setJiraSelectedIssue(username, key string) {
	jiraSelectedIssueMu.Lock()
	defer jiraSelectedIssueMu.Unlock()
	jiraSelectedIssueByUser[username] = key
}

func RegisterMisc(deps MiscDeps) {
	sdui.Register(miscAISettingsScreenID, func(_ context.Context, v sdui.Viewer) (*sdui.Envelope, error) {
		return buildAISettingsScreenForViewer(v, deps)
	})
	sdui.Register(miscJiraIssuesScreenID, func(_ context.Context, v sdui.Viewer) (*sdui.Envelope, error) {
		return buildJiraIssuesScreen(v, deps), nil
	})
	sdui.Register(miscDeployAppsScreenID, func(_ context.Context, v sdui.Viewer) (*sdui.Envelope, error) {
		return buildDeployAppsScreenForViewer(v)
	})
	sdui.Register(miscQueueJobsScreenID, func(_ context.Context, v sdui.Viewer) (*sdui.Envelope, error) {
		return buildQueueJobsScreen(v), nil
	})

	sdui.RegisterCatalog(miscQueueJobsScreenID, sdui.GroupAutomation, "Job queue", alwaysVisible)
	sdui.RegisterCatalog(miscDeployAppsScreenID, sdui.GroupAutomation, "App deploys", adminOnly)
	sdui.RegisterCatalog(miscAISettingsScreenID, sdui.GroupIntegrations, "AI models", adminOnly)
	sdui.RegisterCatalog(miscJiraIssuesScreenID, sdui.GroupIntegrations, "Jira", alwaysVisible)

	registerMiscActions(deps)

	mobilebff.Register("jira.issues.rows", func(api huma.API, mbDeps mobilebff.Deps) {
		registerMiscRows(api, "getJiraIssuesRows", "/jira/issues", "Rows of jira.issues", mbDeps.Cfg,
			func(ctx context.Context, v sdui.Viewer) ([]map[string]any, error) {
				connected, project := deps.JiraStatus(v.Username)
				if !connected {
					return []map[string]any{}, nil
				}
				jql := "assignee = currentUser() ORDER BY updated DESC"
				if project != "" {
					jql = fmt.Sprintf("project = %q ORDER BY updated DESC", project)
				}
				issues, err := deps.JiraListIssues(ctx, v.Username, jql)
				if err != nil {
					return nil, err
				}
				rows := make([]map[string]any, 0, len(issues))
				for _, is := range issues {
					rows = append(rows, jiraIssueRow(is))
				}
				return rows, nil
			})
	})
	mobilebff.Register("jira.issue-detail", func(api huma.API, mbDeps mobilebff.Deps) {
		registerMiscDetail(api, "getJiraIssueDetail", "/jira/issue-detail", "Detail of the selected jira.issues issue", mbDeps.Cfg,
			func(ctx context.Context, v sdui.Viewer) (map[string]any, error) {
				key := jiraSelectedIssueFor(v.Username)
				if key == "" {
					return map[string]any{}, nil
				}
				detail, err := deps.JiraGetIssue(ctx, v.Username, key)
				if err != nil {
					return nil, err
				}
				return jiraIssueDetailRow(detail), nil
			})
	})
	mobilebff.Register("jira.issue-transitions", func(api huma.API, mbDeps mobilebff.Deps) {
		registerMiscRows(api, "getJiraIssueTransitions", "/jira/issue-transitions", "Transitions available for the selected issue", mbDeps.Cfg,
			func(ctx context.Context, v sdui.Viewer) ([]map[string]any, error) {
				key := jiraSelectedIssueFor(v.Username)
				if key == "" {
					return []map[string]any{}, nil
				}
				transitions, err := deps.JiraTransitions(ctx, v.Username, key)
				if err != nil {
					return nil, err
				}
				rows := make([]map[string]any, 0, len(transitions))
				for _, t := range transitions {
					rows = append(rows, map[string]any{"value": t.ID, "label": t.Name})
				}
				return rows, nil
			})
	})

	mobilebff.Register("deploy.apps.rows", func(api huma.API, mbDeps mobilebff.Deps) {
		registerMiscRows(api, "getDeployAppsRows", "/deploy/apps", "Rows of deploy.apps", mbDeps.Cfg,
			func(_ context.Context, _ sdui.Viewer) ([]map[string]any, error) {
				apps, err := deps.ListDeployApps()
				if err != nil {
					return nil, err
				}
				rows := make([]map[string]any, 0, len(apps))
				for _, a := range apps {
					rows = append(rows, deployAppRow(a))
				}
				return rows, nil
			})
	})

	mobilebff.Register("queue.jobs.rows", func(api huma.API, mbDeps mobilebff.Deps) {
		registerMiscRows(api, "getQueueJobsRows", "/queue/jobs", "Rows of queue.jobs", mbDeps.Cfg,
			func(_ context.Context, v sdui.Viewer) ([]map[string]any, error) {
				owner := ""
				if !v.IsAdmin() {
					owner = v.Username
				}
				jobs := deps.ListQueueJobs(owner)
				rows := make([]map[string]any, 0, len(jobs))
				for _, j := range jobs {
					rows = append(rows, queueJobRow(j))
				}
				return rows, nil
			})
	})

	sdui.RegisterForbiddenForNonAdmin(miscAISettingsScreenID, func() []string {
		return []string{miscActionAISettingsSave}
	})
	sdui.RegisterForbiddenForNonAdmin(miscDeployAppsScreenID, func() []string {
		return []string{miscActionDeployAppCreate, miscActionDeployAppRedeploy, miscActionDeployAppDelete}
	})
	sdui.RegisterForbiddenForNonAdmin(miscQueueJobsScreenID, func() []string {
		return nil
	})
}

func buildAISettingsScreenForViewer(v sdui.Viewer, deps MiscDeps) (*sdui.Envelope, error) {
	if !v.IsAdmin() {
		return nil, sdui.ErrScreenNotFound
	}
	return buildAISettingsScreen(deps), nil
}

func aiModelOptionValue(m string) string {
	if m == "" {
		return "inherit"
	}
	return m
}

func buildAISettingsScreen(deps MiscDeps) *sdui.Envelope {
	cur, _ := deps.AIModelsConfig()
	options := []string{"inherit", "haiku", "sonnet", "opus", "fable"}

	form := sdui.FormComponent{
		ComponentBase: sdui.ComponentBase{Type: sdui.ComponentTypeForm, ID: "ai-settings-form"},
		Fields: []sdui.FormField{
			{Key: "suggest", Label: "AI suggestions (ai_suggest)", Kind: "select", Options: options, Value: aiModelOptionValue(cur.Suggest)},
			{Key: "jira_ai", Label: "Ticket review/refinement (jira_ai)", Kind: "select", Options: options, Value: aiModelOptionValue(cur.JiraAI)},
		},
		SubmitAction: sdui.ActionRef{ActionID: miscActionAISettingsSave, Label: "Save", Style: "primary"},
	}

	screen := sdui.Screen{
		ID:         miscAISettingsScreenID,
		Title:      "AI models",
		Components: []sdui.Component{form},
	}
	return &sdui.Envelope{Screen: screen}
}

func buildJiraIssuesScreen(v sdui.Viewer, deps MiscDeps) *sdui.Envelope {
	connected, _ := deps.JiraStatus(v.Username)
	if !connected {
		return buildJiraConnectScreen()
	}
	return buildJiraIssuesConnectedScreen()
}

func buildJiraConnectScreen() *sdui.Envelope {
	form := sdui.FormComponent{
		ComponentBase: sdui.ComponentBase{Type: sdui.ComponentTypeForm, ID: "jira-connect-form"},
		Fields: []sdui.FormField{
			{Key: "site", Label: "Site (e.g. yourcompany)", Kind: "text", Required: true},
			{Key: "email", Label: "E-mail", Kind: "text", Required: true},
			{Key: "token", Label: "API token", Kind: "password", Required: true},
			{Key: "project", Label: "Project (key, optional)", Kind: "text"},
		},
		SubmitAction: sdui.ActionRef{ActionID: miscActionJiraConnect, Label: "Connect", Style: "primary"},
	}
	screen := sdui.Screen{
		ID:         miscJiraIssuesScreenID,
		Title:      "Jira",
		Components: []sdui.Component{form},
	}
	return &sdui.Envelope{Screen: screen}
}

func buildJiraIssuesConnectedScreen() *sdui.Envelope {
	table := sdui.TableComponent{
		ComponentBase: sdui.ComponentBase{Type: sdui.ComponentTypeTable, ID: "issues-table"},
		Columns: []sdui.TableColumn{
			{Key: "key", Label: "Key", Kind: "text"},
			{Key: "summary", Label: "Summary", Kind: "text"},
			{Key: "status", Label: "Status", Kind: "badge"},
			{Key: "assignee", Label: "Assignee", Kind: "text"},
		},
		RowsSource: sdui.DataSource{Endpoint: miscJiraIssuesRowsEndpoint},
		RowActions: []sdui.ActionRef{
			{ActionID: miscActionJiraIssueSelect, Label: "View"},
		},
		EmptyState: &sdui.EmptyState{Text: "The issues your Jira connection brings in: those of the configured project or, with no fixed project, the ones assigned to you. Empty means the search worked and returned nothing — not that the connection dropped. Issues are created in Jira; here you move and comment on the ones that show up."},
	}

	detail := sdui.DetailComponent{
		ComponentBase: sdui.ComponentBase{Type: sdui.ComponentTypeDetail, ID: "issue-detail"},
		DataSource:    sdui.DataSource{Endpoint: miscJiraIssueDetailEndpoint},
		Fields: []sdui.DetailField{
			{Key: "key", Label: "Key", Kind: "text"},
			{Key: "summary", Label: "Summary", Kind: "text"},
			{Key: "status", Label: "Status", Kind: "text"},
			{Key: "assignee", Label: "Assignee", Kind: "text"},
			{Key: "reporter", Label: "Reporter", Kind: "text"},
			{Key: "description", Label: "Description", Kind: "text"},
			{Key: "created", Label: "Created", Kind: "text"},
			{Key: "updated", Label: "Updated", Kind: "text"},
		},
	}

	transitionForm := sdui.FormComponent{
		ComponentBase: sdui.ComponentBase{Type: sdui.ComponentTypeForm, ID: "issue-transition-form"},
		Fields: []sdui.FormField{
			{Key: "transition_id", Label: "Move to", Kind: "select", Required: true,
				OptionsSource: &sdui.DataSource{Endpoint: miscJiraIssueTransitionsEndpoint}},
		},
		SubmitAction: sdui.ActionRef{ActionID: miscActionJiraIssueTransition, Label: "Move", Style: "primary"},
	}

	commentForm := sdui.FormComponent{
		ComponentBase: sdui.ComponentBase{Type: sdui.ComponentTypeForm, ID: "issue-comment-form"},
		Fields: []sdui.FormField{
			{Key: "comment", Label: "Comment", Kind: "text", Required: true},
		},
		SubmitAction: sdui.ActionRef{ActionID: miscActionJiraIssueComment, Label: "Comment", Style: "primary"},
	}

	screen := sdui.Screen{
		ID:         miscJiraIssuesScreenID,
		Title:      "Jira",
		Components: []sdui.Component{table, detail, transitionForm, commentForm},
	}
	return &sdui.Envelope{Screen: screen}
}

func buildDeployAppsScreenForViewer(v sdui.Viewer) (*sdui.Envelope, error) {
	if !v.IsAdmin() {
		return nil, sdui.ErrScreenNotFound
	}
	return buildDeployAppsScreen(), nil
}

func buildDeployAppsScreen() *sdui.Envelope {
	table := sdui.TableComponent{
		ComponentBase: sdui.ComponentBase{Type: sdui.ComponentTypeTable, ID: "deploy-apps-table"},
		Columns: []sdui.TableColumn{
			{Key: "name", Label: "App", Kind: "text"},
			{Key: "domain", Label: "Domain", Kind: "text"},
			{Key: "branch", Label: "Branch", Kind: "text"},
			{Key: "last_status", Label: "Last deploy", Kind: "badge"},
			{Key: "updated", Label: "Updated", Kind: "datetime"},
		},
		RowsSource: sdui.DataSource{Endpoint: miscDeployAppsRowsEndpoint},
		RowActions: []sdui.ActionRef{
			{ActionID: miscActionDeployAppRedeploy, Label: "Redeploy"},
			{ActionID: miscActionDeployAppDelete, Label: "Delete", Style: "destructive"},
		},
		EmptyState: &sdui.EmptyState{Text: "Each row is an app this server hosts: domain, production branch and the status of the last deploy. Empty means no app has been created yet — the server does not discover apps on its own. Create the first one in the form below; the name becomes the DNS slug."},
	}

	createForm := sdui.FormComponent{
		ComponentBase: sdui.ComponentBase{Type: sdui.ComponentTypeForm, ID: "deploy-app-create-form"},
		Fields: []sdui.FormField{
			{Key: "name", Label: "Name (DNS slug)", Kind: "text", Required: true, Placeholder: "my-app"},
			{Key: "domain", Label: "Domain (optional)", Kind: "text"},
			{Key: "branch", Label: "Production branch", Kind: "text", Placeholder: "main"},
			{Key: "compose_file", Label: "Compose file (optional)", Kind: "text", Placeholder: "docker-compose.yml"},
		},
		SubmitAction: sdui.ActionRef{ActionID: miscActionDeployAppCreate, Label: "Create app", Style: "primary"},
	}

	confirmDelete := sdui.ConfirmDestructiveComponent{
		ComponentBase: sdui.ComponentBase{Type: sdui.ComponentTypeConfirmDestructive, ID: "deploy-app-delete-confirm"},
		ActionID:      miscActionDeployAppDelete,
		Message:       "This takes down the app's containers, removes nginx/env and deletes the bare repository. It cannot be undone.",
	}

	screen := sdui.Screen{
		ID:         miscDeployAppsScreenID,
		Title:      "App deploys",
		Components: []sdui.Component{table, createForm, confirmDelete},
	}
	return &sdui.Envelope{Screen: screen}
}

func buildQueueJobsScreen(_ sdui.Viewer) *sdui.Envelope {
	table := sdui.TableComponent{
		ComponentBase: sdui.ComponentBase{Type: sdui.ComponentTypeTable, ID: "queue-jobs-table"},
		Columns: []sdui.TableColumn{
			{Key: "kind", Label: "Type", Kind: "text"},
			{Key: "status", Label: "Status", Kind: "badge"},
			{Key: "progress", Label: "Progress", Kind: "text"},
			{Key: "owner", Label: "Owner", Kind: "text"},
			{Key: "queued", Label: "Queued", Kind: "datetime"},
		},
		RowsSource: sdui.DataSource{Endpoint: miscQueueJobsRowsEndpoint},
		RowActions: []sdui.ActionRef{
			{ActionID: miscActionQueueJobRerun, Label: "Retry"},
			{ActionID: miscActionQueueJobCancel, Label: "Cancel", Style: "destructive"},
		},
		EmptyState: &sdui.EmptyState{Text: "The queue of the panel's heavy tasks: app deploys, backups, package updates. It also keeps the finished ones, so empty means no task has been started yet. Nothing is created here — jobs come in on their own when you trigger a long-running action on another screen."},
	}

	confirmCancel := sdui.ConfirmDestructiveComponent{
		ComponentBase: sdui.ComponentBase{Type: sdui.ComponentTypeConfirmDestructive, ID: "queue-job-cancel-confirm"},
		ActionID:      miscActionQueueJobCancel,
		Message:       "This signals cancellation to the running job. Work already done is not undone.",
	}

	screen := sdui.Screen{
		ID:         miscQueueJobsScreenID,
		Title:      "Job queue",
		Components: []sdui.Component{table, confirmCancel},
	}
	return &sdui.Envelope{Screen: screen}
}

func jiraIssueRow(is jira.Issue) map[string]any {
	assignee := ""
	if is.Assignee != nil {
		assignee = is.Assignee.DisplayName
	}
	return map[string]any{
		"id":       is.Key,
		"key":      is.Key,
		"summary":  is.Summary,
		"status":   is.Status.Name,
		"assignee": assignee,
	}
}

func jiraIssueDetailRow(d *jira.IssueDetail) map[string]any {
	assignee, reporter := "", ""
	if d.Assignee != nil {
		assignee = d.Assignee.DisplayName
	}
	if d.Reporter != nil {
		reporter = d.Reporter.DisplayName
	}
	return map[string]any{
		"key":         d.Key,
		"summary":     d.Summary,
		"status":      d.Status.Name,
		"assignee":    assignee,
		"reporter":    reporter,
		"description": d.Description,
		"created":     d.Created,
		"updated":     d.Updated,
	}
}

func deployAppRow(a deploy.App) map[string]any {
	lastStatus := ""
	if n := len(a.Deploys); n > 0 {
		lastStatus = a.Deploys[n-1].Status
	}
	return map[string]any{
		"id":          a.Name,
		"name":        a.Name,
		"domain":      a.Domain,
		"branch":      a.Branch,
		"last_status": lastStatus,
		"updated":     formatMiscTimestamp(a.Updated),
	}
}

func queueJobRow(j *queue.Job) map[string]any {
	return map[string]any{
		"id":       j.ID,
		"kind":     j.Kind,
		"status":   string(j.Status),
		"progress": fmt.Sprintf("%d%%", j.Progress),
		"owner":    j.Owner,
		"queued":   formatMiscTimestamp(j.Queued),
	}
}

const miscTimestampFormat = "2006-01-02 15:04 UTC"

func formatMiscTimestamp(epoch int64) string {
	if epoch == 0 {
		return ""
	}
	return time.Unix(epoch, 0).UTC().Format(miscTimestampFormat)
}

func registerMiscRows(api huma.API, opID, path, summary string, cfg *config.Config, fetch func(context.Context, sdui.Viewer) ([]map[string]any, error)) {
	huma.Register(api, huma.Operation{
		OperationID: opID,
		Method:      http.MethodGet,
		Path:        path,
		Summary:     summary,
		Tags:        []string{"mobile", "sdui", "misc"},
		Middlewares: huma.Middlewares{mobilebff.RequireAuth, serveMiscRows(cfg, fetch)},
	}, miscRowsDocHandler)
}

type miscRowsInput struct{}

type miscRowsOutput struct {
	Body json.RawMessage
}

func miscRowsDocHandler(_ context.Context, _ *miscRowsInput) (*miscRowsOutput, error) {
	return &miscRowsOutput{Body: json.RawMessage(`{"rows":[]}`)}, nil
}

func serveMiscRows(cfg *config.Config, fetch func(context.Context, sdui.Viewer) ([]map[string]any, error)) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, _ func(huma.Context)) {
		req, w := humago.Unwrap(ctx)

		username := auth.UserFrom(req)
		v := sdui.ViewerFrom(cfg, username)

		rows, err := fetch(req.Context(), v)
		if err != nil {
			log.Printf("mobilebff/screens: error fetching rows (misc): %v", err)
			httpx.WriteErr(w, http.StatusInternalServerError, "internal_error")
			return
		}
		if rows == nil {
			rows = []map[string]any{}
		}

		body, err := json.Marshal(map[string]any{"rows": rows})
		if err != nil {
			log.Printf("mobilebff/screens: error serializing rows (misc): %v", err)
			httpx.WriteErr(w, http.StatusInternalServerError, "internal_error")
			return
		}

		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}
}

func registerMiscDetail(api huma.API, opID, path, summary string, cfg *config.Config, fetch func(context.Context, sdui.Viewer) (map[string]any, error)) {
	huma.Register(api, huma.Operation{
		OperationID: opID,
		Method:      http.MethodGet,
		Path:        path,
		Summary:     summary,
		Tags:        []string{"mobile", "sdui", "misc"},
		Middlewares: huma.Middlewares{mobilebff.RequireAuth, serveMiscDetail(cfg, fetch)},
	}, miscDetailDocHandler)
}

type miscDetailInput struct{}

type miscDetailOutput struct {
	Body json.RawMessage
}

func miscDetailDocHandler(_ context.Context, _ *miscDetailInput) (*miscDetailOutput, error) {
	return &miscDetailOutput{Body: json.RawMessage(`{"detail":{}}`)}, nil
}

func serveMiscDetail(cfg *config.Config, fetch func(context.Context, sdui.Viewer) (map[string]any, error)) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, _ func(huma.Context)) {
		req, w := humago.Unwrap(ctx)

		username := auth.UserFrom(req)
		v := sdui.ViewerFrom(cfg, username)

		detail, err := fetch(req.Context(), v)
		if err != nil {
			log.Printf("mobilebff/screens: error fetching detail (misc): %v", err)
			httpx.WriteErr(w, http.StatusInternalServerError, "internal_error")
			return
		}
		if detail == nil {
			detail = map[string]any{}
		}

		body, err := json.Marshal(map[string]any{"detail": detail})
		if err != nil {
			log.Printf("mobilebff/screens: error serializing detail (misc): %v", err)
			httpx.WriteErr(w, http.StatusInternalServerError, "internal_error")
			return
		}

		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}
}
