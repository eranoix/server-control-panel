package screens

import (
	"context"
	"encoding/json"
	"strconv"

	"server-control-panel/internal/mobilebff/sdui"
)

const (
	systemActionProcessKill = "system.process.kill"

	systemActionUnitStart   = "system.unit.start"
	systemActionUnitStop    = "system.unit.stop"
	systemActionUnitRestart = "system.unit.restart"
	systemActionUnitEnable  = "system.unit.enable"
	systemActionUnitDisable = "system.unit.disable"

	systemActionMetricsWindow = "system.metrics.window"
)

func systemAdminViewer(v sdui.Viewer) bool { return v.IsAdmin() }

func registerSystemActions(deps SystemDeps) {
	sdui.RegisterAction(
		sdui.ActionDescriptor{
			ActionID:    systemActionProcessKill,
			Method:      "POST",
			Endpoint:    "/api/mobile/v1/actions/" + systemActionProcessKill,
			Permission:  "admin",
			Destructive: true,
		},
		systemAdminViewer,
		handleSystemProcessKill(deps),
	)

	sdui.RegisterAction(
		sdui.ActionDescriptor{
			ActionID:   systemActionUnitStart,
			Method:     "POST",
			Endpoint:   "/api/mobile/v1/actions/" + systemActionUnitStart,
			Permission: "admin",
		},
		systemAdminViewer,
		handleSystemUnitAction(deps, "start"),
	)
	sdui.RegisterAction(
		sdui.ActionDescriptor{
			ActionID:   systemActionUnitStop,
			Method:     "POST",
			Endpoint:   "/api/mobile/v1/actions/" + systemActionUnitStop,
			Permission: "admin",
		},
		systemAdminViewer,
		handleSystemUnitAction(deps, "stop"),
	)
	sdui.RegisterAction(
		sdui.ActionDescriptor{
			ActionID:   systemActionUnitRestart,
			Method:     "POST",
			Endpoint:   "/api/mobile/v1/actions/" + systemActionUnitRestart,
			Permission: "admin",
		},
		systemAdminViewer,
		handleSystemUnitAction(deps, "restart"),
	)
	sdui.RegisterAction(
		sdui.ActionDescriptor{
			ActionID:   systemActionUnitEnable,
			Method:     "POST",
			Endpoint:   "/api/mobile/v1/actions/" + systemActionUnitEnable,
			Permission: "admin",
		},
		systemAdminViewer,
		handleSystemUnitAction(deps, "enable"),
	)
	sdui.RegisterAction(
		sdui.ActionDescriptor{
			ActionID:   systemActionUnitDisable,
			Method:     "POST",
			Endpoint:   "/api/mobile/v1/actions/" + systemActionUnitDisable,
			Permission: "admin",
		},
		systemAdminViewer,
		handleSystemUnitAction(deps, "disable"),
	)

	sdui.RegisterAction(
		sdui.ActionDescriptor{
			ActionID:   systemActionMetricsWindow,
			Method:     "POST",
			Endpoint:   "/api/mobile/v1/actions/" + systemActionMetricsWindow,
			Permission: "authenticated",
		},
		authenticatedViewer,
		handleSystemMetricsWindow(deps),
	)
}

func handleSystemProcessKill(deps SystemDeps) sdui.ActionHandler {
	return func(ctx context.Context, v sdui.Viewer, params map[string]string, _ json.RawMessage) (sdui.ActionResult, error) {
		raw := params["id"]
		pid, err := strconv.Atoi(raw)
		if raw == "" || err != nil {
			return sdui.ActionResult{}, sdui.ErrActionNotFound
		}
		if err := deps.KillProcess(ctx, int32(pid)); err != nil {
			return sdui.ActionResult{}, err
		}
		if deps.AuditEvent != nil {
			deps.AuditEvent(v.Username, "system.process.kill", raw)
		}
		return sdui.ActionResult{Invalidate: []string{"processes-table"}}, nil
	}
}

func handleSystemUnitAction(deps SystemDeps, action string) sdui.ActionHandler {
	return func(ctx context.Context, v sdui.Viewer, params map[string]string, _ json.RawMessage) (sdui.ActionResult, error) {
		unit := params["id"]
		if unit == "" {
			return sdui.ActionResult{}, sdui.ErrActionNotFound
		}
		if _, err := deps.UnitAction(ctx, unit, action); err != nil {
			return sdui.ActionResult{}, err
		}
		if deps.AuditEvent != nil {
			deps.AuditEvent(v.Username, "system.unit."+action, unit)
		}
		return sdui.ActionResult{Invalidate: []string{"systemd-table"}}, nil
	}
}

type systemMetricsWindowInput struct {
	Window string `json:"window"`
}

func handleSystemMetricsWindow(deps SystemDeps) sdui.ActionHandler {
	return func(_ context.Context, v sdui.Viewer, _ map[string]string, input json.RawMessage) (sdui.ActionResult, error) {
		var in systemMetricsWindowInput
		if len(input) > 0 {
			if err := json.Unmarshal(input, &in); err != nil {
				return sdui.ActionResult{}, sdui.FieldErrors{}.Add("window", "invalid request body")
			}
		}
		if _, ok := systemMetricsWindowSamples[in.Window]; !ok {
			return sdui.ActionResult{}, sdui.FieldErrors{}.Add("window", "invalid window — use 30m, 1h or 2h")
		}

		setSystemMetricsWindow(v.Username, in.Window)
		if deps.AuditEvent != nil {
			deps.AuditEvent(v.Username, "system.metrics.window", in.Window)
		}
		return sdui.ActionResult{Invalidate: []string{"cpu-chart", "mem-chart", "disk-chart"}}, nil
	}
}
