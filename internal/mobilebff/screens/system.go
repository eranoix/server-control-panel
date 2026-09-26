package screens

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/config"
	"server-control-panel/internal/httpx"
	"server-control-panel/internal/metrics"
	"server-control-panel/internal/mobilebff"
	"server-control-panel/internal/mobilebff/sdui"
	"server-control-panel/internal/procs"
	"server-control-panel/internal/sysextra"
)

// Screen ids — also the golden fixture filename stems (system.history.*,
// etc, see contracts/sdui/fixtures/screens/).
const (
	systemHistoryScreenID   = "system.history"
	systemProcessesScreenID = "system.processes"
	systemPortsScreenID     = "system.ports"
	systemSystemdScreenID   = "system.systemd"
	systemMetricsScreenID   = "system.metrics"
)

// Rows endpoints — one per table/chart data source. Absolute paths (carry
// mobilebff.Prefix), same convention as dockerContainersRowsEndpoint. The
// three system.metrics.cpu/mem/disk endpoints are FIXED URLs that never
// carry a window query parameter — see systemMetricsWindowFor's doc comment
// below for why the window lives in server-side per-viewer state instead.
const (
	systemHistoryRowsEndpoint     = mobilebff.Prefix + "/system/history"
	systemProcessesRowsEndpoint   = mobilebff.Prefix + "/system/processes"
	systemPortsRowsEndpoint       = mobilebff.Prefix + "/system/ports"
	systemSystemdRowsEndpoint     = mobilebff.Prefix + "/system/units"
	systemMetricsCPURowsEndpoint  = mobilebff.Prefix + "/system/metrics/cpu"
	systemMetricsMemRowsEndpoint  = mobilebff.Prefix + "/system/metrics/mem"
	systemMetricsDiskRowsEndpoint = mobilebff.Prefix + "/system/metrics/disk"
)

// systemTimestampFormat mirrors dockerTimestampFormat but keeps seconds
// precision (Docker's is minute-precision) — internal/metrics.Ring is
// sampled every 5 seconds (startMetricsCollector, internal/api/api.go), so a
// minute-precision label would render several distinct history/metric
// samples with an identical, misleading timestamp.
const systemTimestampFormat = "2006-01-02 15:04:05 UTC"

// systemMetricsWindowMu/systemMetricsWindowByUser are the FIRST per-viewer,
// server-side mutable BFF-adapter state in this project. Every other Deps
// struct in this package is a pure delegation to an existing domain
// package; this one is not, and the reason is structural, not a shortcut:
//
// system.metrics.window's job is "let this viewer pick 30m/1h/2h for the
// three charts on system.metrics". The client's ActionResult vocabulary
// (actionregistry.go) only has two verbs — Patch (merge into one row by id)
// and Invalidate (refetch a fixed endpoint) — and ChartComponent's
// SeriesSource is a single fixed DataSource.Endpoint per instance, with no
// way for the server to say "and also change the URL you fetch next time".
// So the window preference cannot travel in the response to the action; it
// has to live somewhere the FIXED /system/metrics/{cpu,mem,disk} endpoints
// can read it back at request time. A per-username in-memory map is that
// somewhere: system.metrics.window's handler writes into it, and each
// metric's rows closure (registerSystemMetricRows) reads it via
// systemMetricsWindowFor(v.Username) to decide how many trailing samples of
// deps.ListHistory() to return, then the action's Invalidate:
// []string{"cpu-chart","mem-chart","disk-chart"} makes the client actually
// re-fetch those same fixed URLs and see the new window.
//
// Being package-level, this state is shared across every test in this
// binary — tests that touch it MUST use a unique username per test (never
// "golden-admin"/"golden-user", the golden corpus' fixed identities) so a
// preference written by one test cannot leak into another's assertions.
var (
	systemMetricsWindowMu     sync.RWMutex
	systemMetricsWindowByUser = map[string]string{}
)

// defaultSystemMetricsWindow is the window a viewer who never called
// system.metrics.window sees — including both golden-corpus viewers, which
// is why system.metrics can be byte-identical for admin and non-admin
// (screensWithNoRoleDifference in golden_test.go): the FormField's Value is
// per-username state, not per-role, and neither golden viewer has ever
// written to it.
const defaultSystemMetricsWindow = "30m"

