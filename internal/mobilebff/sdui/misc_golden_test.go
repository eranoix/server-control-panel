package sdui_test

import (
	"context"

	"server-control-panel/internal/config"
	"server-control-panel/internal/deploy"
	"server-control-panel/internal/jira"
	"server-control-panel/internal/mobilebff/screens"
	"server-control-panel/internal/queue"
)

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

func init() {
	screens.RegisterMisc(miscGoldenBackend{}.deps())
}
