package screens

import (
	"context"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"

	"server-control-panel/internal/adguard"
	"server-control-panel/internal/config"
	"server-control-panel/internal/deploy"
	docksvc "server-control-panel/internal/docker"
	"server-control-panel/internal/jira"
	"server-control-panel/internal/metrics"
	"server-control-panel/internal/mobilebff/sdui"
	"server-control-panel/internal/procs"
	"server-control-panel/internal/queue"
	"server-control-panel/internal/scheduler"
	"server-control-panel/internal/sysextra"
)

type KindOption struct {
	Value string
	Label string
}

type SchedulerDeps struct {
	ListJobs        func(owner string) []*scheduler.Job
	GetJob          func(id string) (*scheduler.Job, error)
	SaveJob         func(scheduler.Job) (*scheduler.Job, error)
	DeleteJob       func(id string) error
	RunNow          func(id string) (string, error)
	NextFires       func(expr string, n int) ([]time.Time, error)
	AuthorizedKinds func(user string, isAdmin bool) []KindOption
	AuditEvent      func(user, action, target string)
}

type DockerDeps struct {
	ListContainers   func(ctx context.Context) ([]types.Container, error)
	StartContainer   func(ctx context.Context, id string) error
	StopContainer    func(ctx context.Context, id string) error
	RestartContainer func(ctx context.Context, id string) error
	RemoveContainer  func(ctx context.Context, id string, force bool) error

	ListImages  func(ctx context.Context) ([]image.Summary, error)
	RemoveImage func(ctx context.Context, id string, force bool) error

	ListVolumes  func(ctx context.Context) (volume.ListResponse, error)
	ListNetworks func(ctx context.Context) ([]network.Summary, error)

	ListComposeStacks func(ctx context.Context) ([]docksvc.ComposeProject, error)
	ComposeUp         func(ctx context.Context, stack string) (string, error)
	ComposeDown       func(ctx context.Context, stack string) (string, error)

	Prune func(ctx context.Context, kinds []string) (map[string]any, error)

	AuditEvent func(user, action, target string)
}

type SystemDeps struct {
	ListHistory func() []metrics.Point

	ListProcesses func(ctx context.Context) ([]procs.Info, error)
	KillProcess   func(ctx context.Context, pid int32) error

	ListPorts func() ([]sysextra.Port, error)

	ListUnits  func() ([]sysextra.Unit, error)
	UnitAction func(ctx context.Context, unit, action string) (string, error)

	AuditEvent func(user, action, target string)
}

type UserRow struct {
	Username  string
	IsPrimary bool
	IsAdmin   bool
	HasTOTP   bool
	Sessions  int
}

type UserInput struct {
	Username string
	Password string
	Admin    bool
}

type SecretKeyRow struct {
	Key string
}

type SessionRow struct {
	ID        string
	User      string
	IP        string
	UserAgent string
	IssuedAt  int64
	LastSeen  int64
	ExpiresAt int64
	IsCurrent bool
}

type AuditFilter struct {
	Limit int
}

type AuditRow struct {
	Time   int64
	User   string
	Action string
	Target string
	IP     string
}

type DeviceRow struct {
	Name      string
	UUID      string
	Exit      string
	Datasaver bool
	Created   int64
}

type UsageRow struct {
	Name        string
	Port        int
	TotalBytes  int64
	RateBps     float64
	ActiveConns int
}

type SecurityDeps struct {
	ListUsers       func() []UserRow
	SaveUser        func(UserInput) (*UserRow, error)
	DeleteUser      func(username string) error
	ResetPassword   func(username, password string) error
	ListSecretKeys  func() []SecretKeyRow
	SetSecret       func(key, value string) error
	DeleteSecret    func(key string) error
	ListSessions    func() []SessionRow
	RevokeSession   func(sessionID string) error
	ListAuditEvents func(filter AuditFilter) ([]AuditRow, error)
	AuditEvent      func(user, action, target string)
}

type NetworkDeps struct {
	UFWStatus             func() (enabled bool, output string, err error)
	UFWApplyRule          func(action, spec string) (output string, err error)
	AdGuardStatus         func(ctx context.Context) (*adguard.Status, error)
	AdGuardSetProtection  func(ctx context.Context, enabled bool, durationMs int) error
	ListDevices           func() ([]DeviceRow, error)
	AddDevice             func(ctx context.Context, name string) (DeviceRow, error)
	RemoveDevice          func(ctx context.Context, uuid string) error
	SetDeviceExit         func(ctx context.Context, uuid, exit string) error
	SetDeviceDatasaver    func(ctx context.Context, uuid string, on bool) error
	ProbeDatasaverHealthy func(ctx context.Context, exit string) (string, error)
	RenameDevice          func(ctx context.Context, uuid, newName string) error
	DeviceLink            func(uuid string) (string, error)
	UsageSnapshot         func() ([]UsageRow, error)
	AuditEvent            func(user, action, target string)
}

type MiscDeps struct {
	AIModelsConfig func() (cur config.AIModels, effective map[string]string)
	SaveAIModels   func(config.AIModels) error

	JiraStatus      func(user string) (connected bool, project string)
	JiraConnect     func(user string, site, email, token, project string) error
	JiraListIssues  func(ctx context.Context, user, jql string) ([]jira.Issue, error)
	JiraGetIssue    func(ctx context.Context, user, key string) (*jira.IssueDetail, error)
	JiraTransitions func(ctx context.Context, user, key string) ([]jira.Transition, error)
	JiraTransition  func(ctx context.Context, user, key, transitionID string) error
	JiraAddComment  func(ctx context.Context, user, key, text string) (*jira.Comment, error)

	ListDeployApps   func() ([]deploy.App, error)
	GetDeployApp     func(name string) (deploy.App, bool, error)
	CreateDeployApp  func(deploy.App) (deploy.App, error)
	TriggerRedeploy  func(user, name string) (jobID string, err error)
	DestroyDeployApp func(ctx context.Context, name string) error

	ListQueueJobs      func(owner string) []*queue.Job
	GetQueueJob        func(id string) (*queue.Job, error)
	CancelQueueJob     func(id string) error
	RerunQueueJob      func(id string) (*queue.Job, error)
	AuthorizedForRerun func(user string, isAdmin bool, kind string) bool

	AuditEvent func(user, action, target string)
}

type AlertRuleRow struct {
	ID          string
	Name        string
	Enabled     bool
	TypePrefix  string
	MinSeverity string
	Channels    []string
}

type AlertRuleInput struct {
	ID          string
	Name        string
	Enabled     bool
	TypePrefix  string
	MinSeverity string
	Channels    []string
}

type ChannelOption struct {
	Value string
	Label string
}

type EventOption struct {
	Value string
	Label string
}

type AlertsDeps struct {
	ListAlertRules  func(v sdui.Viewer) []AlertRuleRow
	ChannelOptions  func() []ChannelOption
	EventOptions    func() []EventOption
	SaveAlertRule   func(AlertRuleInput) (*AlertRuleRow, error)
	DeleteAlertRule func(id string) error
	AuditEvent      func(user, action, target string)
}
