package sdui_test

import (
	"context"

	"server-control-panel/internal/metrics"
	"server-control-panel/internal/mobilebff/screens"
	"server-control-panel/internal/procs"
	"server-control-panel/internal/sysextra"
)

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

func init() {
	screens.RegisterSystem(systemGoldenBackend{}.deps())
}