// systemMetricsWindowSamples maps each allowed window to a trailing sample
// count of internal/metrics.Ring. The ring is pushed every 5 seconds
// (startMetricsCollector) with a default capacity of 1440 samples — exactly
// 2 hours — so all three options fit inside the ring's own retention with
// no rounding surprises: 30m=360, 1h=720, 2h=1440 (the ring's full
// capacity).
var systemMetricsWindowSamples = map[string]int{
	"30m": 360,
	"1h":  720,
	"2h":  1440,
}

// systemMetricsWindowFor reads the caller's saved window preference,
// defaulting to defaultSystemMetricsWindow when the viewer has never called
// system.metrics.window.
func systemMetricsWindowFor(username string) string {
	systemMetricsWindowMu.RLock()
	defer systemMetricsWindowMu.RUnlock()
	if w, ok := systemMetricsWindowByUser[username]; ok {
		return w
	}
	return defaultSystemMetricsWindow
}

// setSystemMetricsWindow writes the caller's window preference. Only
// system_actions.go's handleSystemMetricsWindow calls this, after
// validating window against systemMetricsWindowSamples' key set.
func setSystemMetricsWindow(username, window string) {
	systemMetricsWindowMu.Lock()
	defer systemMetricsWindowMu.Unlock()
	systemMetricsWindowByUser[username] = window
}

// RegisterSystem wires the five System screens, their actions and their
// rows/chart-series endpoints. Called explicitly by internal/api/api.go,
// mirroring RegisterDocker.
func RegisterSystem(deps SystemDeps) {
	sdui.Register(systemHistoryScreenID, func(_ context.Context, _ sdui.Viewer) (*sdui.Envelope, error) {
		return buildSystemHistoryScreen(), nil
	})
	sdui.Register(systemProcessesScreenID, func(_ context.Context, v sdui.Viewer) (*sdui.Envelope, error) {
		return buildSystemProcessesScreen(v), nil
	})
	sdui.Register(systemPortsScreenID, func(_ context.Context, _ sdui.Viewer) (*sdui.Envelope, error) {
		return buildSystemPortsScreen(), nil
	})
	sdui.Register(systemSystemdScreenID, func(_ context.Context, v sdui.Viewer) (*sdui.Envelope, error) {
		return buildSystemSystemdScreen(v), nil
	})
	sdui.Register(systemMetricsScreenID, func(_ context.Context, v sdui.Viewer) (*sdui.Envelope, error) {
		return buildSystemMetricsScreen(v), nil
	})

	// Catalog entries. None of the five is somenteAdmin: every builder
	// assembles the screen for any viewer — processes and systemd merely omit
	// the kill-process / restart-unit actions from inside the Envelope, which
	// is an ACTION filter, not a screen filter. "Metrics" names the three
	// series the screen carries, because the word alone does not say of what.
	sdui.RegisterCatalog(systemMetricsScreenID, sdui.GroupSistema, "Metrics (CPU, memory, disk)", sempreVisivel)
	sdui.RegisterCatalog(systemProcessesScreenID, sdui.GroupSistema, "Processes", sempreVisivel)
	sdui.RegisterCatalog(systemPortsScreenID, sdui.GroupSistema, "Listening ports", sempreVisivel)
	sdui.RegisterCatalog(systemSystemdScreenID, sdui.GroupSistema, "Services (systemd)", sempreVisivel)
	sdui.RegisterCatalog(systemHistoryScreenID, sdui.GroupSistema, "System history", sempreVisivel)

	registerSystemActions(deps)

	mobilebff.Register("system.history.rows", func(api huma.API, mbDeps mobilebff.Deps) {
		registerSystemHistoryRows(api, deps, mbDeps)
	})
	mobilebff.Register("system.processes.rows", func(api huma.API, mbDeps mobilebff.Deps) {
		registerSystemProcessesRows(api, deps, mbDeps)
	})
	mobilebff.Register("system.ports.rows", func(api huma.API, mbDeps mobilebff.Deps) {
		registerSystemPortsRows(api, deps, mbDeps)
	})
	mobilebff.Register("system.systemd.rows", func(api huma.API, mbDeps mobilebff.Deps) {
		registerSystemUnitsRows(api, deps, mbDeps)
	})
	mobilebff.Register("system.metrics.cpu.rows", func(api huma.API, mbDeps mobilebff.Deps) {
		registerSystemMetricsCPURows(api, deps, mbDeps)
	})
	mobilebff.Register("system.metrics.mem.rows", func(api huma.API, mbDeps mobilebff.Deps) {
		registerSystemMetricsMemRows(api, deps, mbDeps)
	})
	mobilebff.Register("system.metrics.disk.rows", func(api huma.API, mbDeps mobilebff.Deps) {
		registerSystemMetricsDiskRows(api, deps, mbDeps)
	})

	// Forbidden-for-non-admin ledger: processes hides its one destructive
	// row action (kill); systemd hides all five of its admin-only unit
	// actions. history, ports and system.metrics have no admin-only content
	// at all and are registered in screensWithNoRoleDifference
	// (golden_test.go) instead of here.
	sdui.RegisterForbiddenForNonAdmin(systemProcessesScreenID, func() []string {
		return []string{systemActionProcessKill}
	})
	sdui.RegisterForbiddenForNonAdmin(systemSystemdScreenID, func() []string {
		return []string{
			systemActionUnitStart,
			systemActionUnitStop,
			systemActionUnitRestart,
			systemActionUnitEnable,
			systemActionUnitDisable,
		}
	})
}

