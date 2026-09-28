package screens

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/config"
	"server-control-panel/internal/httpx"
	"server-control-panel/internal/mobilebff"
	"server-control-panel/internal/mobilebff/sdui"
	"server-control-panel/internal/scheduler"
)

const schedulerJobsScreenID = "scheduler.jobs"

const schedulerJobsRowsEndpoint = mobilebff.Prefix + "/scheduler/jobs"

const schedulerTimestampFormat = "2006-01-02 15:04 UTC"

func Register(deps SchedulerDeps) {
	sdui.Register(schedulerJobsScreenID, func(_ context.Context, v sdui.Viewer) (*sdui.Envelope, error) {
		return buildSchedulerJobsScreen(deps, v)
	})
	sdui.RegisterCatalog(schedulerJobsScreenID, sdui.GroupAutomation, "Scheduler", alwaysVisible)
	registerSchedulerActions(deps)
	mobilebff.Register("scheduler.jobs.rows", func(api huma.API, mbDeps mobilebff.Deps) {
		registerSchedulerRows(api, deps, mbDeps)
	})
	sdui.RegisterForbiddenForNonAdmin(schedulerJobsScreenID, func() []string {
		nonAdminKinds := map[string]bool{}
		for _, k := range deps.AuthorizedKinds("", false) {
			nonAdminKinds[k.Value] = true
		}
		forbidden := []string{"run_as_root"}
		for _, k := range deps.AuthorizedKinds("", true) {
			if !nonAdminKinds[k.Value] {
				forbidden = append(forbidden, k.Value)
			}
		}
		return forbidden
	})
}

func buildSchedulerJobsScreen(deps SchedulerDeps, v sdui.Viewer) (*sdui.Envelope, error) {
	table := sdui.TableComponent{
		ComponentBase: sdui.ComponentBase{Type: sdui.ComponentTypeTable, ID: "jobs-table"},
		Columns: []sdui.TableColumn{
			{Key: "name", Label: "Name", Kind: "text"},
			{Key: "schedule", Label: "Schedule", Kind: "text"},
			{Key: "kind", Label: "Type", Kind: "text"},
			{Key: "last_status", Label: "Last status", Kind: "badge", BadgeMap: map[string]string{
				"ok": "success", "failed": "danger", "skipped": "neutral",
			}},
			{Key: "next_fire", Label: "Next run", Kind: "text"},
		},
		RowsSource: sdui.DataSource{Endpoint: schedulerJobsRowsEndpoint},
		RowActions: []sdui.ActionRef{
			{ActionID: schedulerActionRunNow, Label: "Run now", Style: "secondary"},
			{ActionID: schedulerActionDelete, Label: "Delete", Style: "destructive"},
		},
		EmptyState: &sdui.EmptyState{Text: "Scheduler jobs run on their own at the time you set, in cron syntax. Empty means nothing is scheduled: no recurring task fires until the first one is created. Fill in name, schedule (e.g. */15 * * * *) and type in the form below."},
	}

	kindOptions := deps.AuthorizedKinds(v.Username, v.IsAdmin())
	kindValues := make([]string, len(kindOptions))
	for i, k := range kindOptions {
		kindValues[i] = k.Value
	}

	form := sdui.FormComponent{
		ComponentBase: sdui.ComponentBase{Type: sdui.ComponentTypeForm, ID: "job-form"},
		Fields: []sdui.FormField{
			{Key: "name", Label: "Name", Kind: "text", Required: true},
			{Key: "schedule", Label: "Schedule (cron)", Kind: "text", Required: true, Placeholder: "*/15 * * * *"},
			{Key: "kind", Label: "Type", Kind: "select", Required: true, Options: kindValues},
			{Key: "enabled", Label: "Enabled", Kind: "bool"},
			{Key: "run_as_root", Label: "Run as root", Kind: "bool"},
		},
		SubmitAction: sdui.ActionRef{ActionID: schedulerActionSave, Label: "Save", Style: "primary"},
	}
	if !v.IsAdmin() {
		sdui.DropFormFields(&form, func(f sdui.FormField) bool { return f.Key != "run_as_root" })
	}

	refreshAction := sdui.ActionComponent{
		ComponentBase: sdui.ComponentBase{Type: sdui.ComponentTypeAction, ID: "refresh-jobs"},
		Label:         "Refresh",
		ActionID:      schedulerActionRefresh,
		Style:         "secondary",
	}

	deleteConfirm := sdui.ConfirmDestructiveComponent{
		ComponentBase: sdui.ComponentBase{Type: sdui.ComponentTypeConfirmDestructive, ID: "job-delete-confirm"},
		ActionID:      schedulerActionDelete,
		Message:       "This job will be deleted permanently. This action cannot be undone.",
	}

	screen := sdui.Screen{
		ID:    schedulerJobsScreenID,
		Title: "Scheduler",
		Components: []sdui.Component{
			table,
			form,
			refreshAction,
			deleteConfirm,
		},
	}

	return &sdui.Envelope{Screen: screen}, nil
}

func registerSchedulerRows(api huma.API, deps SchedulerDeps, mbDeps mobilebff.Deps) {
	cfg := mbDeps.Cfg
	huma.Register(api, huma.Operation{
		OperationID: "getSchedulerJobRows",
		Method:      http.MethodGet,
		Path:        "/scheduler/jobs",
		Summary:     "Rows of the scheduler.jobs table, filtered by RBAC for the authenticated user",
		Tags:        []string{"mobile", "sdui", "scheduler"},
		Middlewares: huma.Middlewares{mobilebff.RequireAuth, serveSchedulerRows(cfg, deps)},
	}, schedulerRowsDocHandler)
}

type schedulerRowsInput struct{}

type schedulerRowsOutput struct {
	Body json.RawMessage
}

func schedulerRowsDocHandler(ctx context.Context, in *schedulerRowsInput) (*schedulerRowsOutput, error) {
	return &schedulerRowsOutput{Body: json.RawMessage(`{"rows":[]}`)}, nil
}

func serveSchedulerRows(cfg *config.Config, deps SchedulerDeps) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		req, w := humago.Unwrap(ctx)

		username := auth.UserFrom(req)
		v := sdui.ViewerFrom(cfg, username)

		owner := ""
		if !v.IsAdmin() {
			owner = v.Username
		}
		jobs := deps.ListJobs(owner)

		rows := make([]map[string]any, 0, len(jobs))
		for _, j := range jobs {
			rows = append(rows, schedulerJobRow(j, v.IsAdmin()))
		}

		body, err := json.Marshal(map[string]any{"rows": rows})
		if err != nil {
			log.Printf("mobilebff/screens: error serializing the rows of %q: %v", schedulerJobsScreenID, err)
			httpx.WriteErr(w, http.StatusInternalServerError, "internal_error")
			return
		}

		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}
}

func schedulerJobRow(j *scheduler.Job, isAdmin bool) map[string]any {
	row := map[string]any{
		"id":          j.ID,
		"name":        j.Name,
		"schedule":    j.Schedule,
		"kind":        j.Kind,
		"enabled":     j.Enabled,
		"owner":       j.Owner,
		"last_status": j.LastStatus,
		"last_fire":   formatSchedulerTimestamp(j.LastFire),
		"next_fire":   formatSchedulerTimestamp(j.NextFire),
	}
	if isAdmin {
		row["run_as_root"] = j.RunAsRoot
	}
	return row
}

func formatSchedulerTimestamp(epoch int64) string {
	if epoch == 0 {
		return ""
	}
	return time.Unix(epoch, 0).UTC().Format(schedulerTimestampFormat)
}
