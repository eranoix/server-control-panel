package screens

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/metrics"
	"server-control-panel/internal/mobilebff"
	"server-control-panel/internal/procs"
	"server-control-panel/internal/sysextra"
)

// fakeSystemRowsDeps builds a SystemDeps with only the List* fields
// populated — the rows/series endpoints never call a mutating closure, so
// KillProcess/UnitAction are deliberately left nil, mirroring
// fakeDockerRowsDeps' rationale.
func fakeSystemRowsDeps() SystemDeps {
	return SystemDeps{
		ListHistory: func() []metrics.Point {
			return []metrics.Point{
				{T: 1798000000, CPU: 12.3, MemUsed: 1024, MemPct: 45.6, Load1: 0.75, DiskPct: 30.1},
				{T: 1798000005, CPU: 13.1, MemUsed: 1030, MemPct: 45.9, Load1: 0.80, DiskPct: 30.1},
			}
		},
		ListProcesses: func(context.Context) ([]procs.Info, error) {
			return []procs.Info{
				{PID: 4242, Name: "nginx", User: "www-data", CPU: 0.5, Memory: 1.2, Status: "sleeping"},
			}, nil
		},
		ListPorts: func() ([]sysextra.Port, error) {
			return []sysextra.Port{
				{Proto: "tcp", Local: "0.0.0.0:22", Peer: "*:*", State: "LISTEN", Process: "sshd", PID: 100},
			}, nil
		},
		ListUnits: func() ([]sysextra.Unit, error) {
			return []sysextra.Unit{
				{Name: "sshd.service", Load: "loaded", Active: "active", Sub: "running", Description: "OpenSSH server"},
			}, nil
		},
	}
}

// newSystemRowsMux wires only the seven registerSystemXRows functions onto a
// throwaway huma.API — mirrors newDockerRowsMux's rationale: RegisterSystem
// itself goes through the process-global sdui registries (which panic on
// double-registration), so tests exercise the rows/series plumbing directly.
func newSystemRowsMux(deps SystemDeps) *http.ServeMux {
	mux := http.NewServeMux()
	api := humago.NewWithPrefix(mux, mobilebff.Prefix, huma.DefaultConfig("system-rows-test", "0"))
	mbDeps := mobilebff.Deps{Cfg: testSystemCfg()}
	registerSystemHistoryRows(api, deps, mbDeps)
	registerSystemProcessesRows(api, deps, mbDeps)
	registerSystemPortsRows(api, deps, mbDeps)
	registerSystemUnitsRows(api, deps, mbDeps)
	registerSystemMetricsCPURows(api, deps, mbDeps)
	registerSystemMetricsMemRows(api, deps, mbDeps)
	registerSystemMetricsDiskRows(api, deps, mbDeps)
	return mux
}

type systemRowsBody struct {
	Rows []map[string]any `json:"rows"`
}

func doSystemRowsRequest(t *testing.T, mux *http.ServeMux, path, username string) (*httptest.ResponseRecorder, systemRowsBody) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, mobilebff.Prefix+path, nil)
	if username != "" {
		req = req.WithContext(auth.WithUser(req.Context(), username))
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	var body systemRowsBody
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s: decode: %v (body=%s)", path, err, rec.Body.String())
		}
	}
	return rec, body
}

func TestSystemRows_Unauthenticated(t *testing.T) {
	mux := newSystemRowsMux(fakeSystemRowsDeps())
	paths := []string{
		"/system/history", "/system/processes", "/system/ports", "/system/units",
		"/system/metrics/cpu", "/system/metrics/mem", "/system/metrics/disk",
	}
	for _, path := range paths {
		rec, _ := doSystemRowsRequest(t, mux, path, "")
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: status = %d, want 401 (body=%s)", path, rec.Code, rec.Body.String())
		}
	}
}

