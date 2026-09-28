package mobilebff

import (
	"context"
	"sort"

	"github.com/danielgtaylor/huma/v2"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/config"
	"server-control-panel/internal/jira"
	"server-control-panel/internal/metrics"
	"server-control-panel/internal/notify"
	ptysvc "server-control-panel/internal/pty"
	"server-control-panel/internal/queue"
	"server-control-panel/internal/sessions"
	"server-control-panel/internal/system"
	"server-control-panel/internal/videocall"
	"server-control-panel/internal/whatsapp"
)

type Deps struct {
	Auth           UserEmailMapper
	Cfg            *config.Config
	WhatsAppMgr    *whatsapp.Manager
	Passkey        PasskeyBackend
	SessionOwn     *ptysvc.Ownership
	Audit          *auth.AuditLog
	Queue          *queue.Queue
	Alerts         *metrics.Engine
	HealthDetailed func() (ok bool, checks map[string]string)
	Hub            *Hub
	Sessions       *sessions.Store
	Notify         *notify.Router
	SysStats       func(ctx context.Context) (*system.Stats, error)
	Videocall      *videocall.Service

	JiraFor        func(user string) (*jira.Client, error)
	JiraConfigFor  func(user string) jira.Config
	JiraConnect    func(user, site, email, token, project string) error
	JiraSetProject func(user, project string) error
	Idem           *Idempotency
}

type Registrar func(api huma.API, deps Deps)

type namedRegistrar struct {
	name string
	fn   Registrar
}

var registrars []namedRegistrar

func Register(name string, fn Registrar) {
	for _, r := range registrars {
		if r.name == name {
			panic("mobilebff: duplicate registration: " + name)
		}
	}
	registrars = append(registrars, namedRegistrar{name: name, fn: fn})
}

func runRegistrars(api huma.API, deps Deps) {
	sorted := make([]namedRegistrar, len(registrars))
	copy(sorted, registrars)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].name < sorted[j].name })
	for _, r := range sorted {
		r.fn(api, deps)
	}
}