// buildSystemHistoryScreen builds the history-table screen: raw metrics
// samples (handleHistory's r.ring.Snapshot(), oldest to newest), open to
// every viewer — the web panel applies no admin gate to GET
// /api/system/history and neither does this screen.
func buildSystemHistoryScreen() *sdui.Envelope {
	table := sdui.TableComponent{
		ComponentBase: sdui.ComponentBase{Type: sdui.ComponentTypeTable, ID: "history-table"},
		Columns: []sdui.TableColumn{
			{Key: "ts", Label: "Time", Kind: "text"},
			{Key: "cpu", Label: "CPU %", Kind: "text"},
			{Key: "mem", Label: "Memory %", Kind: "text"},
			{Key: "load1", Label: "Load (1m)", Kind: "text"},
			{Key: "disk", Label: "Disk %", Kind: "text"},
		},
		RowsSource: sdui.DataSource{Endpoint: systemHistoryRowsEndpoint},
		EmptyState: &sdui.EmptyState{Text: "CPU, memory, load and disk samples, one per minute, oldest first. Empty means the panel restarted recently: this history lives in memory and starts over on every restart. The first rows appear within a few minutes, with nothing for you to do."},
	}
	screen := sdui.Screen{ID: systemHistoryScreenID, Title: "System history", Components: []sdui.Component{table}}
	return &sdui.Envelope{Screen: screen}
}

// buildSystemProcessesScreen builds the processes-table screen. kill is
// destructive and admin-only — see system_actions.go's doc comment on
// systemActionProcessKill for why ownership-based self-kill (which the web
// panel supports via procs.SignalAsOwner) is deliberately not reproduced
// here: the golden harness's binary admin/non-admin model has no way to
// express per-row, per-owner authorization.
func buildSystemProcessesScreen(v sdui.Viewer) *sdui.Envelope {
	table := sdui.TableComponent{
		ComponentBase: sdui.ComponentBase{Type: sdui.ComponentTypeTable, ID: "processes-table"},
		Columns: []sdui.TableColumn{
			{Key: "pid", Label: "PID", Kind: "text"},
			{Key: "name", Label: "Process", Kind: "text"},
			{Key: "user", Label: "User", Kind: "text"},
			{Key: "cpu", Label: "CPU %", Kind: "text"},
			{Key: "mem", Label: "Memory %", Kind: "text"},
			{Key: "status", Label: "Status", Kind: "text"},
		},
		RowsSource: sdui.DataSource{Endpoint: systemProcessesRowsEndpoint},
		RowActions: []sdui.ActionRef{
			{ActionID: systemActionProcessKill, Label: "Terminate", Style: "destructive"},
		},
		EmptyState: &sdui.EmptyState{Text: "Everything running on the server right now, with the CPU and memory use of each process. A running server never has zero processes, so an empty list here is a read failure, not an idle server. Reload the screen; if it stays empty, the problem is on the host, not in the app."},
	}
	if !v.IsAdmin() {
		sdui.DropRowActions(&table, func(a sdui.ActionRef) bool { return a.ActionID != systemActionProcessKill })
	}

	confirm := sdui.ConfirmDestructiveComponent{
		ComponentBase: sdui.ComponentBase{Type: sdui.ComponentTypeConfirmDestructive, ID: "process-kill-confirm"},
		ActionID:      systemActionProcessKill,
		Message:       "This process will receive SIGTERM. Critical system processes are protected and the attempt will be refused.",
	}

	screen := sdui.Screen{ID: systemProcessesScreenID, Title: "Processes", Components: []sdui.Component{table}}
	if v.IsAdmin() {
		screen.Components = append(screen.Components, confirm)
	}
	return &sdui.Envelope{Screen: screen}
}