// TestSystemHistoryRows_WireShape pins {"rows":[{"ts","cpu","mem","load1","disk"}]}.
func TestSystemHistoryRows_WireShape(t *testing.T) {
	mux := newSystemRowsMux(fakeSystemRowsDeps())
	rec, body := doSystemRowsRequest(t, mux, "/system/history", "sys-admin")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if len(body.Rows) != 2 {
		t.Fatalf("rows = %v, want 2 rows", body.Rows)
	}
	row := body.Rows[0]
	for _, k := range []string{"ts", "cpu", "mem", "load1", "disk"} {
		if _, ok := row[k]; !ok {
			t.Errorf("row does not have the %q key: %v", k, row)
		}
		if _, isString := row[k].(string); !isString {
			t.Errorf("%q = %v (%T), want a pre-formatted string", k, row[k], row[k])
		}
	}
}

// TestSystemProcessesRows_WireShape pins {"rows":[{"id","pid","name","user","cpu","mem","status"}]}.
func TestSystemProcessesRows_WireShape(t *testing.T) {
	mux := newSystemRowsMux(fakeSystemRowsDeps())
	rec, body := doSystemRowsRequest(t, mux, "/system/processes", "sys-admin")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if len(body.Rows) != 1 {
		t.Fatalf("rows = %v, want 1 row", body.Rows)
	}
	row := body.Rows[0]
	for _, k := range []string{"id", "pid", "name", "user", "cpu", "mem", "status"} {
		if _, ok := row[k]; !ok {
			t.Errorf("row does not have the %q key: %v", k, row)
		}
	}
	if row["id"] != "4242" || row["pid"] != "4242" {
		t.Errorf("id/pid = %v/%v, want \"4242\"/\"4242\"", row["id"], row["pid"])
	}
	if row["name"] != "nginx" {
		t.Errorf("name = %v, want \"nginx\"", row["name"])
	}
}

// TestSystemPortsRows_WireShape pins {"rows":[{"id","proto","local","peer","state","process"}]}.
func TestSystemPortsRows_WireShape(t *testing.T) {
	mux := newSystemRowsMux(fakeSystemRowsDeps())
	rec, body := doSystemRowsRequest(t, mux, "/system/ports", "sys-admin")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if len(body.Rows) != 1 {
		t.Fatalf("rows = %v, want 1 row", body.Rows)
	}
	row := body.Rows[0]
	for _, k := range []string{"id", "proto", "local", "peer", "state", "process"} {
		if _, ok := row[k]; !ok {
			t.Errorf("row does not have the %q key: %v", k, row)
		}
	}
	if row["id"] != "tcp:0.0.0.0:22" {
		t.Errorf("id = %v, want \"tcp:0.0.0.0:22\"", row["id"])
	}
}

// TestSystemUnitsRows_WireShape pins {"rows":[{"id","name","load","active","sub","description"}]}.
func TestSystemUnitsRows_WireShape(t *testing.T) {
	mux := newSystemRowsMux(fakeSystemRowsDeps())
	rec, body := doSystemRowsRequest(t, mux, "/system/units", "sys-admin")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if len(body.Rows) != 1 {
		t.Fatalf("rows = %v, want 1 row", body.Rows)
	}
	row := body.Rows[0]
	for _, k := range []string{"id", "name", "load", "active", "sub", "description"} {
		if _, ok := row[k]; !ok {
			t.Errorf("row does not have the %q key: %v", k, row)
		}
	}
	if row["name"] != "sshd.service" || row["active"] != "active" {
		t.Errorf("row = %v, want name=sshd.service active=active", row)
	}
}

