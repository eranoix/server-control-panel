package screens

import (
	"encoding/json"
	"strings"
	"testing"

	"server-control-panel/internal/config"
	"server-control-panel/internal/metrics"
	"server-control-panel/internal/mobilebff/sdui"
	"server-control-panel/internal/procs"
	"server-control-panel/internal/sysextra"
)

// testSystemCfg/testSystemViewers mirror testDockerCfg/testDockerViewers — a
// real *config.Config through the real ViewerFrom/httpx.IsAdmin path, never
// a Viewer{} literal.
func testSystemCfg() *config.Config {
	return &config.Config{
		SchemaVersion: config.CurrentSchemaVersion,
		Primary:       "sys-admin",
		Users: []config.User{
			{Username: "sys-admin", PasswordHash: "h"},
			{Username: "sys-user", PasswordHash: "h"},
		},
	}
}

func testSystemViewers() (admin, nonAdmin sdui.Viewer) {
	cfg := testSystemCfg()
	return sdui.ViewerFrom(cfg, "sys-admin"), sdui.ViewerFrom(cfg, "sys-user")
}

// --- Test 1: structure -------------------------------------------------

func TestSystemHistoryScreen_Structure(t *testing.T) {
	env := buildSystemHistoryScreen()

	counts := map[sdui.ComponentType]int{}
	for _, c := range env.Screen.Components {
		counts[c.ComponentType()]++
	}
	if counts[sdui.ComponentTypeTable] != 1 {
		t.Errorf("system.history components = %v, want exactly 1 table", counts)
	}

	table, ok := findComponent(t, env, "history-table").(sdui.TableComponent)
	if !ok {
		t.Fatalf("history-table is not a TableComponent")
	}
	if len(table.RowActions) != 0 {
		t.Errorf("history-table.row_actions = %v, want empty", table.RowActions)
	}
}

func TestSystemProcessesScreen_Structure(t *testing.T) {
	admin, _ := testSystemViewers()
	env := buildSystemProcessesScreen(admin)

	table, ok := findComponent(t, env, "processes-table").(sdui.TableComponent)
	if !ok {
		t.Fatalf("processes-table is not a TableComponent")
	}
	if len(table.RowActions) != 1 || table.RowActions[0].ActionID != systemActionProcessKill {
		t.Errorf("processes-table.row_actions = %v, want only %q", table.RowActions, systemActionProcessKill)
	}

	confirm, ok := findComponent(t, env, "process-kill-confirm").(sdui.ConfirmDestructiveComponent)
	if !ok {
		t.Fatalf("process-kill-confirm is not a ConfirmDestructiveComponent")
	}
	if confirm.ActionID != systemActionProcessKill {
		t.Errorf("process-kill-confirm.action_id = %q, want %q", confirm.ActionID, systemActionProcessKill)
	}
}

// TestSystemPortsScreen_ListOnlyForEveryViewer proves system.ports never
// carries a row action or a confirm_destructive component — no removal of a
// listening socket exists anywhere in this project.
func TestSystemPortsScreen_ListOnlyForEveryViewer(t *testing.T) {
	env := buildSystemPortsScreen()

	table, ok := findComponent(t, env, "ports-table").(sdui.TableComponent)
	if !ok {
		t.Fatalf("ports-table is not a TableComponent")
	}
	if len(table.RowActions) != 0 {
		t.Errorf("ports-table.row_actions = %v, want empty", table.RowActions)
	}
	for _, c := range env.Screen.Components {
		if c.ComponentType() == sdui.ComponentTypeConfirmDestructive {
			t.Errorf("system.ports contains a ConfirmDestructiveComponent (%s) — there should not be any", c.Base().ID)
		}
	}
}

func TestSystemSystemdScreen_Structure(t *testing.T) {
	admin, _ := testSystemViewers()
	env := buildSystemSystemdScreen(admin)

	table, ok := findComponent(t, env, "systemd-table").(sdui.TableComponent)
	if !ok {
		t.Fatalf("systemd-table is not a TableComponent")
	}
	gotActions := map[string]bool{}
	for _, ra := range table.RowActions {
		gotActions[ra.ActionID] = true
	}
	for _, want := range []string{
		systemActionUnitStart, systemActionUnitStop, systemActionUnitRestart,
		systemActionUnitEnable, systemActionUnitDisable,
	} {
		if !gotActions[want] {
			t.Errorf("systemd-table.row_actions does not reference %q: %v", want, table.RowActions)
		}
	}
	// No systemd action is destructive (restarting a service is not
	// irreversible), so there must be no confirm_destructive on this screen.
	for _, c := range env.Screen.Components {
		if c.ComponentType() == sdui.ComponentTypeConfirmDestructive {
			t.Errorf("system.systemd contains a ConfirmDestructiveComponent (%s) — no systemd action is destructive", c.Base().ID)
		}
	}
}