// buildSystemPortsScreen builds the ports-table screen: list-only for every
// viewer, mirroring handleListening (sysextra.Listening()) — no admin gate,
// no removal action of any kind exists for a listening socket.
func buildSystemPortsScreen() *sdui.Envelope {
	table := sdui.TableComponent{
		ComponentBase: sdui.ComponentBase{Type: sdui.ComponentTypeTable, ID: "ports-table"},
		Columns: []sdui.TableColumn{
			{Key: "proto", Label: "Protocol", Kind: "text"},
			{Key: "local", Label: "Local", Kind: "text"},
			{Key: "peer", Label: "Peer", Kind: "text"},
			{Key: "state", Label: "State", Kind: "text"},
			{Key: "process", Label: "Process", Kind: "text"},
		},
		RowsSource: sdui.DataSource{Endpoint: systemPortsRowsEndpoint},
		EmptyState: &sdui.EmptyState{Text: "Each row is a listening socket: who answers on this server, on which port and through which process. Empty is suspicious — at the very least SSH and the panel itself should be here. It usually means the ss command answered with no data, not that the server stopped answering."},
	}
	screen := sdui.Screen{ID: systemPortsScreenID, Title: "Ports", Components: []sdui.Component{table}}
	return &sdui.Envelope{Screen: screen}
}

// buildSystemSystemdScreen builds the systemd-table screen. All five row
// actions (start/stop/restart/enable/disable) mirror handleUnitAction: every
// one of them is admin-only (mustPrimary) on the web panel, including plain
// restart — none is marked Destructive (a service restart is not
// irreversible the way a Docker prune or process kill is), so there is no
// confirm_destructive component here.
func buildSystemSystemdScreen(v sdui.Viewer) *sdui.Envelope {
	table := sdui.TableComponent{
		ComponentBase: sdui.ComponentBase{Type: sdui.ComponentTypeTable, ID: "systemd-table"},
		Columns: []sdui.TableColumn{
			{Key: "name", Label: "Unit", Kind: "text"},
			{Key: "load", Label: "Load", Kind: "text"},
			{Key: "active", Label: "Active", Kind: "badge", BadgeMap: map[string]string{
				"active": "success", "inactive": "neutral", "failed": "danger",
				"activating": "warning", "deactivating": "warning",
			}},
			{Key: "sub", Label: "Sub", Kind: "text"},
			{Key: "description", Label: "Description", Kind: "text"},
		},
		RowsSource: sdui.DataSource{Endpoint: systemSystemdRowsEndpoint},
		RowActions: []sdui.ActionRef{
			{ActionID: systemActionUnitStart, Label: "Start", Style: "secondary"},
			{ActionID: systemActionUnitStop, Label: "Stop", Style: "secondary"},
			{ActionID: systemActionUnitRestart, Label: "Restart", Style: "secondary"},
			{ActionID: systemActionUnitEnable, Label: "Enable", Style: "secondary"},
			{ActionID: systemActionUnitDisable, Label: "Disable", Style: "secondary"},
		},
		EmptyState: &sdui.EmptyState{Text: "The systemd .service units, active or not, with load, state and description. No host with systemd has zero services — even if it were only ssh.service. An empty list is an incomplete answer from systemctl, not a server without services."},
	}
	if !v.IsAdmin() {
		sdui.DropRowActions(&table, func(sdui.ActionRef) bool { return false })
	}
	screen := sdui.Screen{ID: systemSystemdScreenID, Title: "Services (systemd)", Components: []sdui.Component{table}}
	return &sdui.Envelope{Screen: screen}
}

