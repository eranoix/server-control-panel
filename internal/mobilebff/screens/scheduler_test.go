package screens

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"server-control-panel/internal/config"
	"server-control-panel/internal/mobilebff/sdui"
	"server-control-panel/internal/scheduler"
)

// testSchedulerCfg mirrors golden_test.go's testGoldenViewers construction —
// a real *config.Config through the real ViewerFrom/httpx.IsAdmin path,
// never a Viewer{} literal, so these tests exercise the same admin
// resolution production does.
func testSchedulerCfg() *config.Config {
	return &config.Config{
		SchemaVersion: config.CurrentSchemaVersion,
		Primary:       "sched-admin",
		Users: []config.User{
			{Username: "sched-admin", PasswordHash: "h"},
			{Username: "sched-user", PasswordHash: "h"},
		},
	}
}

func testSchedulerViewers() (admin, nonAdmin sdui.Viewer) {
	cfg := testSchedulerCfg()
	return sdui.ViewerFrom(cfg, "sched-admin"), sdui.ViewerFrom(cfg, "sched-user")
}

// testSchedulerDeps returns a SchedulerDeps sufficient to build the screen:
// only AuthorizedKinds matters for buildSchedulerJobsScreen — the rows
// endpoint (which uses ListJobs/etc.) is covered separately in
// scheduler_rows_test.go, since TableComponent never embeds row data in the
// screen envelope (see component.go): the envelope itself carries no job.
func testSchedulerDeps() SchedulerDeps {
	adminOnlyKind := KindOption{Value: "system_reboot", Label: "Reiniciar sistema"}
	sharedKind := KindOption{Value: "docker_prune", Label: "Limpar Docker"}
	return SchedulerDeps{
		AuthorizedKinds: func(_ string, isAdmin bool) []KindOption {
			if isAdmin {
				return []KindOption{sharedKind, adminOnlyKind}
			}
			return []KindOption{sharedKind}
		},
	}
}

func findComponent(t *testing.T, env *sdui.Envelope, id string) sdui.Component {
	t.Helper()
	for _, c := range env.Screen.Components {
		if c.Base().ID == id {
			return c
		}
	}
	t.Fatalf("component %q not found in %v", id, componentIDs(env))
	return nil
}

func componentIDs(env *sdui.Envelope) []string {
	ids := make([]string, len(env.Screen.Components))
	for i, c := range env.Screen.Components {
		ids[i] = c.Base().ID
	}
	return ids
}

// Test 1 (structure): exactly one table (jobs-table), one form (job-form),
// one standalone action (refresh-jobs) and one confirm_destructive
// (job-delete-confirm, action_id scheduler.job.delete); the table's
// row_actions reference scheduler.job.run_now and scheduler.job.delete.
func TestSchedulerScreen_Structure(t *testing.T) {
	admin, _ := testSchedulerViewers()
	env, err := buildSchedulerJobsScreen(testSchedulerDeps(), admin)
	if err != nil {
		t.Fatalf("buildSchedulerJobsScreen: %v", err)
	}

	counts := map[sdui.ComponentType]int{}
	for _, c := range env.Screen.Components {
		counts[c.ComponentType()]++
	}
	want := map[sdui.ComponentType]int{
		sdui.ComponentTypeTable:              1,
		sdui.ComponentTypeForm:               1,
		sdui.ComponentTypeAction:             1,
		sdui.ComponentTypeConfirmDestructive: 1,
	}
	for ct, n := range want {
		if counts[ct] != n {
			t.Errorf("components of type %q = %d, want %d (full count: %v)", ct, counts[ct], n, counts)
		}
	}

	table, ok := findComponent(t, env, "jobs-table").(sdui.TableComponent)
	if !ok {
		t.Fatalf("jobs-table is not a TableComponent")
	}
	gotActions := map[string]bool{}
	for _, ra := range table.RowActions {
		gotActions[ra.ActionID] = true
	}
	for _, want := range []string{schedulerActionRunNow, schedulerActionDelete} {
		if !gotActions[want] {
			t.Errorf("jobs-table.row_actions does not reference %q: %v", want, table.RowActions)
		}
	}

	confirm, ok := findComponent(t, env, "job-delete-confirm").(sdui.ConfirmDestructiveComponent)
	if !ok {
		t.Fatalf("job-delete-confirm is not a ConfirmDestructiveComponent")
	}
	if confirm.ActionID != schedulerActionDelete {
		t.Errorf("job-delete-confirm.action_id = %q, want %q", confirm.ActionID, schedulerActionDelete)
	}

	action, ok := findComponent(t, env, "refresh-jobs").(sdui.ActionComponent)
	if !ok {
		t.Fatalf("refresh-jobs is not an ActionComponent")
	}
	if action.ActionID != schedulerActionRefresh {
		t.Errorf("refresh-jobs.action_id = %q, want %q", action.ActionID, schedulerActionRefresh)
	}
}

