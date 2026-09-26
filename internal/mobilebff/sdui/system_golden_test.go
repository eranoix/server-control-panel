// Package sdui_test wires the five System screens into the sdui package's
// own golden-fixture test binary — same rationale as docker_golden_test.go's
// header comment: golden_test.go's harness only sees screens registered
// inside the SAME test binary process, and being an external test package
// (sdui_test) is what lets this file import internal/mobilebff/screens
// without an import cycle.
package sdui_test

import (
	"context"

	"server-control-panel/internal/metrics"
	"server-control-panel/internal/mobilebff/screens"
	"server-control-panel/internal/procs"
	"server-control-panel/internal/sysextra"
)

// systemGoldenBackend is a small in-memory stand-in for internal/api.Router's
// *metrics.Ring, internal/procs and internal/sysextra — enough to build all
// five System screens and their rows deterministically for the golden
// corpus. The golden harness only calls Build (via the sdui.Screen builder),
// never RunAction, so KillProcess/UnitAction below are unreachable from the
// harness and exist only to satisfy SystemDeps' shape.
type systemGoldenBackend struct{}

func (systemGoldenBackend) deps() screens.SystemDeps {
	return screens.SystemDeps{
		ListHistory: func() []metrics.Point {
			return []metrics.Point{
				{T: 1798000000, CPU: 12.3, MemUsed: 1073741824, MemPct: 45.6, Load1: 0.75, DiskPct: 30.1},
				{T: 1798000005, CPU: 13.1, MemUsed: 1077936128, MemPct: 45.9, Load1: 0.80, DiskPct: 30.1},
			}
		},
		ListProcesses: func(context.Context) ([]procs.Info, error) {
			return []procs.Info{
				{PID: 4242, Name: "nginx", User: "www-data", CPU: 0.5, Memory: 1.2, Status: "sleeping"},
			}, nil
		},
		KillProcess: func(context.Context, int32) error { return nil },

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
		UnitAction: func(context.Context, string, string) (string, error) { return "ok", nil },

		AuditEvent: func(_, _, _ string) {},
	}
}

// init registers all five System screens into this test binary's
// process-global sdui registries exactly once — the same
// RegisterSystem(deps) internal/api/api.go calls in production, fed
// synthetic data instead of the real ring/procs/sysextra. This is what
// makes RegisteredScreens() (used by TestGoldenScreens and its two
// role-omission checks) see system.history, system.processes, system.ports,
// system.systemd and system.metrics at all when running
// `go test ./internal/mobilebff/sdui/...`.
func init() {
	screens.RegisterSystem(systemGoldenBackend{}.deps())
}
