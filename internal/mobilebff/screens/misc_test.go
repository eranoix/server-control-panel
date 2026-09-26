package screens

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"server-control-panel/internal/config"
	"server-control-panel/internal/deploy"
	"server-control-panel/internal/jira"
	"server-control-panel/internal/mobilebff/sdui"
	"server-control-panel/internal/queue"
)

// testMiscCfg/testMiscViewers mirror testDockerCfg/testDockerViewers — a real
// *config.Config through the real ViewerFrom/httpx.IsAdmin path, never a
// Viewer{} literal.
func testMiscCfg() *config.Config {
	return &config.Config{
		SchemaVersion: config.CurrentSchemaVersion,
		Primary:       "misc-admin",
		Users: []config.User{
			{Username: "misc-admin", PasswordHash: "h"},
			{Username: "misc-user", PasswordHash: "h"},
		},
	}
}

func testMiscViewers() (admin, nonAdmin sdui.Viewer) {
	cfg := testMiscCfg()
	return sdui.ViewerFrom(cfg, "misc-admin"), sdui.ViewerFrom(cfg, "misc-user")
}

// testMiscDeps returns a MiscDeps sufficient to build every screen — no
// mutating closure here is exercised by misc_test.go (that's
// misc_actions_test.go's job); this only needs the read-side closures the
// four builders call directly.
func testMiscDeps() MiscDeps {
	return MiscDeps{
		AIModelsConfig: func() (config.AIModels, map[string]string) {
			return config.AIModels{Suggest: "haiku", JiraAI: ""}, map[string]string{"suggest": "haiku", "jira_ai": "sonnet"}
		},
		JiraStatus: func(user string) (bool, string) {
			return false, ""
		},
	}
}

// testMiscDepsJiraConnected is testMiscDeps with JiraStatus reporting the
// given user as connected — used by the setup-gating test to prove the
// connected shape.
func testMiscDepsJiraConnected(connectedUser string) MiscDeps {
	d := testMiscDeps()
	d.JiraStatus = func(user string) (bool, string) {
		return user == connectedUser, "PROJ"
	}
	return d
}

// --- Test 1: structure ----------------------------------------------------

// TestAISettingsScreen_Structure proves exactly one form (ai-settings-form)
// with the two model-tier select fields, submit_action pointed at
// ai.settings.save — and, separately, that a non-admin viewer gets
// ErrScreenNotFound (whole-screen admin-only, mirrors handleAIModelsConfig).
func TestAISettingsScreen_Structure(t *testing.T) {
	admin, nonAdmin := testMiscViewers()
	deps := testMiscDeps()

	env, err := buildAISettingsScreenForViewer(admin, deps)
	if err != nil {
		t.Fatalf("buildAISettingsScreenForViewer(admin): %v", err)
	}
	form, ok := findComponent(t, env, "ai-settings-form").(sdui.FormComponent)
	if !ok {
		t.Fatalf("ai-settings-form is not a FormComponent")
	}
	if form.SubmitAction.ActionID != miscActionAISettingsSave {
		t.Errorf("submit_action = %q, want %q", form.SubmitAction.ActionID, miscActionAISettingsSave)
	}
	wantKeys := map[string]bool{"suggest": false, "jira_ai": false}
	for _, f := range form.Fields {
		if _, ok := wantKeys[f.Key]; ok {
			wantKeys[f.Key] = true
		}
	}
	for k, found := range wantKeys {
		if !found {
			t.Errorf("ai-settings-form does not have the field %q: %v", k, form.Fields)
		}
	}

	if _, err := buildAISettingsScreenForViewer(nonAdmin, deps); err != sdui.ErrScreenNotFound {
		t.Errorf("non-admin: err = %v, want sdui.ErrScreenNotFound", err)
	}
}

