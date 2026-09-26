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

// Deps carries the domain dependencies the BFF handlers need.
//
// Why a struct instead of positional parameters on Mount: the BFF will take on
// ~30 endpoints written by different workstreams, and each new endpoint would
// add one more parameter to Mount — a signature everyone has to edit at the
// same time. Here each workstream adds ONE field, and nothing else.
//
// Fields may be nil: cmd/mobile-openapi-gen builds the BFF only to extract the
// shape of the types and generate the spec, never invoking a real handler.
// Every registrar must tolerate a missing dependency without panicking at
// registration time.
type Deps struct {
	// Auth maps user -> e-mail. See handlers_session.go.
	Auth UserEmailMapper
	// Cfg is the panel's configuration — needed to resolve sdui.ViewerFrom (the
	// admin check every SDUI screen uses). See handlers_screens.go.
	Cfg *config.Config
	// WhatsAppMgr resolves the *whatsapp.Service per authenticated user (the same
	// multi-tenant Manager the web panel already uses). See handlers_whatsapp.go.
	WhatsAppMgr *whatsapp.Manager
	// Passkey implements the WebAuthn ceremonies (registration/login) — used by
	// the PUBLIC registrars (RegisterPublic, see registry_public.go and
	// auth_passkey.go), never by the authenticated ones. Implemented by
	// internal/api.Router.
	Passkey PasskeyBackend
	// SessionOwn is the per-user ownership registry of terminal sessions
	// (internal/pty.Ownership) — the same *ptysvc.Ownership internal/api.Router
	// carries in r.sessionOwn. Needed so SessionListForUser/OwnsSession can
	// filter by owner; see handlers_terminal.go. nil degrades the way the web
	// panel already degrades (an admin sees ownerless sessions, everyone else
	// sees nothing).
	SessionOwn *ptysvc.Ownership
	// Audit is the same *auth.AuditLog internal/api.Router carries in r.audit —
	// every SDUI action that succeeds records an event here (see
	// handlers_actions.go), at the same granularity the web panel already audits
	// its own mutations with. nil turns auditing off without panicking
	// (httpx.AuditEvent already tolerates a nil audit).
	Audit *auth.AuditLog
	// Queue is the same *queue.Queue internal/api.Router carries in r.queue —
	// used to fire and query the self_deploy job (see ops_deploy.go). nil turns
	// the ops endpoints into 503s, never a panic.
	Queue *queue.Queue
	// Alerts is the same *metrics.Engine internal/api.Router carries in
	// r.alerts — the source of the currently-firing rules for GET /ops/status
	// (see ops_health.go). nil ⇒ empty alert list.
	Alerts *metrics.Engine
	// HealthDetailed reuses the very same check aggregation
	// GET /api/health/detailed already computes (internal/api/handlers_health.go)
	// — injected as a closure because internal/api imports this package, and the
	// reverse would create an import cycle. See ops_health.go. nil ⇒ empty
	// health/ok=false instead of a panic.
	HealthDetailed func() (ok bool, checks map[string]string)
	// Hub is /ws/mobile-events (events_hub.go), which events_bridge_ops.go uses
	// to publish the deploy's live log/progress (channel "deploy.<jobID>") and
	// the periodic health snapshot (channel "ops.health"). nil ⇒ the ops HTTP
	// endpoints keep working (trigger/status/aggregated status), only the "live"
	// part goes inert.
	Hub *Hub
	// Sessions is the SAME *sessions.Store the web panel uses for password/2FA
	// sessions (internal/api.Router carries it in r.auth.Sessions()) — not a
	// separate mobile refresh-token store. POST /auth/logout (auth_logout.go)
	// revokes by jti in this store, which also invalidates the token on any
	// protected route of the web panel, not just on the mobile BFF. nil ⇒ logout
	// answers 200 without revoking anything (degrading like the other optional
	// fields of this struct).
	Sessions *sessions.Store
	// Notify is the SAME *notify.Router internal/api.Router carries in r.notify —
	// used by notify_prefs.go to project the Rule catalogue
	// (GET /notify/preferences) and by push_devices.go to seed the default
	// (critical-only) preference the first time a device_id registers. nil ⇒ the
	// preference endpoints answer 503 instead of panicking, the same pattern as
	// Queue/Alerts/HealthDetailed.
	Notify *notify.Router
	// SysStats returns the SAME resource snapshot the web panel's GET /api/stats
	// serves — internal/api wires this to its collectStatsCached
	// (handlers_system.go), NOT to raw system.Collect: the collection blocks
	// ~200ms sampling CPU and sweeps all of /proc, and the panel's wrapper
	// already solves that with a 3s TTL + singleflight under a mutex. Wiring the
	// cached function here makes app and panel SHARE one collection instead of
	// paying for two. Injected as a closure for the same reason as
	// HealthDetailed (internal/api imports this package; the reverse would be a
	// cycle). See ops_metrics.go. nil ⇒ OpsStatus's `system` field is absent,
	// never zeroed and never a panic.
	SysStats func(ctx context.Context) (*system.Stats, error)
	// Videocall is the SAME *videocall.Service internal/api.Router carries in
	// r.videocall — used by handlers_videocall.go to list the authenticated
	// user's rooms (GET /videocall/rooms), reusing ListForUser without
	// re-deriving the shape the web panel already exposes at
	// /api/videocall/rooms. nil ⇒ the route answers an empty list instead of
	// panicking, the same pattern as the other optional fields of this struct.
	Videocall *videocall.Service

	// --- Jira (handlers_jira.go) ---------------------------------------
	//
	// Four closures, and not one per method of the Jira client: the board uses
	// twelve different operations (search, transition, comment, assign, create,
	// list projects, list people…) and one closure for each would turn into a
	// slice of internal/api rewritten here. Handing over the already-resolved
	// *jira.Client keeps the boundary where it belongs — whoever knows how to
	// find the credential in the vault is internal/api; whoever knows how to
	// build the board is this package.

	// JiraFor returns the user's Jira client, with the PER-USER credentials from
	// the vault (the same path as the web panel: Jira access is a personal
	// credential, not an admin role). It returns jira.ErrNotConfigured when the
	// account has not been linked yet — a NORMAL situation, which the board
	// turns into a connection form, never into an error. nil ⇒ the Jira routes
	// answer 503 instead of panicking, the same pattern as the other fields.
	JiraFor func(user string) (*jira.Client, error)
	// JiraConfigFor returns this user's board configuration — the default
	// project, the board's JQL and the custom columns. It never returns the
	// token: the struct has the field, and this path does not fill it in.
	JiraConfigFor func(user string) jira.Config
	// JiraConnect writes site/e-mail/token/project into the user's vault,
	// mirroring the POST branch of the web panel's handleJiraConfig.
	JiraConnect func(user, site, email, token, project string) error
	// JiraSetProject pins the default project only, without touching the
	// credential — it is what switching project in the board's picker does.
	JiraSetProject func(user, project string) error
	// Idem is the result table that keeps a RETRY from executing the action twice
	// (see idempotencia.go). It is what allows the app's outbound queue to carry
	// writes beyond WhatsApp: without a server that recognizes "I have seen
	// this", resending what timed out duplicates.
	//
	// ONE instance for the whole BFF, built in internal/api. Several of them
	// pointing at the same file would trample each other in gravar(). nil is
	// legitimate and becomes a no-op in every method — it is the case of
	// cmd/mobile-openapi-gen, which builds the registry without a dataDir.
	Idem *Idempotencia
}