// TestSystemMetricsScreen_Structure proves the plan's own claim about
// chart's shape: exactly one form (the window selector) and three charts
// (cpu/mem/disk), no table — and that each chart's x_key/y_key/chart_kind
// match the pinned data shape (see systemMetricPointRow's doc comment).
func TestSystemMetricsScreen_Structure(t *testing.T) {
	admin, _ := testSystemViewers()
	env := buildSystemMetricsScreen(admin)

	counts := map[sdui.ComponentType]int{}
	for _, c := range env.Screen.Components {
		counts[c.ComponentType()]++
	}
	want := map[sdui.ComponentType]int{
		sdui.ComponentTypeForm:  1,
		sdui.ComponentTypeChart: 3,
	}
	for ct, n := range want {
		if counts[ct] != n {
			t.Errorf("components of type %q = %d, want %d (full count: %v)", ct, counts[ct], n, counts)
		}
	}
	if counts[sdui.ComponentTypeTable] != 0 {
		t.Errorf("system.metrics should not have any table: %v", counts)
	}

	form, ok := findComponent(t, env, "metrics-window-form").(sdui.FormComponent)
	if !ok {
		t.Fatalf("metrics-window-form is not a FormComponent")
	}
	if len(form.Fields) != 1 || form.Fields[0].Kind != "select" {
		t.Errorf("metrics-window-form.fields = %v, want a single field with Kind=select", form.Fields)
	}
	if form.SubmitAction.ActionID != systemActionMetricsWindow {
		t.Errorf("metrics-window-form.submit_action.action_id = %q, want %q", form.SubmitAction.ActionID, systemActionMetricsWindow)
	}

	for _, id := range []string{"cpu-chart", "mem-chart", "disk-chart"} {
		chart, ok := findComponent(t, env, id).(sdui.ChartComponent)
		if !ok {
			t.Fatalf("%s is not a ChartComponent", id)
		}
		if chart.ChartKind != "line" {
			t.Errorf("%s.chart_kind = %q, want \"line\"", id, chart.ChartKind)
		}
		if chart.XKey != "ts" || chart.YKey != "value" {
			t.Errorf("%s.x_key/y_key = %q/%q, want \"ts\"/\"value\"", id, chart.XKey, chart.YKey)
		}
		if chart.SeriesSource.Endpoint == "" {
			t.Errorf("%s.series_source.endpoint is empty", id)
		}
	}
}

// --- Test 2 (RBAC omission, on bytes) + Test 3 (non-vacuity) -----------

func TestSystemProcessesScreen_RBACOmissionOnBytes(t *testing.T) {
	admin, nonAdmin := testSystemViewers()

	adminBytes, err := json.Marshal(buildSystemProcessesScreen(admin))
	if err != nil {
		t.Fatalf("marshal admin: %v", err)
	}
	nonAdminBytes, err := json.Marshal(buildSystemProcessesScreen(nonAdmin))
	if err != nil {
		t.Fatalf("marshal non-admin: %v", err)
	}

	if !strings.Contains(string(adminBytes), systemActionProcessKill) {
		t.Errorf("admin envelope does not contain %q: %s", systemActionProcessKill, adminBytes)
	}
	if strings.Contains(string(nonAdminBytes), systemActionProcessKill) {
		t.Errorf("non-admin envelope contains %q: %s", systemActionProcessKill, nonAdminBytes)
	}
	// Non-vacuity: the table's columns stay present for the non-admin.
	for _, want := range []string{"\"pid\"", "\"name\"", "\"user\""} {
		if !strings.Contains(string(nonAdminBytes), want) {
			t.Errorf("non-admin envelope does not contain column %s (should stay visible): %s", want, nonAdminBytes)
		}
	}
}

