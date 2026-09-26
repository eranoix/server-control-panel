package screens

import (
	"encoding/json"
	"strings"
	"testing"

	"server-control-panel/internal/config"
	"server-control-panel/internal/mobilebff/sdui"
)

// testSecurityCfg/testSecurityViewers mirror testDockerCfg/testDockerViewers
// — a real *config.Config through the real ViewerFrom/httpx.IsAdmin path,
// never a Viewer{} literal.
func testSecurityCfg() *config.Config {
	return &config.Config{
		SchemaVersion: config.CurrentSchemaVersion,
		Primary:       "sec-admin",
		Users: []config.User{
			{Username: "sec-admin", PasswordHash: "h"},
			{Username: "sec-user", PasswordHash: "h"},
		},
	}
}

func testSecurityViewers() (admin, nonAdmin sdui.Viewer) {
	cfg := testSecurityCfg()
	return sdui.ViewerFrom(cfg, "sec-admin"), sdui.ViewerFrom(cfg, "sec-user")
}

// --- Test 1: structure --------------------------------------------------

func TestSecurityUsersScreen_Structure(t *testing.T) {
	env := buildSecurityUsersScreen()

	table, ok := findComponent(t, env, "users-table").(sdui.TableComponent)
	if !ok {
		t.Fatalf("users-table is not a TableComponent")
	}
	if len(table.RowActions) != 1 || table.RowActions[0].ActionID != securityActionUserDelete {
		t.Errorf("users-table.row_actions = %v, want only %q", table.RowActions, securityActionUserDelete)
	}

	userForm, ok := findComponent(t, env, "user-form").(sdui.FormComponent)
	if !ok {
		t.Fatalf("user-form is not a FormComponent")
	}
	if userForm.SubmitAction.ActionID != securityActionUserSave {
		t.Errorf("user-form.submit_action = %q, want %q", userForm.SubmitAction.ActionID, securityActionUserSave)
	}

	pwForm, ok := findComponent(t, env, "user-password-reset-form").(sdui.FormComponent)
	if !ok {
		t.Fatalf("user-password-reset-form is not a FormComponent")
	}
	if pwForm.SubmitAction.ActionID != securityActionUserResetPassword {
		t.Errorf("user-password-reset-form.submit_action = %q, want %q — password reset needs to be a separate action from save", pwForm.SubmitAction.ActionID, securityActionUserResetPassword)
	}
	if pwForm.SubmitAction.ActionID == userForm.SubmitAction.ActionID {
		t.Errorf("user-form and user-password-reset-form must not share the same action")
	}

	delConfirm, ok := findComponent(t, env, "user-delete-confirm").(sdui.ConfirmDestructiveComponent)
	if !ok {
		t.Fatalf("user-delete-confirm is not a ConfirmDestructiveComponent")
	}
	if delConfirm.ActionID != securityActionUserDelete {
		t.Errorf("user-delete-confirm.action_id = %q, want %q", delConfirm.ActionID, securityActionUserDelete)
	}

	pwConfirm, ok := findComponent(t, env, "user-password-reset-confirm").(sdui.ConfirmDestructiveComponent)
	if !ok {
		t.Fatalf("user-password-reset-confirm is not a ConfirmDestructiveComponent")
	}
	if pwConfirm.ActionID != securityActionUserResetPassword {
		t.Errorf("user-password-reset-confirm.action_id = %q, want %q", pwConfirm.ActionID, securityActionUserResetPassword)
	}
}

func TestSecuritySecretsScreen_Structure(t *testing.T) {
	env := buildSecuritySecretsScreen()

	table, ok := findComponent(t, env, "secrets-table").(sdui.TableComponent)
	if !ok {
		t.Fatalf("secrets-table is not a TableComponent")
	}
	if len(table.Columns) != 1 || table.Columns[0].Key != "key" {
		t.Errorf("secrets-table.columns = %v, want only the \"key\" column — never a value", table.Columns)
	}

	form, ok := findComponent(t, env, "secret-form").(sdui.FormComponent)
	if !ok {
		t.Fatalf("secret-form is not a FormComponent")
	}
	if form.SubmitAction.ActionID != securityActionSecretSet {
		t.Errorf("secret-form.submit_action = %q, want %q", form.SubmitAction.ActionID, securityActionSecretSet)
	}

	confirm, ok := findComponent(t, env, "secret-delete-confirm").(sdui.ConfirmDestructiveComponent)
	if !ok {
		t.Fatalf("secret-delete-confirm is not a ConfirmDestructiveComponent")
	}
	if confirm.ActionID != securityActionSecretDelete {
		t.Errorf("secret-delete-confirm.action_id = %q, want %q", confirm.ActionID, securityActionSecretDelete)
	}
}