// buildSystemMetricsScreen builds the metrics screen: one window-selector
// form plus three single-series charts (cpu/mem/disk) — ChartComponent has
// no multi-series support, so three separate instances is the only
// vocabulary-compliant way to show three metrics. Open to every viewer, no
// admin gate: history/metrics read has none on the web panel either. The
// form field's Value is the viewer's OWN saved window preference (see
// systemMetricsWindowFor), not a role-derived value — this is why
// system.metrics can still be byte-identical for the two golden viewers
// (screensWithNoRoleDifference), even though the value is per-viewer state.
func buildSystemMetricsScreen(v sdui.Viewer) *sdui.Envelope {
	window := systemMetricsWindowFor(v.Username)

	form := sdui.FormComponent{
		ComponentBase: sdui.ComponentBase{Type: sdui.ComponentTypeForm, ID: "metrics-window-form"},
		Fields: []sdui.FormField{
			{Key: "window", Label: "Window", Kind: "select", Options: []string{"30m", "1h", "2h"}, Value: window},
		},
		SubmitAction: sdui.ActionRef{ActionID: systemActionMetricsWindow, Label: "Apply", Style: "secondary"},
	}
	cpuChart := sdui.ChartComponent{
		ComponentBase: sdui.ComponentBase{Type: sdui.ComponentTypeChart, ID: "cpu-chart"},
		ChartKind:     "line",
		SeriesSource:  sdui.DataSource{Endpoint: systemMetricsCPURowsEndpoint},
		XKey:          "ts",
		YKey:          "value",
	}
	memChart := sdui.ChartComponent{
		ComponentBase: sdui.ComponentBase{Type: sdui.ComponentTypeChart, ID: "mem-chart"},
		ChartKind:     "line",
		SeriesSource:  sdui.DataSource{Endpoint: systemMetricsMemRowsEndpoint},
		XKey:          "ts",
		YKey:          "value",
	}
	diskChart := sdui.ChartComponent{
		ComponentBase: sdui.ComponentBase{Type: sdui.ComponentTypeChart, ID: "disk-chart"},
		ChartKind:     "line",
		SeriesSource:  sdui.DataSource{Endpoint: systemMetricsDiskRowsEndpoint},
		XKey:          "ts",
		YKey:          "value",
	}

	screen := sdui.Screen{
		ID:         systemMetricsScreenID,
		Title:      "Metrics",
		Components: []sdui.Component{form, cpuChart, memChart, diskChart},
	}
	return &sdui.Envelope{Screen: screen}
}

// --- Rows / series endpoints ------------------------------------------------

func registerSystemHistoryRows(api huma.API, deps SystemDeps, mbDeps mobilebff.Deps) {
	registerSystemRows(api, "getSystemHistoryRows", "/system/history", "Linhas de system.history", mbDeps.Cfg,
		func(_ context.Context, _ sdui.Viewer) ([]map[string]any, error) {
			points := deps.ListHistory()
			rows := make([]map[string]any, 0, len(points))
			for _, p := range points {
				rows = append(rows, systemHistoryRow(p))
			}
			return rows, nil
		})
}

func registerSystemProcessesRows(api huma.API, deps SystemDeps, mbDeps mobilebff.Deps) {
	registerSystemRows(api, "getSystemProcessesRows", "/system/processes", "Linhas de system.processes", mbDeps.Cfg,
		func(ctx context.Context, _ sdui.Viewer) ([]map[string]any, error) {
			list, err := deps.ListProcesses(ctx)
			if err != nil {
				return nil, err
			}
			rows := make([]map[string]any, 0, len(list))
			for _, p := range list {
				rows = append(rows, systemProcessRow(p))
			}
			return rows, nil
		})
}

func registerSystemPortsRows(api huma.API, deps SystemDeps, mbDeps mobilebff.Deps) {
	registerSystemRows(api, "getSystemPortsRows", "/system/ports", "Linhas de system.ports", mbDeps.Cfg,
		func(_ context.Context, _ sdui.Viewer) ([]map[string]any, error) {
			list, err := deps.ListPorts()
			if err != nil {
				return nil, err
			}
			rows := make([]map[string]any, 0, len(list))
			for _, p := range list {
				rows = append(rows, systemPortRow(p))
			}
			return rows, nil
		})
}