// Test 2 (RBAC by omission) + Test 3 (non-vacuity): the admin envelope
// contains run_as_root and the admin-only kind; the non-admin envelope
// contains neither.
func TestSchedulerScreen_RBACOmissionOnBytes(t *testing.T) {
	admin, nonAdmin := testSchedulerViewers()
	deps := testSchedulerDeps()

	adminEnv, err := buildSchedulerJobsScreen(deps, admin)
	if err != nil {
		t.Fatalf("build admin: %v", err)
	}
	nonAdminEnv, err := buildSchedulerJobsScreen(deps, nonAdmin)
	if err != nil {
		t.Fatalf("build non-admin: %v", err)
	}

	adminBytes, err := json.Marshal(adminEnv)
	if err != nil {
		t.Fatalf("marshal admin: %v", err)
	}
	nonAdminBytes, err := json.Marshal(nonAdminEnv)
	if err != nil {
		t.Fatalf("marshal non-admin: %v", err)
	}

	// Non-vacuity: the admin does receive both.
	if !strings.Contains(string(adminBytes), "run_as_root") {
		t.Errorf("admin envelope does not contain \"run_as_root\": %s", adminBytes)
	}
	if !strings.Contains(string(adminBytes), "system_reboot") {
		t.Errorf("admin envelope does not contain the admin-only kind \"system_reboot\": %s", adminBytes)
	}

	// Omission: the non-admin receives neither.
	if strings.Contains(string(nonAdminBytes), "run_as_root") {
		t.Errorf("non-admin envelope contains \"run_as_root\": %s", nonAdminBytes)
	}
	if strings.Contains(string(nonAdminBytes), "system_reboot") {
		t.Errorf("non-admin envelope contains the admin-only kind \"system_reboot\": %s", nonAdminBytes)
	}
}

// Test 4: the options of the form's "kind" field come exactly from the
// AuthorizedKinds closure — never a list the Builder builds on its own.
func TestSchedulerScreen_KindOptionsComeFromAuthorizedKinds(t *testing.T) {
	admin, nonAdmin := testSchedulerViewers()
	deps := testSchedulerDeps()

	for name, v := range map[string]sdui.Viewer{"admin": admin, "nonadmin": nonAdmin} {
		env, err := buildSchedulerJobsScreen(deps, v)
		if err != nil {
			t.Fatalf("build %s: %v", name, err)
		}
		form, ok := findComponent(t, env, "job-form").(sdui.FormComponent)
		if !ok {
			t.Fatalf("job-form is not a FormComponent (%s)", name)
		}
		var kindField *sdui.FormField
		for i := range form.Fields {
			if form.Fields[i].Key == "kind" {
				kindField = &form.Fields[i]
			}
		}
		if kindField == nil {
			t.Fatalf("job-form does not have field \"kind\" (%s)", name)
		}

		want := deps.AuthorizedKinds(v.Username, v.IsAdmin())
		wantValues := make([]string, len(want))
		for i, k := range want {
			wantValues[i] = k.Value
		}
		if len(kindField.Options) != len(wantValues) {
			t.Fatalf("%s: kind.options = %v, want %v", name, kindField.Options, wantValues)
		}
		for i, v := range wantValues {
			if kindField.Options[i] != v {
				t.Errorf("%s: kind.options[%d] = %q, want %q", name, i, kindField.Options[i], v)
			}
		}
	}
}

// Test 5: no client-side decision logic leaks into the payload — no
// condition/visible_when/expression/permission key outside
// permission_hint.
func TestSchedulerScreen_NoClientSideLogicKeys(t *testing.T) {
	admin, _ := testSchedulerViewers()
	env, err := buildSchedulerJobsScreen(testSchedulerDeps(), admin)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	body, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var generic any
	if err := json.Unmarshal(body, &generic); err != nil {
		t.Fatalf("generic unmarshal: %v", err)
	}
	forbidden := []string{"\"condition\"", "\"visible_when\"", "\"expression\""}
	for _, key := range forbidden {
		if strings.Contains(string(body), key) {
			t.Errorf("payload contains forbidden client-side logic key %s: %s", key, body)
		}
	}
	// "permission" may only appear as part of "permission_hint".
	idx := 0
	for {
		i := strings.Index(string(body)[idx:], "\"permission")
		if i < 0 {
			break
		}
		i += idx
		if !strings.HasPrefix(string(body)[i:], "\"permission_hint\"") {
			t.Errorf("payload contains a \"permission...\" key that is not permission_hint, around: %s", string(body)[i:min(i+40, len(body))])
		}
		idx = i + len("\"permission")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Test 6: the table's timestamp columns are declared as display-ready text
// (kind "text"), never as a type that would suggest the client format a raw
// epoch. The actually pre-formatted value is tested in
// scheduler_rows_test.go, against the rows endpoint — the screen carries no
// rows at all (see TableComponent.RowsSource in component.go).
func TestSchedulerScreen_TimestampColumnsAreDisplayReadyText(t *testing.T) {
	admin, _ := testSchedulerViewers()
	env, err := buildSchedulerJobsScreen(testSchedulerDeps(), admin)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	table, ok := findComponent(t, env, "jobs-table").(sdui.TableComponent)
	if !ok {
		t.Fatalf("jobs-table is not a TableComponent")
	}
	for _, col := range table.Columns {
		if col.Key == "next_fire" || col.Key == "last_fire" {
			if col.Kind != "text" {
				t.Errorf("column %q has kind %q, want \"text\" (value already comes pre-formatted from the server)", col.Key, col.Kind)
			}
		}
	}
}

// sanity: makes sure the timestamp formatting helper used by the rows
// endpoint (scheduler_rows_test.go) is reachable from this test file too,
// and that epoch 0 becomes an empty string.
func TestFormatSchedulerTimestamp_ZeroIsEmpty(t *testing.T) {
	if got := formatSchedulerTimestamp(0); got != "" {
		t.Errorf("formatSchedulerTimestamp(0) = %q, want \"\"", got)
	}
	epoch := time.Date(2026, 1, 2, 15, 4, 0, 0, time.UTC).Unix()
	want := "2026-01-02 15:04 UTC"
	if got := formatSchedulerTimestamp(epoch); got != want {
		t.Errorf("formatSchedulerTimestamp(%d) = %q, want %q", epoch, got, want)
	}
}

var _ = scheduler.Job{} // keeps the import even if a test above is removed while iterating