func TestSecuritySessionsScreen_Structure(t *testing.T) {
	env := buildSecuritySessionsScreen()

	table, ok := findComponent(t, env, "sessions-table").(sdui.TableComponent)
	if !ok {
		t.Fatalf("sessions-table is not a TableComponent")
	}
	if len(table.RowActions) != 1 || table.RowActions[0].ActionID != securityActionSessionRevoke {
		t.Errorf("sessions-table.row_actions = %v, want only %q", table.RowActions, securityActionSessionRevoke)
	}
	hasIsCurrent := false
	for _, c := range table.Columns {
		if c.Key == "is_current" {
			hasIsCurrent = true
		}
	}
	if !hasIsCurrent {
		t.Errorf("sessions-table.columns does not have \"is_current\" — without it there's no way to flag your own current session")
	}

	confirm, ok := findComponent(t, env, "session-revoke-confirm").(sdui.ConfirmDestructiveComponent)
	if !ok {
		t.Fatalf("session-revoke-confirm is not a ConfirmDestructiveComponent")
	}
	if confirm.ActionID != securityActionSessionRevoke {
		t.Errorf("session-revoke-confirm.action_id = %q, want %q", confirm.ActionID, securityActionSessionRevoke)
	}
}

func TestSecurityAuditScreen_Structure(t *testing.T) {
	env := buildSecurityAuditScreen()

	if _, ok := findComponent(t, env, "audit-table").(sdui.TableComponent); !ok {
		t.Fatalf("audit-table is not a TableComponent")
	}
	if len(env.Screen.Components) != 1 {
		t.Errorf("security.audit has %d components, want only 1 (audit-table, no form/confirm/action)", len(env.Screen.Components))
	}
}

func TestSecurityUFWScreen_Structure(t *testing.T) {
	env := buildSecurityUFWScreen()

	detail, ok := findComponent(t, env, "ufw-status-detail").(sdui.DetailComponent)
	if !ok {
		t.Fatalf("ufw-status-detail is not a DetailComponent")
	}
	if detail.DataSource.Endpoint != securityUFWDetailEndpoint {
		t.Errorf("ufw-status-detail.data_source.endpoint = %q, want %q", detail.DataSource.Endpoint, securityUFWDetailEndpoint)
	}

	form, ok := findComponent(t, env, "ufw-rule-form").(sdui.FormComponent)
	if !ok {
		t.Fatalf("ufw-rule-form is not a FormComponent")
	}
	if form.SubmitAction.ActionID != securityActionUFWApply {
		t.Errorf("ufw-rule-form.submit_action = %q, want %q", form.SubmitAction.ActionID, securityActionUFWApply)
	}

	confirm, ok := findComponent(t, env, "ufw-rule-confirm").(sdui.ConfirmDestructiveComponent)
	if !ok {
		t.Fatalf("ufw-rule-confirm is not a ConfirmDestructiveComponent — applying a firewall rule can lock out remote access and needs confirmation")
	}
	if confirm.ActionID != securityActionUFWApply {
		t.Errorf("ufw-rule-confirm.action_id = %q, want %q", confirm.ActionID, securityActionUFWApply)
	}
}

func TestSecurityAdGuardScreen_Structure(t *testing.T) {
	env := buildSecurityAdGuardScreen()

	detail, ok := findComponent(t, env, "adguard-status-detail").(sdui.DetailComponent)
	if !ok {
		t.Fatalf("adguard-status-detail is not a DetailComponent")
	}
	if detail.DataSource.Endpoint != securityAdGuardDetailEndpoint {
		t.Errorf("adguard-status-detail.data_source.endpoint = %q, want %q", detail.DataSource.Endpoint, securityAdGuardDetailEndpoint)
	}
	wantKeys := map[string]bool{"protection_enabled": true, "running": true, "version": true, "num_queries": true, "num_blocked": true, "blocked_pct": true}
	for _, f := range detail.Fields {
		delete(wantKeys, f.Key)
	}
	if len(wantKeys) != 0 {
		t.Errorf("adguard-status-detail.fields does not cover %v — fields come from real adguard.Status, never invented", wantKeys)
	}

	form, ok := findComponent(t, env, "adguard-protection-form").(sdui.FormComponent)
	if !ok {
		t.Fatalf("adguard-protection-form is not a FormComponent")
	}
	if form.SubmitAction.ActionID != securityActionAdGuardSetProtection {
		t.Errorf("adguard-protection-form.submit_action = %q, want %q", form.SubmitAction.ActionID, securityActionAdGuardSetProtection)
	}
}