// TestSystemMetricsChartRows_WireShape is THE round-trip test pinning
// chart's data shape for the first time in this project: GET
// <series_source.endpoint> returns {"rows":[{"ts":"<pre-formatted
// label>","value":"<plain numeric string, no unit suffix>"}, ...]}, exactly
// matching ChartComponent's XKey="ts"/YKey="value" set in
// buildSystemMetricsScreen. Checked for all three chart series
// (cpu/mem/disk) since each hits a different extractor field on the same
// metrics.Point.
func TestSystemMetricsChartRows_WireShape(t *testing.T) {
	mux := newSystemRowsMux(fakeSystemRowsDeps())
	cases := []struct {
		path      string
		wantFirst string
	}{
		{"/system/metrics/cpu", "12.3"},
		{"/system/metrics/mem", "45.6"},
		{"/system/metrics/disk", "30.1"},
	}
	for _, c := range cases {
		rec, body := doSystemRowsRequest(t, mux, c.path, "sys-admin")
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200 (body=%s)", c.path, rec.Code, rec.Body.String())
		}
		if len(body.Rows) != 2 {
			t.Fatalf("%s: rows = %v, want 2 rows (the default window fits both fake samples)", c.path, body.Rows)
		}
		row := body.Rows[0]
		if len(row) != 2 {
			t.Errorf("%s: row has extra keys besides ts/value: %v", c.path, row)
		}
		ts, ok := row["ts"].(string)
		if !ok || ts == "" {
			t.Errorf("%s: ts = %v (%T), want a non-empty pre-formatted string", c.path, row["ts"], row["ts"])
		}
		value, ok := row["value"].(string)
		if !ok {
			t.Errorf("%s: value = %v (%T), want string", c.path, row["value"], row["value"])
		}
		if value != c.wantFirst {
			t.Errorf("%s: value = %q, want %q", c.path, value, c.wantFirst)
		}
	}
}

// TestSystemMetricsChartRows_WindowControlsSampleCount proves the trailing
// sample count returned by each chart series changes with the caller's
// saved window preference — the mechanism system.metrics.window actually
// controls, per systemMetricsWindowMu's doc comment in system.go. Uses a
// unique username so this test's writes cannot leak into any other test's
// assertions.
func TestSystemMetricsChartRows_WindowControlsSampleCount(t *testing.T) {
	const username = "sys-window-test-rows"
	points := make([]metrics.Point, 0, 1440)
	for i := 0; i < 1440; i++ {
		points = append(points, metrics.Point{T: int64(1798000000 + i*5), CPU: float64(i)})
	}
	deps := fakeSystemRowsDeps()
	deps.ListHistory = func() []metrics.Point { return points }
	mux := newSystemRowsMux(deps)

	setSystemMetricsWindow(username, "30m")
	_, body30m := doSystemRowsRequest(t, mux, "/system/metrics/cpu", username)
	if len(body30m.Rows) != systemMetricsWindowSamples["30m"] {
		t.Errorf("window 30m: rows = %d, want %d", len(body30m.Rows), systemMetricsWindowSamples["30m"])
	}

	setSystemMetricsWindow(username, "1h")
	_, body1h := doSystemRowsRequest(t, mux, "/system/metrics/cpu", username)
	if len(body1h.Rows) != systemMetricsWindowSamples["1h"] {
		t.Errorf("window 1h: rows = %d, want %d", len(body1h.Rows), systemMetricsWindowSamples["1h"])
	}

	if len(body1h.Rows) <= len(body30m.Rows) {
		t.Errorf("1h (%d) should bring more samples than 30m (%d)", len(body1h.Rows), len(body30m.Rows))
	}
}

// TestSystemRows_SameShapeForAdminAndNonAdmin proves history/processes/
// ports/units rows are identical for admin and non-admin — RBAC-by-omission
// in this package only ever removes ACTIONS from a screen, never rows;
// internal/procs and internal/sysextra apply no per-viewer scoping to
// either list (verified against handlers_system.go/handlers_procs.go).
func TestSystemRows_SameShapeForAdminAndNonAdmin(t *testing.T) {
	mux := newSystemRowsMux(fakeSystemRowsDeps())
	for _, path := range []string{"/system/history", "/system/processes", "/system/ports", "/system/units"} {
		_, adminBody := doSystemRowsRequest(t, mux, path, "sys-admin")
		_, userBody := doSystemRowsRequest(t, mux, path, "sys-user")
		adminJSON, _ := json.Marshal(adminBody)
		userJSON, _ := json.Marshal(userBody)
		if string(adminJSON) != string(userJSON) {
			t.Errorf("%s: admin and non-admin diverge: admin=%s non-admin=%s", path, adminJSON, userJSON)
		}
	}
}
