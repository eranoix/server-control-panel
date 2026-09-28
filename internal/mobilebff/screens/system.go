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

const (
	systemHistoryScreenID   = "system.history"
	systemProcessesScreenID = "system.processes"
	systemPortsScreenID     = "system.ports"
	systemSystemdScreenID   = "system.systemd"
	systemMetricsScreenID   = "system.metrics"
)

const (
	systemHistoryRowsEndpoint     = mobilebff.Prefix + "/system/history"
	systemProcessesRowsEndpoint   = mobilebff.Prefix + "/system/processes"
	systemPortsRowsEndpoint       = mobilebff.Prefix + "/system/ports"
	systemSystemdRowsEndpoint     = mobilebff.Prefix + "/system/units"
	systemMetricsCPURowsEndpoint  = mobilebff.Prefix + "/system/metrics/cpu"
	systemMetricsMemRowsEndpoint  = mobilebff.Prefix + "/system/metrics/mem"
	systemMetricsDiskRowsEndpoint = mobilebff.Prefix + "/system/metrics/disk"
)

const systemTimestampFormat = "2006-01-02 15:04:05 UTC"

var (
	systemMetricsWindowMu     sync.RWMutex
	systemMetricsWindowByUser = map[string]string{}
)

const defaultSystemMetricsWindow = "30m"

var systemMetricsWindowSamples = map[string]int{
	"30m": 360,
	"1h":  720,
	"2h":  1440,
}

func systemMetricsWindowFor(username string) string {
	systemMetricsWindowMu.RLock()
	defer systemMetricsWindowMu.RUnlock()
	if w, ok := systemMetricsWindowByUser[username]; ok {
		return w
	}
	return defaultSystemMetricsWindow
}

func setSystemMetricsWindow(username, window string) {
	systemMetricsWindowMu.Lock()
	defer systemMetricsWindowMu.Unlock()
	systemMetricsWindowByUser[username] = window
}

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

	sdui.RegisterCatalog(systemMetricsScreenID, sdui.GroupSystem, "Metrics (CPU, memory, disk)", alwaysVisible)
	sdui.RegisterCatalog(systemProcessesScreenID, sdui.GroupSystem, "Processes", alwaysVisible)
	sdui.RegisterCatalog(systemPortsScreenID, sdui.GroupSystem, "Listening ports", alwaysVisible)
	sdui.RegisterCatalog(systemSystemdScreenID, sdui.GroupSystem, "Services (systemd)", alwaysVisible)
	sdui.RegisterCatalog(systemHistoryScreenID, sdui.GroupSystem, "System history", alwaysVisible)

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

func registerSystemHistoryRows(api huma.API, deps SystemDeps, mbDeps mobilebff.Deps) {
	registerSystemRows(api, "getSystemHistoryRows", "/system/history", "Rows of system.history", mbDeps.Cfg,
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
	registerSystemRows(api, "getSystemProcessesRows", "/system/processes", "Rows of system.processes", mbDeps.Cfg,
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
	registerSystemRows(api, "getSystemPortsRows", "/system/ports", "Rows of system.ports", mbDeps.Cfg,
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
	registerSystemRows(api, "getSystemUnitsRows", "/system/units", "Rows of system.systemd", mbDeps.Cfg,
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

func systemHistoryRow(p metrics.Point) map[string]any {
	return map[string]any{
		"ts":    formatSystemTimestamp(p.T),
		"cpu":   formatSystemPercent(p.CPU),
		"mem":   formatSystemPercent(p.MemPct),
		"load1": formatSystemLoad(p.Load1),
		"disk":  formatSystemPercent(p.DiskPct),
	}
}

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

func systemMetricPointRow(p metrics.Point, extract func(metrics.Point) float64) map[string]any {
	return map[string]any{
		"ts":    formatSystemTimestamp(p.T),
		"value": formatSystemPercent(extract(p)),
	}
}

func formatSystemTimestamp(epoch int64) string {
	if epoch == 0 {
		return ""
	}
	return time.Unix(epoch, 0).UTC().Format(systemTimestampFormat)
}

func formatSystemPercent(v float64) string {
	return strconv.FormatFloat(v, 'f', 1, 64)
}

func formatSystemLoad(v float64) string {
	return strconv.FormatFloat(v, 'f', 2, 64)
}
