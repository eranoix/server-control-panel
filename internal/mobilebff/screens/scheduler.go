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

// schedulerJobsScreenID is the id the app requests via
// GET /api/mobile/v1/screens/{id} and the id sdui's golden harness enumerates
// via RegisteredScreens().
const schedulerJobsScreenID = "scheduler.jobs"

// schedulerJobsRowsEndpoint is the absolute path the jobs-table row_actions
// endpoint resolves to. Absolute (carries mobilebff.Prefix), matching the
// existing fixture convention (contracts/sdui/fixtures/all-components.json)
// of DataSource.Endpoint being a ready-to-fetch URL, never a path fragment
// the client has to prefix itself.
const schedulerJobsRowsEndpoint = mobilebff.Prefix + "/scheduler/jobs"

// schedulerTimestampFormat is how every timestamp in this screen (and its
// rows endpoint) is rendered server-side. PITFALLS.md Pitfall 2's third
// warning sign is a client formatting a raw epoch — the payload here never
// carries one: next_fire/last_fire are always this pre-formatted string, so
// a locale or format change is a server change, not a client release.
const schedulerTimestampFormat = "2006-01-02 15:04 UTC"

// Register wires the scheduler.jobs screen, its four actions and its rows
// endpoint. Called explicitly (not from an init()) by internal/api/api.go,
// which is the only place that can construct a real SchedulerDeps — see
// deps.go for why this is a function, not a package-level side effect.
func Register(deps SchedulerDeps) {
	sdui.Register(schedulerJobsScreenID, func(_ context.Context, v sdui.Viewer) (*sdui.Envelope, error) {
		return buildSchedulerJobsScreen(deps, v)
	})
	// Catalog entry: sempreVisivel. The builder assembles the screen for any
	// viewer and filters the KINDS the person may schedule (AuthorizedKinds)
	// from inside the Envelope — a content filter, not a screen filter, so
	// hiding the item from the picker would only create an unexplained gap
	// for the non-admin.
	sdui.RegisterCatalog(schedulerJobsScreenID, sdui.GroupAutomacao, "Scheduler", sempreVisivel)
	registerSchedulerActions(deps)
	mobilebff.Register("scheduler.jobs.rows", func(api huma.API, mbDeps mobilebff.Deps) {
		registerSchedulerRows(api, deps, mbDeps)
	})
	// The forbidden set for the non-admin golden is derived from the SAME
	// AuthorizedKinds the Builder uses (never a separate hand-picked list):
	// every kind that shows up for an admin and not for a non-admin, plus
	// "run_as_root" — the one field (not a kind) that is always omitted from
	// the non-admin golden. The empty username is safe here because
	// AuthorizedKinds decides by isAdmin (equivalent to "is the primary
	// user"), not by identity — see deps.go's comment.
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

// buildSchedulerJobsScreen is the pure Builder body, split out from Register
// so tests can call it directly with a fake SchedulerDeps and a synthetic
// Viewer without registering anything globally (avoiding the
// duplicate-registration panic across test functions).
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
			// run_as_root only enters the form for an admin — removed below
			// by DropFormFields, never by an `if` that assembles two
			// different forms, so that the omission goes through the same
			// audited helper every other screen uses.
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
		// RequireTypedConfirmation deliberately empty: a scheduler job is a
		// routine, recreatable object, not a genuinely irreversible
		// operation — demanding a typed confirmation here would train the
		// user to type without reading, which is worse on the day the action
		// really is irreversible. Reserving typed text for those cases is a
		// deliberate choice, not an oversight; see
		// scheduler_actions.go.
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

// registerSchedulerRows registers GET /scheduler/jobs — the rows_source the
// jobs-table binds to. TableComponent never embeds row data in the screen
// envelope (see component.go); rows are always a separate, role-filtered
// fetch, exactly like the panel's own GET /api/scheduler/jobs.
//
// Wire shape (pinned here — this is the first Go handler to define it for
// any SDUI table, per plan 07-08): {"rows": [{"id", "name", "schedule",
// "kind", "enabled", "owner", "last_status", "last_fire", "next_fire",
// "run_as_root"?}, ...]} — a wrapped object under the generic key "rows",
// not "jobs", so every future table screen's client-side fetch code is
// identical regardless of the resource. run_as_root is present only when
// the caller is admin — RBAC by omission applies to row data exactly as it
// applies to the screen envelope.
func registerSchedulerRows(api huma.API, deps SchedulerDeps, mbDeps mobilebff.Deps) {
	cfg := mbDeps.Cfg
	huma.Register(api, huma.Operation{
		OperationID: "getSchedulerJobRows",
		Method:      http.MethodGet,
		Path:        "/scheduler/jobs",
		Summary:     "Linhas da tabela scheduler.jobs, filtradas por RBAC para o usuário autenticado",
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

// schedulerJobRow shapes one *scheduler.Job into the row wire format —
// preformatted timestamps, run_as_root omitted for non-admin.
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

// formatSchedulerTimestamp renders a Unix epoch (0 = never/unset) as the
// server-formatted display string every timestamp in this screen uses.
func formatSchedulerTimestamp(epoch int64) string {
	if epoch == 0 {
		return ""
	}
	return time.Unix(epoch, 0).UTC().Format(schedulerTimestampFormat)
}