// Registrar registers one cohesive group of routes (one handlers_*.go file) on
// the BFF's huma.API.
type Registrar func(api huma.API, deps Deps)

type namedRegistrar struct {
	name string
	fn   Registrar
}

var registrars []namedRegistrar

// Register signs a group of routes up with the BFF. Call it from an init() in
// the handlers_*.go file itself — that way adding an endpoint does not require
// editing mobilebff.go, the one file every workstream would contend over.
//
// `name` has to be unique and stable: registrars are sorted by it before they
// run, so that registration order — and therefore route order in the generated
// mobile-v1.yaml — does not depend on the initialization order of the package's
// files. The generated spec has to be byte-identical between runs, otherwise
// the contract-drift gate in CI turns into noise.
func Register(name string, fn Registrar) {
	for _, r := range registrars {
		if r.name == name {
			panic("mobilebff: duplicate registration: " + name)
		}
	}
	registrars = append(registrars, namedRegistrar{name: name, fn: fn})
}

// runRegistrars runs every registered registrar, in deterministic order.
func runRegistrars(api huma.API, deps Deps) {
	sorted := make([]namedRegistrar, len(registrars))
	copy(sorted, registrars)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].name < sorted[j].name })
	for _, r := range sorted {
		r.fn(api, deps)
	}
}
