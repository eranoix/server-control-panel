// Package sdui_test wires the scheduler.jobs screen into the sdui package's
// own golden-fixture test binary. It lives here, and not in
// internal/mobilebff/screens, because golden_test.go's harness
// (TestGoldenScreens, TestGoldenScreens_RoleOmissionIsReasoned,
// TestGoldenScreens_NonAdminNeverContainsForbiddenStrings — all package
// sdui, internal) only ever sees screens registered inside the SAME test
// binary process, and Go links every _test.go file in a directory into one
// binary regardless of package (sdui vs sdui_test). Being an EXTERNAL test
// package (sdui_test) is what lets this file import
// internal/mobilebff/screens without creating an import cycle (screens
// already imports sdui; a non-test file in package sdui could never import
// screens back).
package sdui_test

import (
	"time"

	"server-control-panel/internal/mobilebff/screens"
	"server-control-panel/internal/scheduler"
)

// schedulerGoldenBackend is a small in-memory stand-in for
// internal/api.Router's *scheduler.Scheduler + queue runner registry —
// enough to build the screen and its rows deterministically for the golden
// corpus. It never runs a real cron or queue; the golden harness only calls
// Build (via the sdui.Screen builder), never RunAction.
type schedulerGoldenBackend struct {
	jobs map[string]*scheduler.Job
}

// newSchedulerGoldenBackend seeds a fixed, deliberately non-trivial data set
// so the admin and non-admin goldens actually differ (RBAC-by-omission is
// only provable if there is something to omit):
//   - a job owned by golden-admin with run_as_root=true and an admin-only
//     kind (system_reboot) — proves both run_as_root AND the admin-only kind
//     are omitted from the non-admin row data;
//   - a job owned by golden-user (the non-admin viewer) with an open kind
//     (docker_prune) — visible to golden-user, and to golden-admin too since
//     admin sees every job;
//   - a job owned by a THIRD user (someone-else) — proves ListJobs("") vs
//     ListJobs(owner) ownership filtering the same way handlers_scheduler.go
//     filters for the panel.
func newSchedulerGoldenBackend() *schedulerGoldenBackend {
	return &schedulerGoldenBackend{
		jobs: map[string]*scheduler.Job{
			"job-root-reboot": {
				ID: "job-root-reboot", Name: "Reinício semanal", Schedule: "0 4 * * 0",
				Kind: "system_reboot", Owner: "golden-admin", RunAsRoot: true, Enabled: true,
				LastStatus: "ok", LastFire: 1798761600, NextFire: 1799366400, Created: 1798000000,
			},
			"job-user-prune": {
				ID: "job-user-prune", Name: "Limpeza Docker", Schedule: "*/30 * * * *",
				Kind: "docker_prune", Owner: "golden-user", Enabled: true,
				LastStatus: "ok", LastFire: 1798761600, NextFire: 1798763400, Created: 1798000000,
			},
			"job-other-prune": {
				ID: "job-other-prune", Name: "Limpeza de outro usuário", Schedule: "0 * * * *",
				Kind: "docker_prune", Owner: "someone-else", Enabled: true,
				LastStatus: "failed", LastFire: 1798758000, NextFire: 1798765200, Created: 1798000000,
			},
		},
	}
}

// deps adapts the backend into a SchedulerDeps exactly as
// internal/api/api.go's real wiring does, minus persistence: ListJobs
// filters by owner (empty = every job, mirroring handlers_scheduler.go),
// AuthorizedKinds returns docker_prune for everyone and adds system_reboot
// only for admin (the same shape testSchedulerDeps/testSchedulerBackend use
// in scheduler_test.go/scheduler_actions_test.go, kept consistent here so
// the golden fixtures and the unit tests agree on what "admin-only" means).
func (b *schedulerGoldenBackend) deps() screens.SchedulerDeps {
	return screens.SchedulerDeps{
		ListJobs: func(owner string) []*scheduler.Job {
			out := make([]*scheduler.Job, 0, len(b.jobs))
			for _, j := range b.jobs {
				if owner == "" || j.Owner == owner {
					cp := *j
					out = append(out, &cp)
				}
			}
			return out
		},
		GetJob: func(id string) (*scheduler.Job, error) {
			j, ok := b.jobs[id]
			if !ok {
				return nil, scheduler.ErrNotFound
			}
			cp := *j
			return &cp, nil
		},
		SaveJob: func(in scheduler.Job) (*scheduler.Job, error) { return &in, nil },
		DeleteJob: func(id string) error {
			if _, ok := b.jobs[id]; !ok {
				return scheduler.ErrNotFound
			}
			return nil
		},
		RunNow:    func(id string) (string, error) { return "queue-id", nil },
		NextFires: func(expr string, n int) ([]time.Time, error) { return nil, nil },
		AuthorizedKinds: func(_ string, isAdmin bool) []screens.KindOption {
			kinds := []screens.KindOption{{Value: "docker_prune", Label: "Limpar Docker"}}
			if isAdmin {
				kinds = append(kinds, screens.KindOption{Value: "system_reboot", Label: "Reiniciar sistema"})
			}
			return kinds
		},
		AuditEvent: func(_, _, _ string) {},
	}
}

// init registers scheduler.jobs into this test binary's process-global sdui
// registries exactly once — the same Register(deps) internal/api/api.go
// calls in production, just fed synthetic data instead of a real
// *scheduler.Scheduler. This is what makes RegisteredScreens() (used by
// TestGoldenScreens and its two role-omission checks) see "scheduler.jobs"
// at all when running `go test ./internal/mobilebff/sdui/...`.
func init() {
	screens.Register(newSchedulerGoldenBackend().deps())
}