// TestDeployAppsScreen_Structure proves exactly one table (deploy-apps-table,
// row_actions redeploy+delete), one form (deploy-app-create-form) and one
// confirm_destructive (deploy-app-delete-confirm, action_id deploy.app.delete)
// — and that a non-admin gets ErrScreenNotFound (whole-screen admin-only,
// mirrors handlers_deploy.go's blanket r.mustPrimary gate).
func TestDeployAppsScreen_Structure(t *testing.T) {
	admin, nonAdmin := testMiscViewers()

	env, err := buildDeployAppsScreenForViewer(admin)
	if err != nil {
		t.Fatalf("buildDeployAppsScreenForViewer(admin): %v", err)
	}
	table, ok := findComponent(t, env, "deploy-apps-table").(sdui.TableComponent)
	if !ok {
		t.Fatalf("deploy-apps-table is not a TableComponent")
	}
	gotActions := map[string]bool{}
	for _, ra := range table.RowActions {
		gotActions[ra.ActionID] = true
	}
	for _, want := range []string{miscActionDeployAppRedeploy, miscActionDeployAppDelete} {
		if !gotActions[want] {
			t.Errorf("deploy-apps-table.row_actions does not reference %q: %v", want, table.RowActions)
		}
	}
	if _, ok := findComponent(t, env, "deploy-app-create-form").(sdui.FormComponent); !ok {
		t.Fatalf("deploy-app-create-form is not a FormComponent")
	}
	confirm, ok := findComponent(t, env, "deploy-app-delete-confirm").(sdui.ConfirmDestructiveComponent)
	if !ok {
		t.Fatalf("deploy-app-delete-confirm is not a ConfirmDestructiveComponent")
	}
	if confirm.ActionID != miscActionDeployAppDelete {
		t.Errorf("deploy-app-delete-confirm.action_id = %q, want %q", confirm.ActionID, miscActionDeployAppDelete)
	}

	if _, err := buildDeployAppsScreenForViewer(nonAdmin); err != sdui.ErrScreenNotFound {
		t.Errorf("non-admin: err = %v, want sdui.ErrScreenNotFound", err)
	}
}

// TestQueueJobsScreen_Structure proves exactly one table (queue-jobs-table,
// row_actions retry+cancel) and one confirm_destructive (queue-job-cancel-confirm,
// action_id queue.job.cancel) — no admin gate at the screen level (any
// authenticated viewer builds successfully; per-owner scoping happens at the
// rows endpoint/action-handler level, not here).
func TestQueueJobsScreen_Structure(t *testing.T) {
	admin, nonAdmin := testMiscViewers()

	for name, v := range map[string]sdui.Viewer{"admin": admin, "nonadmin": nonAdmin} {
		env := buildQueueJobsScreen(v)
		table, ok := findComponent(t, env, "queue-jobs-table").(sdui.TableComponent)
		if !ok {
			t.Fatalf("%s: queue-jobs-table is not a TableComponent", name)
		}
		gotActions := map[string]bool{}
		for _, ra := range table.RowActions {
			gotActions[ra.ActionID] = true
		}
		for _, want := range []string{miscActionQueueJobRerun, miscActionQueueJobCancel} {
			if !gotActions[want] {
				t.Errorf("%s: queue-jobs-table.row_actions does not reference %q: %v", name, want, table.RowActions)
			}
		}
		confirm, ok := findComponent(t, env, "queue-job-cancel-confirm").(sdui.ConfirmDestructiveComponent)
		if !ok {
			t.Fatalf("%s: queue-job-cancel-confirm is not a ConfirmDestructiveComponent", name)
		}
		if confirm.ActionID != miscActionQueueJobCancel {
			t.Errorf("%s: queue-job-cancel-confirm.action_id = %q, want %q", name, confirm.ActionID, miscActionQueueJobCancel)
		}
	}
}

// --- Test 2: Jira setup gating ---------------------------------------------

// TestJiraIssuesScreen_SetupGating proves a not-connected viewer sees ONLY
// jira-connect-form (no table, no detail, no other form), while a connected
// viewer sees the full table+detail+transition-form+comment-form shape —
// and that this gate is per-user credential (JiraStatus), never IsAdmin: a
// non-admin connected viewer gets the full shape too.
func TestJiraIssuesScreen_SetupGating(t *testing.T) {
	_, nonAdmin := testMiscViewers()

	notConnected := testMiscDeps()
	env := buildJiraIssuesScreen(nonAdmin, notConnected)
	if len(env.Screen.Components) != 1 {
		t.Fatalf("not-connected: %d component(s), want 1: %v", len(env.Screen.Components), componentIDs(env))
	}
	if _, ok := findComponent(t, env, "jira-connect-form").(sdui.FormComponent); !ok {
		t.Fatalf("not-connected: jira-connect-form is not a FormComponent")
	}

	connected := testMiscDepsJiraConnected(nonAdmin.Username)
	env2 := buildJiraIssuesScreen(nonAdmin, connected)
	for _, id := range []string{"issues-table", "issue-detail", "issue-transition-form", "issue-comment-form"} {
		findComponent(t, env2, id) // t.Fatalf inside if missing
	}
	for _, forbidden := range []string{"jira-connect-form"} {
		for _, c := range env2.Screen.Components {
			if c.Base().ID == forbidden {
				t.Errorf("connected: component %q should no longer exist", forbidden)
			}
		}
	}
}

// --- Test 3: RBAC omission on bytes -----------------------------------------

