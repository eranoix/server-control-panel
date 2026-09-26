// Package sdui_test wires ai.settings/jira.issues/deploy.apps/queue.jobs
// into the sdui package's own golden-fixture test binary — same rationale as
// alerts_golden_test.go's header comment: golden_test.go's harness only sees
// screens registered inside the SAME test binary process, and being an
// external test package (sdui_test) is what lets this file import
// internal/mobilebff/screens without an import cycle.
package sdui_test

import (
	"context"

	"server-control-panel/internal/config"
	"server-control-panel/internal/deploy"
	"server-control-panel/internal/jira"
	"server-control-panel/internal/mobilebff/screens"
	"server-control-panel/internal/queue"
)

// miscGoldenBackend is a small in-memory stand-in for MiscDeps — enough to
// build the four screens' envelopes deterministically for the golden corpus.
// The harness only calls Build (via the sdui.Screen builder), never
// RunAction, so the mutating closures below are unreachable from the
// harness and exist only to satisfy MiscDeps' shape. JiraStatus reports
// "connected" for EVERY user identically on purpose (see this file's init
// comment): jira.issues gates on a per-user credential, not on IsAdmin, so
// the golden-admin and golden-user envelopes are expected to be byte
// identical — that identity is asserted in golden_test.go's
// screensWithNoRoleDifference, not hidden here.
type miscGoldenBackend struct{}

func (miscGoldenBackend) deps() screens.MiscDeps {
	return screens.MiscDeps{
		AIModelsConfig: func() (config.AIModels, map[string]string) {
			return config.AIModels{Suggest: "haiku", JiraAI: "sonnet"}, map[string]string{
				"suggest": "haiku",
				"jira_ai": "sonnet",
			}
		},
		SaveAIModels: func(config.AIModels) error { return nil },

		JiraStatus:     func(string) (bool, string) { return true, "PROJ" },
		JiraConnect:    func(string, string, string, string, string) error { return nil },
		JiraListIssues: func(context.Context, string, string) ([]jira.Issue, error) { return nil, nil },
		JiraGetIssue: func(context.Context, string, string) (*jira.IssueDetail, error) {
			return nil, nil
		},
		JiraTransitions: func(context.Context, string, string) ([]jira.Transition, error) {
			return nil, nil
		},
		JiraTransition: func(context.Context, string, string, string) error { return nil },
		JiraAddComment: func(context.Context, string, string, string) (*jira.Comment, error) {
			return nil, nil
		},

		ListDeployApps:   func() ([]deploy.App, error) { return nil, nil },
		GetDeployApp:     func(string) (deploy.App, bool, error) { return deploy.App{}, false, nil },
		CreateDeployApp:  func(a deploy.App) (deploy.App, error) { return a, nil },
		TriggerRedeploy:  func(string, string) (string, error) { return "", nil },
		DestroyDeployApp: func(context.Context, string) error { return nil },

		ListQueueJobs:      func(string) []*queue.Job { return nil },
		GetQueueJob:        func(string) (*queue.Job, error) { return nil, nil },
		CancelQueueJob:     func(string) error { return nil },
		RerunQueueJob:      func(string) (*queue.Job, error) { return nil, nil },
		AuthorizedForRerun: func(string, bool, string) bool { return false },

		AuditEvent: func(string, string, string) {},
	}
}

// init registers the four screens into this test binary's process-global
// sdui registries exactly once — the same screens.RegisterMisc(deps)
// internal/api/api.go calls in production, fed synthetic data instead of
// the real internal/deploy/internal/queue/internal/jira backends.
func init() {
	screens.RegisterMisc(miscGoldenBackend{}.deps())
}
