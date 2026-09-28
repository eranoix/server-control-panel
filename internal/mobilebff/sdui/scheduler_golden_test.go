package sdui_test

import (
	"time"

	"server-control-panel/internal/mobilebff/screens"
	"server-control-panel/internal/scheduler"
)

type schedulerGoldenBackend struct {
	jobs map[string]*scheduler.Job
}

func newSchedulerGoldenBackend() *schedulerGoldenBackend {
	return &schedulerGoldenBackend{
		jobs: map[string]*scheduler.Job{
			"job-root-reboot": {
				ID: "job-root-reboot", Name: "Weekly reboot", Schedule: "0 4 * * 0",
				Kind: "system_reboot", Owner: "golden-admin", RunAsRoot: true, Enabled: true,
				LastStatus: "ok", LastFire: 1798761600, NextFire: 1799366400, Created: 1798000000,
			},
			"job-user-prune": {
				ID: "job-user-prune", Name: "Docker cleanup", Schedule: "*/30 * * * *",
				Kind: "docker_prune", Owner: "golden-user", Enabled: true,
				LastStatus: "ok", LastFire: 1798761600, NextFire: 1798763400, Created: 1798000000,
			},
			"job-other-prune": {
				ID: "job-other-prune", Name: "Another user's cleanup", Schedule: "0 * * * *",
				Kind: "docker_prune", Owner: "someone-else", Enabled: true,
				LastStatus: "failed", LastFire: 1798758000, NextFire: 1798765200, Created: 1798000000,
			},
		},
	}
}

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
			kinds := []screens.KindOption{{Value: "docker_prune", Label: "Prune Docker"}}
			if isAdmin {
				kinds = append(kinds, screens.KindOption{Value: "system_reboot", Label: "Reboot system"})
			}
			return kinds
		},
		AuditEvent: func(_, _, _ string) {},
	}
}

func init() {
	screens.Register(newSchedulerGoldenBackend().deps())
}