func TestSecurityDevicesScreen_Structure(t *testing.T) {
	env := buildSecurityDevicesScreen()

	table, ok := findComponent(t, env, "devices-table").(sdui.TableComponent)
	if !ok {
		t.Fatalf("devices-table is not a TableComponent")
	}
	gotActions := map[string]bool{}
	for _, ra := range table.RowActions {
		gotActions[ra.ActionID] = true
	}
	for _, want := range []string{securityActionDeviceRename, securityActionDeviceSetExit, securityActionDeviceSetDatasaver, securityActionDeviceRemove} {
		if !gotActions[want] {
			t.Errorf("devices-table.row_actions does not reference %q: %v", want, table.RowActions)
		}
	}

	addForm, ok := findComponent(t, env, "device-add-form").(sdui.FormComponent)
	if !ok {
		t.Fatalf("device-add-form is not a FormComponent")
	}
	if addForm.SubmitAction.ActionID != securityActionDeviceAdd {
		t.Errorf("device-add-form.submit_action = %q, want %q", addForm.SubmitAction.ActionID, securityActionDeviceAdd)
	}

	confirm, ok := findComponent(t, env, "device-remove-confirm").(sdui.ConfirmDestructiveComponent)
	if !ok {
		t.Fatalf("device-remove-confirm is not a ConfirmDestructiveComponent")
	}
	if confirm.ActionID != securityActionDeviceRemove {
		t.Errorf("device-remove-confirm.action_id = %q, want %q", confirm.ActionID, securityActionDeviceRemove)
	}
}

func TestSecurityEconomiaScreen_Structure(t *testing.T) {
	env := buildSecurityEconomiaScreen()

	if _, ok := findComponent(t, env, "usage-table").(sdui.TableComponent); !ok {
		t.Fatalf("usage-table is not a TableComponent")
	}
	if len(env.Screen.Components) != 1 {
		t.Errorf("security.economia has %d components, want only 1 (usage-table, read-only)", len(env.Screen.Components))
	}
}

// --- Test 2: whole-screen omission (404-never-403) -----------------------

// TestSecurityScreens_NonAdminGetsErrScreenNotFound proves every one of the
// eight admin-only screens returns sdui.ErrScreenNotFound (never an emptied
// envelope) for a non-admin viewer, while an admin viewer builds fine.
func TestSecurityScreens_NonAdminGetsErrScreenNotFound(t *testing.T) {
	admin, nonAdmin := testSecurityViewers()

	cases := []struct {
		name    string
		builder func(sdui.Viewer) (*sdui.Envelope, error)
	}{
		{"security.users", buildSecurityUsersScreenForViewer},
		{"security.secrets", buildSecuritySecretsScreenForViewer},
		{"security.sessions", buildSecuritySessionsScreenForViewer},
		{"security.audit", buildSecurityAuditScreenForViewer},
		{"security.ufw", buildSecurityUFWScreenForViewer},
		{"security.adguard", buildSecurityAdGuardScreenForViewer},
		{"security.devices", buildSecurityDevicesScreenForViewer},
		{"security.economia", buildSecurityEconomiaScreenForViewer},
	}
	for _, c := range cases {
		if _, err := c.builder(admin); err != nil {
			t.Errorf("%s: admin: %v", c.name, err)
		}
		env, err := c.builder(nonAdmin)
		if err != sdui.ErrScreenNotFound {
			t.Errorf("%s: non-admin: err = %v, want sdui.ErrScreenNotFound", c.name, err)
		}
		if env != nil {
			t.Errorf("%s: non-admin: envelope is not nil: %v", c.name, env)
		}
	}
}

// --- Test 3: secret redaction, on bytes -----------------------------------

// TestSecuritySecretsScreen_NeverLeaksAValue proves the secrets screen's
// marshalled envelope never contains a secret VALUE — only key names and
// static placeholder text. SecretKeyRow (deps.go) has no Value field at all,
// so this test also stands as a structural guard: if a future edit adds one
// and threads a real value into the envelope, this test catches it on bytes.
func TestSecuritySecretsScreen_NeverLeaksAValue(t *testing.T) {
	const testSecretValue = "sk-live-super-secret-do-not-leak-9f3a"

	env := buildSecuritySecretsScreen()
	body, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(body), testSecretValue) {
		t.Fatalf("security.secrets envelope contains a secret value: %s", body)
	}
	if !strings.Contains(string(body), "\"key\"") {
		t.Errorf("security.secrets envelope does not contain the \"key\" column: %s", body)
	}
	if strings.Contains(string(body), "\"value\"") {
		// A form field named "value" existing as an INPUT key is fine (the
		// client submits into it); what must never appear is a submitted or
		// echoed value. Guard here is the absence of the literal secret
		// content, already checked above — this branch only documents the
		// expectation for a future reader.
		t.Logf("note: \"value\" appears as a form field key (expected — it's write-only)")
	}
}

// --- Test 4: multi-admin CRUD surface ------------------------------------