// TestDeployAppsScreen_RBACOmissionOnBytes proves a non-admin never even gets
// a deploy.apps envelope AT ALL (whole-screen ErrScreenNotFound) — the
// stronger form of RBAC omission compared to Docker's per-row-action
// omission, since deploy.apps has no non-admin view whatsoever.
func TestDeployAppsScreen_RBACOmissionOnBytes(t *testing.T) {
	admin, nonAdmin := testMiscViewers()

	adminEnv, err := buildDeployAppsScreenForViewer(admin)
	if err != nil {
		t.Fatalf("admin: %v", err)
	}
	adminBytes, err := json.Marshal(adminEnv)
	if err != nil {
		t.Fatalf("marshal admin: %v", err)
	}
	if !strings.Contains(string(adminBytes), miscActionDeployAppDelete) {
		t.Errorf("admin envelope does not contain %q: %s", miscActionDeployAppDelete, adminBytes)
	}

	if _, err := buildDeployAppsScreenForViewer(nonAdmin); err != sdui.ErrScreenNotFound {
		t.Errorf("non-admin: err = %v, want sdui.ErrScreenNotFound (no envelope at all to inspect)", err)
	}
}

// TestQueueJobsScreen_NonVacuityOnBytes proves non-destructive retry stays
// visible for every viewer's envelope even though cancel is destructive — the
// non-vacuity half of the RBAC-on-bytes proof, applied here to queue.jobs
// (which has no role-based omission of its own, per RegisterMisc's ledger
// comment — the interesting boundary is ownership, covered in
// misc_actions_test.go, not a role split at the screen level).
func TestQueueJobsScreen_NonVacuityOnBytes(t *testing.T) {
	admin, nonAdmin := testMiscViewers()
	for name, v := range map[string]sdui.Viewer{"admin": admin, "nonadmin": nonAdmin} {
		body, err := json.Marshal(buildQueueJobsScreen(v))
		if err != nil {
			t.Fatalf("%s: marshal: %v", name, err)
		}
		for _, want := range []string{miscActionQueueJobRerun, miscActionQueueJobCancel} {
			if !strings.Contains(string(body), want) {
				t.Errorf("%s: envelope does not contain %q: %s", name, want, body)
			}
		}
	}
}

// --- Test 4: no client-side logic + preformatted values ---------------------

// TestMiscScreens_NoClientSideLogicKeys mirrors
// TestDockerScreens_NoClientSideLogicKeys exactly: no component payload in
// this batch may carry a client-evaluated condition/expression/visibility key,
// and no "permission..." key may exist beyond permission_hint.
func TestMiscScreens_NoClientSideLogicKeys(t *testing.T) {
	admin, nonAdmin := testMiscViewers()
	aiEnv, err := buildAISettingsScreenForViewer(admin, testMiscDeps())
	if err != nil {
		t.Fatalf("buildAISettingsScreenForViewer: %v", err)
	}
	deployEnv, err := buildDeployAppsScreenForViewer(admin)
	if err != nil {
		t.Fatalf("buildDeployAppsScreenForViewer: %v", err)
	}
	envs := map[string]*sdui.Envelope{
		"ai.settings":         aiEnv,
		"jira.issues.connect": buildJiraIssuesScreen(nonAdmin, testMiscDeps()),
		"jira.issues.full":    buildJiraIssuesConnectedScreen(),
		"deploy.apps":         deployEnv,
		"queue.jobs":          buildQueueJobsScreen(nonAdmin),
	}
	forbidden := []string{"\"condition\"", "\"visible_when\"", "\"expression\""}
	for name, env := range envs {
		body, err := json.Marshal(env)
		if err != nil {
			t.Fatalf("%s: marshal: %v", name, err)
		}
		for _, key := range forbidden {
			if strings.Contains(string(body), key) {
				t.Errorf("%s: payload contains forbidden client-side logic key %s: %s", name, key, body)
			}
		}
		idx := 0
		for {
			i := strings.Index(string(body)[idx:], "\"permission")
			if i < 0 {
				break
			}
			i += idx
			if !strings.HasPrefix(string(body)[i:], "\"permission_hint\"") {
				end := i + 40
				if end > len(body) {
					end = len(body)
				}
				t.Errorf("%s: payload contains a \"permission...\" key that is not permission_hint, around: %s", name, string(body)[i:end])
			}
			idx = i + len("\"permission")
		}
	}
}

// TestFormatMiscTimestamp_ZeroIsEmpty proves the zero-epoch convention this
// package's row-shaping helpers rely on.
func TestFormatMiscTimestamp_ZeroIsEmpty(t *testing.T) {
	if got := formatMiscTimestamp(0); got != "" {
		t.Errorf("formatMiscTimestamp(0) = %q, want \"\"", got)
	}
	if got := formatMiscTimestamp(1798000000); got == "" {
		t.Errorf("formatMiscTimestamp(1798000000) = \"\", want a non-empty string")
	}
}