func registerSystemUnitsRows(api huma.API, deps SystemDeps, mbDeps mobilebff.Deps) {
	registerSystemRows(api, "getSystemUnitsRows", "/system/units", "Linhas de system.systemd", mbDeps.Cfg,
		func(_ context.Context, _ sdui.Viewer) ([]map[string]any, error) {
			list, err := deps.ListUnits()
			if err != nil {
				return nil, err
			}
			rows := make([]map[string]any, 0, len(list))
			for _, u := range list {
				rows = append(rows, systemUnitRow(u))
			}
			return rows, nil
		})
}

func registerSystemMetricsCPURows(api huma.API, deps SystemDeps, mbDeps mobilebff.Deps) {
	registerSystemMetricRows(api, deps, mbDeps, "cpu", "getSystemMetricsCPURows", "/system/metrics/cpu",
		func(p metrics.Point) float64 { return p.CPU })
}

func registerSystemMetricsMemRows(api huma.API, deps SystemDeps, mbDeps mobilebff.Deps) {
	registerSystemMetricRows(api, deps, mbDeps, "mem", "getSystemMetricsMemRows", "/system/metrics/mem",
		func(p metrics.Point) float64 { return p.MemPct })
}

func registerSystemMetricsDiskRows(api huma.API, deps SystemDeps, mbDeps mobilebff.Deps) {
	registerSystemMetricRows(api, deps, mbDeps, "disk", "getSystemMetricsDiskRows", "/system/metrics/disk",
		func(p metrics.Point) float64 { return p.DiskPct })
}

// registerSystemMetricRows is the shared plumbing behind the three
// system.metrics chart series. Unlike every other rows endpoint in this
// package, its fetch closure reads the caller's Viewer to resolve the
// window preference at REQUEST time (systemMetricsWindowFor) — the fixed
// endpoint URL never changes, only the trailing-sample count sliced out of
// deps.ListHistory() changes, per systemMetricsWindowMu's doc comment above.
func registerSystemMetricRows(api huma.API, deps SystemDeps, mbDeps mobilebff.Deps, metric, opID, path string, extract func(metrics.Point) float64) {
	registerSystemRows(api, opID, path, "Series of system.metrics ("+metric+")", mbDeps.Cfg,
		func(_ context.Context, v sdui.Viewer) ([]map[string]any, error) {
			points := deps.ListHistory()
			window := systemMetricsWindowFor(v.Username)
			n := systemMetricsWindowSamples[window]
			if n <= 0 || n > len(points) {
				n = len(points)
			}
			windowed := points[len(points)-n:]
			rows := make([]map[string]any, 0, len(windowed))
			for _, p := range windowed {
				rows = append(rows, systemMetricPointRow(p, extract))
			}
			return rows, nil
		})
}

// registerSystemRows is the shared plumbing for all seven System rows/series
// endpoints: authenticate, resolve Viewer, call fetch, wrap as
// {"rows":[...]} — the exact same wire shape 07-08 pinned for tables, now
// reused verbatim for the chart series (a chart's SeriesSource fetches
// through the identical envelope, just with "ts"/"value" keys instead of
// table columns).
func registerSystemRows(api huma.API, opID, path, summary string, cfg *config.Config, fetch func(context.Context, sdui.Viewer) ([]map[string]any, error)) {
	huma.Register(api, huma.Operation{
		OperationID: opID,
		Method:      http.MethodGet,
		Path:        path,
		Summary:     summary,
		Tags:        []string{"mobile", "sdui", "system"},
		Middlewares: huma.Middlewares{mobilebff.RequireAuth, serveSystemRows(cfg, fetch)},
	}, systemRowsDocHandler)
}

type systemRowsInput struct{}

type systemRowsOutput struct {
	Body json.RawMessage
}

func systemRowsDocHandler(_ context.Context, _ *systemRowsInput) (*systemRowsOutput, error) {
	return &systemRowsOutput{Body: json.RawMessage(`{"rows":[]}`)}, nil
}

func serveSystemRows(cfg *config.Config, fetch func(context.Context, sdui.Viewer) ([]map[string]any, error)) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, _ func(huma.Context)) {
		req, w := humago.Unwrap(ctx)

		username := auth.UserFrom(req)
		v := sdui.ViewerFrom(cfg, username)

		rows, err := fetch(req.Context(), v)
		if err != nil {
			log.Printf("mobilebff/screens: error fetching the system rows: %v", err)
			httpx.WriteErr(w, http.StatusInternalServerError, "internal_error")
			return
		}
		if rows == nil {
			rows = []map[string]any{}
		}

		body, err := json.Marshal(map[string]any{"rows": rows})
		if err != nil {
			log.Printf("mobilebff/screens: error serializing the system rows: %v", err)
			httpx.WriteErr(w, http.StatusInternalServerError, "internal_error")
			return
		}

		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}
}