func TestSystemSystemdScreen_RBACOmissionOnBytes(t *testing.T) {
	admin, nonAdmin := testSystemViewers()

	adminBytes, err := json.Marshal(buildSystemSystemdScreen(admin))
	if err != nil {
		t.Fatalf("marshal admin: %v", err)
	}
	nonAdminBytes, err := json.Marshal(buildSystemSystemdScreen(nonAdmin))
	if err != nil {
		t.Fatalf("marshal non-admin: %v", err)
	}

	for _, want := range []string{
		systemActionUnitStart, systemActionUnitStop, systemActionUnitRestart,
		systemActionUnitEnable, systemActionUnitDisable,
	} {
		if !strings.Contains(string(adminBytes), want) {
			t.Errorf("admin envelope does not contain %q: %s", want, adminBytes)
		}
		if strings.Contains(string(nonAdminBytes), want) {
			t.Errorf("non-admin envelope contains %q: %s", want, nonAdminBytes)
		}
	}
	// Non-vacuity: the table's columns stay present for the non-admin.
	if !strings.Contains(string(nonAdminBytes), "\"name\"") {
		t.Errorf("non-admin envelope does not contain the \"name\" column (should stay visible): %s", nonAdminBytes)
	}
}

// --- Test 4: no client-side logic ---------------------------------------

func TestSystemScreens_NoClientSideLogicKeys(t *testing.T) {
	admin, _ := testSystemViewers()
	envs := map[string]*sdui.Envelope{
		"history":   buildSystemHistoryScreen(),
		"processes": buildSystemProcessesScreen(admin),
		"ports":     buildSystemPortsScreen(),
		"systemd":   buildSystemSystemdScreen(admin),
		"metrics":   buildSystemMetricsScreen(admin),
	}
	forbidden := []string{"\"condition\"", "\"visible_when\"", "\"expression\""}
	for name, env := range envs {
		body, err := json.Marshal(env)
		if err != nil {
			t.Fatalf("%s: marshal: %v", name, err)
		}
		for _, key := range forbidden {
			if strings.Contains(string(body), key) {
				t.Errorf("%s: payload contains a forbidden client-side-logic key %s: %s", name, key, body)
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

// --- Test 5: preformatted values -----------------------------------------

func TestFormatSystemTimestamp_ZeroIsEmpty(t *testing.T) {
	if got := formatSystemTimestamp(0); got != "" {
		t.Errorf("formatSystemTimestamp(0) = %q, want \"\"", got)
	}
}

func TestFormatSystemPercentAndLoad(t *testing.T) {
	if got := formatSystemPercent(42.567); got != "42.6" {
		t.Errorf("formatSystemPercent(42.567) = %q, want \"42.6\"", got)
	}
	if got := formatSystemLoad(1.2345); got != "1.23" {
		t.Errorf("formatSystemLoad(1.2345) = %q, want \"1.23\"", got)
	}
}

// TestSystemRowShapingFuncs_NeverEmitRawNumbers proves the row-shaping
// functions always produce display-ready strings for numeric/timestamp
// keys, never a raw number — using synthetic domain values.
func TestSystemRowShapingFuncs_NeverEmitRawNumbers(t *testing.T) {
	procRow := systemProcessRow(procs.Info{PID: 123, Name: "sshd", User: "root", CPU: 0.5, Memory: 1.2, Status: "sleeping"})
	if _, isString := procRow["pid"].(string); !isString {
		t.Errorf("process row \"pid\" = %v (%T), want string", procRow["pid"], procRow["pid"])
	}
	if _, isString := procRow["cpu"].(string); !isString {
		t.Errorf("process row \"cpu\" = %v (%T), want string", procRow["cpu"], procRow["cpu"])
	}

	portRow := systemPortRow(sysextra.Port{Proto: "tcp", Local: "0.0.0.0:22", State: "LISTEN", Process: "sshd", PID: 123})
	if portRow["id"] != "tcp:0.0.0.0:22" {
		t.Errorf("port row \"id\" = %v, want \"tcp:0.0.0.0:22\"", portRow["id"])
	}

	unitRow := systemUnitRow(sysextra.Unit{Name: "sshd.service", Load: "loaded", Active: "active", Sub: "running"})
	if unitRow["name"] != "sshd.service" {
		t.Errorf("unit row \"name\" = %v, want \"sshd.service\"", unitRow["name"])
	}

	pointRow := systemMetricPointRow(metrics.Point{T: 1798000000, CPU: 12.34}, func(p metrics.Point) float64 { return p.CPU })
	if _, isString := pointRow["value"].(string); !isString {
		t.Errorf("metric point row \"value\" = %v (%T), want string", pointRow["value"], pointRow["value"])
	}
	if _, isString := pointRow["ts"].(string); !isString {
		t.Errorf("metric point row \"ts\" = %v (%T), want string", pointRow["ts"], pointRow["ts"])
	}
}
