package screens

import (
	"context"
	"encoding/json"
	"strconv"

	"server-control-panel/internal/mobilebff/sdui"
)

// Action ids the five System screens reference from their tables' row
// actions and system.metrics' form submit_action.
const (
	systemActionProcessKill = "system.process.kill"

	systemActionUnitStart   = "system.unit.start"
	systemActionUnitStop    = "system.unit.stop"
	systemActionUnitRestart = "system.unit.restart"
	systemActionUnitEnable  = "system.unit.enable"
	systemActionUnitDisable = "system.unit.disable"

	systemActionMetricsWindow = "system.metrics.window"
)

// systemAdminViewer is the RegisterAction authorize gate for kill and every
// systemd unit action — all six mirror the web panel's mustPrimary gate
// (handlers_procs.go's PRIMARY branch, handlers_system.go's handleUnitAction)
// exactly: admin-only, even for a plain restart. A non-admin invocation gets
// ErrActionNotFound at RunAction's step 2, before the handler ever runs.
func systemAdminViewer(v sdui.Viewer) bool { return v.IsAdmin() }

// registerSystemActions registers the six System mutations: kill (admin,
// destructive) plus the five unit verbs (admin, non-destructive) and
// system.metrics.window (authenticated, non-destructive — open to every
// viewer, same posture as the read side of system.metrics). Called once by
// RegisterSystem (system.go).
func registerSystemActions(deps SystemDeps) {
	sdui.RegisterAction(
		sdui.ActionDescriptor{
			ActionID:    systemActionProcessKill,
			Method:      "POST",
			Endpoint:    "/api/mobile/v1/actions/" + systemActionProcessKill,
			Permission:  "admin",
			Destructive: true,
			// RequireTypedConfirmation deliberately empty: Destructive:true
			// plus the safety floor built into procs.Signal (IsDenied) are
			// already proportionate — the same reasoning as
			// docker.container.remove, not docker.prune.run.
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

// handleSystemProcessKill implements system.process.kill. params["id"] is
// the row's pid, rendered as a string by systemProcessRow. deps.KillProcess
// delegates to procs.Signal (never SignalAsOwner — see deps.go's doc
// comment on KillProcess for why the web panel's ownership-based self-kill
// is deliberately not reproduced here), whose own IsDenied check is the
// real safety floor (pid<=1, self, ppid, sshd/systemd/init/kthreadd/
// ksoftirqd) — this handler adds no floor of its own, it only parses the id
// and delegates.
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

// handleSystemUnitAction implements the shared shape of the five unit verbs:
// call deps.UnitAction (which mirrors handleUnitAction's
// execCmd("systemctl", action, name) exactly), audit, then invalidate the
// systemd table so the client re-fetches current unit state. There is no
// per-row Patch here (unlike Docker's container lifecycle) because
// sysextra.Unit carries no per-field id keyed the same way a container's
// re-list does — a full table refetch is the correct, simplest instruction.
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

// systemMetricsWindowInput is the body system.metrics.window decodes from
// ActionHandler's input — one field, the window's form (metrics-window-form
// in system.go).
type systemMetricsWindowInput struct {
	Window string `json:"window"`
}

// handleSystemMetricsWindow implements system.metrics.window — see
// system.go's doc comment on systemMetricsWindowMu for the full rationale of
// why this writes into package-level per-viewer state instead of returning
// a Patch/resampled series directly. Validates window against the same
// allowed set systemMetricsWindowSamples defines, writes the caller's
// preference, audits, then invalidates all three charts so ScreenState's
// action-runner refetches their (still fixed) URLs and sees the new window.
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