// --- Row shaping -------------------------------------------------------------

// systemHistoryRow shapes one metrics.Point into the history-table row wire
// format. Wire shape: {"ts","cpu","mem","load1","disk"} — every numeric
// value is pre-formatted server-side, never a raw float.
func systemHistoryRow(p metrics.Point) map[string]any {
	return map[string]any{
		"ts":    formatSystemTimestamp(p.T),
		"cpu":   formatSystemPercent(p.CPU),
		"mem":   formatSystemPercent(p.MemPct),
		"load1": formatSystemLoad(p.Load1),
		"disk":  formatSystemPercent(p.DiskPct),
	}
}

// systemProcessRow shapes one procs.Info into the processes-table row wire
// format. Wire shape: {"id","pid","name","user","cpu","mem","status"} — "id"
// duplicates "pid" as a string so the client's generic row-identity code
// (which always reads "id") stays uniform, mirroring dockerComposeRow's
// "id"/"stack" duplication.
func systemProcessRow(p procs.Info) map[string]any {
	pid := strconv.Itoa(int(p.PID))
	return map[string]any{
		"id":     pid,
		"pid":    pid,
		"name":   p.Name,
		"user":   p.User,
		"cpu":    formatSystemPercent(p.CPU),
		"mem":    formatSystemPercent(p.Memory),
		"status": p.Status,
	}
}

// systemPortRow shapes one sysextra.Port into the ports-table row wire
// format. Wire shape: {"id","proto","local","peer","state","process"} — a
// listening socket has no natural single-field id, so "id" is synthesized
// from proto+local, the pair that is unique per row in ss's own output.
func systemPortRow(p sysextra.Port) map[string]any {
	return map[string]any{
		"id":      p.Proto + ":" + p.Local,
		"proto":   p.Proto,
		"local":   p.Local,
		"peer":    p.Peer,
		"state":   p.State,
		"process": p.Process,
	}
}

// systemUnitRow shapes one sysextra.Unit into the systemd-table row wire
// format. Wire shape: {"id","name","load","active","sub","description"}.
func systemUnitRow(u sysextra.Unit) map[string]any {
	return map[string]any{
		"id":          u.Name,
		"name":        u.Name,
		"load":        u.Load,
		"active":      u.Active,
		"sub":         u.Sub,
		"description": u.Description,
	}
}

// systemMetricPointRow shapes one metrics.Point into a chart series row —
// THE pinned system.metrics chart data shape: {"ts":"<pre-formatted
// label>","value":"<plain numeric string, no unit suffix>"}, matching
// ChartComponent's XKey/YKey ("ts"/"value") set in
// buildSystemMetricsScreen. extract picks which metric (CPU/MemPct/DiskPct)
// this particular series renders.
func systemMetricPointRow(p metrics.Point, extract func(metrics.Point) float64) map[string]any {
	return map[string]any{
		"ts":    formatSystemTimestamp(p.T),
		"value": formatSystemPercent(extract(p)),
	}
}

// formatSystemTimestamp renders a Unix epoch as the server-formatted display
// string every timestamp in these five screens uses — mirrors
// formatDockerTimestamp's zero-is-empty rule.
func formatSystemTimestamp(epoch int64) string {
	if epoch == 0 {
		return ""
	}
	return time.Unix(epoch, 0).UTC().Format(systemTimestampFormat)
}

// formatSystemPercent renders a float64 percentage as a plain, one-decimal
// numeric string with no "%" suffix — the client renders the unit, the
// server never bakes display formatting a locale change could break.
func formatSystemPercent(v float64) string {
	return strconv.FormatFloat(v, 'f', 1, 64)
}

// formatSystemLoad renders a load-average float64 as a plain, two-decimal
// numeric string (loadavg convention), no unit suffix.
func formatSystemLoad(v float64) string {
	return strconv.FormatFloat(v, 'f', 2, 64)
}