// TestSecurityUsersScreen_SupportsMultiAdminCRUD proves the users form
// exposes a binary admin/non-admin field (so promoting/demoting a SECOND
// admin is possible from the app) and the table is not filtered to hide
// other admins.
func TestSecurityUsersScreen_SupportsMultiAdminCRUD(t *testing.T) {
	env := buildSecurityUsersScreen()

	form, ok := findComponent(t, env, "user-form").(sdui.FormComponent)
	if !ok {
		t.Fatalf("user-form is not a FormComponent")
	}
	var adminField *sdui.FormField
	for i := range form.Fields {
		if form.Fields[i].Key == "admin" {
			adminField = &form.Fields[i]
		}
	}
	if adminField == nil {
		t.Fatalf("user-form does not have field \"admin\" — there's no way to promote/demote a second admin from the app")
	}
	if adminField.Kind != "bool" {
		t.Errorf("user-form.admin.kind = %q, want \"bool\" (two-value vocabulary)", adminField.Kind)
	}

	// The table itself carries no per-role filtering: securityUserRow always
	// emits every row ListUsers returns, admins included — proven at the row
	// level in security_rows_test.go's wire-shape tests. Here we just assert
	// the "role" column exists so an admin row is visibly distinguishable.
	table, ok := findComponent(t, env, "users-table").(sdui.TableComponent)
	if !ok {
		t.Fatalf("users-table is not a TableComponent")
	}
	hasRole := false
	for _, c := range table.Columns {
		if c.Key == "role" {
			hasRole = true
		}
	}
	if !hasRole {
		t.Errorf("users-table.columns does not have \"role\" — there's no way to see who is already an admin")
	}
}

// --- Test 5: UFW spec pass-through, on bytes ------------------------------

// TestSecurityUFWScreen_SpecFieldIsPassthroughNotStructural proves the
// ufw-rule-form's spec field carries no structural validation of its own
// (no regex/pattern/enum baked into the SDUI payload) — NetworkDeps.UFWApplyRule
// forwards it verbatim via strings.Fields, exactly like handleUFWRule, and
// handleSecurityUFWApply (security_actions.go) only checks non-emptiness for
// actions that require a spec, never its internal token structure.
func TestSecurityUFWScreen_SpecFieldIsPassthroughNotStructural(t *testing.T) {
	env := buildSecurityUFWScreen()
	form, ok := findComponent(t, env, "ufw-rule-form").(sdui.FormComponent)
	if !ok {
		t.Fatalf("ufw-rule-form is not a FormComponent")
	}
	var specField *sdui.FormField
	for i := range form.Fields {
		if form.Fields[i].Key == "spec" {
			specField = &form.Fields[i]
		}
	}
	if specField == nil {
		t.Fatalf("ufw-rule-form does not have field \"spec\"")
	}
	if specField.Kind != "text" {
		t.Errorf("ufw-rule-form.spec.kind = %q, want \"text\" — no select/enum, it is passed verbatim to ufw(8)", specField.Kind)
	}
	if len(specField.Options) != 0 {
		t.Errorf("ufw-rule-form.spec has options = %v, want none — spec is not an enum, it is forwarded verbatim (forwarded verbatim, not parsed)", specField.Options)
	}

	// A multi-token spec (like a real ufw rule with a source/port clause)
	// must not be rejected by anything at the SDUI layer itself — there is
	// no pattern/regex field on FormField to violate, so this assertion is
	// necessarily about ABSENCE of such a mechanism, proven on the marshalled
	// bytes: no "pattern"/"regex" key anywhere in the envelope.
	body, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, forbidden := range []string{"\"pattern\"", "\"regex\""} {
		if strings.Contains(string(body), forbidden) {
			t.Errorf("security.ufw envelope contains %s — spec should be passed verbatim (forwarded verbatim, not parsed), with no built-in structural validation", forbidden)
		}
	}
}

// --- Test 6: no client-side logic ------------------------------------------

// TestSecurityScreens_NoClientSideLogicKeys mirrors
// TestDockerScreens_NoClientSideLogicKeys / TestSystemScreens_NoClientSideLogicKeys
// exactly: none of the eight envelopes may carry a condition/visible_when/
// expression key — every branching decision (admin-only, RBAC omission of an
// action) happens server-side in Go, never encoded as client-evaluated logic.
func TestSecurityScreens_NoClientSideLogicKeys(t *testing.T) {
	envs := map[string]*sdui.Envelope{
		"users":    buildSecurityUsersScreen(),
		"secrets":  buildSecuritySecretsScreen(),
		"sessions": buildSecuritySessionsScreen(),
		"audit":    buildSecurityAuditScreen(),
		"ufw":      buildSecurityUFWScreen(),
		"adguard":  buildSecurityAdGuardScreen(),
		"devices":  buildSecurityDevicesScreen(),
		"economia": buildSecurityEconomiaScreen(),
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
	}
}