// TestMiscRowShapingFuncs_NeverEmitRawNumbers proves every row-shaping
// function in this file always produces display-ready strings, never a raw
// number/epoch/struct — mirrors
// TestDockerRowShapingFuncs_NeverEmitRawNumbers's synthetic-domain-value
// pattern.
func TestMiscRowShapingFuncs_NeverEmitRawNumbers(t *testing.T) {
	issueRow := jiraIssueRow(jira.Issue{
		Key: "PROJ-1", Summary: "algo", Status: jira.Status{Name: "Aberto"},
		Assignee: &jira.User{DisplayName: "Fulano"},
	})
	if _, ok := issueRow["assignee"].(string); !ok {
		t.Errorf("jiraIssueRow[\"assignee\"] = %v (%T), want string", issueRow["assignee"], issueRow["assignee"])
	}

	noAssignee := jiraIssueRow(jira.Issue{Key: "PROJ-2", Summary: "outro", Status: jira.Status{Name: "Feito"}})
	if v, ok := noAssignee["assignee"].(string); !ok || v != "" {
		t.Errorf("jiraIssueRow without an assignee: %v (%T), want an empty string", noAssignee["assignee"], noAssignee["assignee"])
	}

	appRow := deployAppRow(deploy.App{
		Name: "app1", Domain: "app1.example.com", Branch: "main", Updated: 1798000000,
		Deploys: []deploy.DeployRecord{{ID: "d1", Status: "success"}},
	})
	if _, ok := appRow["updated"].(string); !ok {
		t.Errorf("deployAppRow[\"updated\"] = %v (%T), want string", appRow["updated"], appRow["updated"])
	}
	if v, ok := appRow["last_status"].(string); !ok || v != "success" {
		t.Errorf("deployAppRow[\"last_status\"] = %v (%T), want \"success\"", appRow["last_status"], appRow["last_status"])
	}

	jobRow := queueJobRow(&queue.Job{ID: "j1", Kind: "app_deploy", Status: queue.StatusRunning, Progress: 42, Owner: "misc-user", Queued: 1798000000})
	if v, ok := jobRow["progress"].(string); !ok || v != "42%" {
		t.Errorf("queueJobRow[\"progress\"] = %v (%T), want \"42%%\"", jobRow["progress"], jobRow["progress"])
	}
	if _, ok := jobRow["queued"].(string); !ok {
		t.Errorf("queueJobRow[\"queued\"] = %v (%T), want string", jobRow["queued"], jobRow["queued"])
	}
	if _, ok := jobRow["status"].(string); !ok {
		t.Errorf("queueJobRow[\"status\"] = %v (%T), want string", jobRow["status"], jobRow["status"])
	}
}

// --- Test 5: vocabulary boundary — no board/kanban/column in jira.issues ---

// TestJiraIssuesScreen_NeverEmitsBoardVocabulary is the explicit,
// PLAN.md-mandated boundary test: no component anywhere in jira.issues'
// payload (connected OR not-connected) may have a type/id/label containing
// "board", "kanban" or "column" — the kanban board stays desktop-only
// PERMANENTLY (see misc.go's package doc comment and
// buildJiraIssuesConnectedScreen's own doc comment).
func TestJiraIssuesScreen_NeverEmitsBoardVocabulary(t *testing.T) {
	_, nonAdmin := testMiscViewers()

	notConnected := buildJiraIssuesScreen(nonAdmin, testMiscDeps())
	connected := buildJiraIssuesScreen(nonAdmin, testMiscDepsJiraConnected(nonAdmin.Username))

	// Word-boundary match, not a raw substring: TableComponent's own
	// "columns" JSON key (its list of TableColumn — an entirely legitimate,
	// unrelated field every table in this package emits) would otherwise
	// false-positive on a bare Contains(..., "column"). \bcolumn\b matches a
	// standalone "column" (a board's column) but not "columns" (a table's
	// column list) since "s" continues the word.
	forbiddenRe := regexp.MustCompile(`(?i)\b(board|kanban|column)\b`)
	for name, env := range map[string]*sdui.Envelope{"not-connected": notConnected, "connected": connected} {
		body, err := json.Marshal(env)
		if err != nil {
			t.Fatalf("%s: marshal: %v", name, err)
		}
		if m := forbiddenRe.FindString(string(body)); m != "" {
			t.Errorf("%s: jira.issues payload contains the forbidden word %q (kanban board vocabulary is permanently out of scope for mobile): %s", name, m, body)
		}
	}
}
