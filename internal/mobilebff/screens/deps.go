// Package screens holds the SDUI screen and action definitions the mobile
// BFF serves — one file per admin section, so a future section is a new pair
// of files here, never an edit to internal/mobilebff itself.
//
// Every screen in this package ADAPTS an existing domain package; it never
// reimplements domain logic. The adaptation seam is a *Deps struct per
// section (see SchedulerDeps below): a set of thin closures that
// internal/api supplies when it wires the mobile BFF, because
// internal/mobilebff/screens importing internal/api directly would be an
// import cycle (internal/api already imports internal/mobilebff). Keeping
// each closure to a single delegating call is what keeps that boundary true — if
// a closure ever grows a rule that does not already exist on the panel side,
// the rule is in the wrong place.
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

// KindOption is one entry of the queue-kind catalogue a viewer is allowed to
// schedule — the same catalogue GET /api/scheduler/catalog already exposes
// to the web panel, reused here instead of re-derived.
type KindOption struct {
	Value string
	Label string
}

// SchedulerDeps is the injection seam for the scheduler.jobs screen and its
// actions. internal/api/api.go constructs one value of this type from its
// own *scheduler.Scheduler, queue runner registry and audit log, and passes
// it to Register below. Every field is a direct delegation to code that
// already exists in internal/api or internal/scheduler — see the doc
// comment on each field for exactly which panel code it must mirror.
type SchedulerDeps struct {
	// ListJobs mirrors handlers_scheduler.go's GET list: owner == "" means
	// every job (primary/admin), a non-empty owner means only that user's
	// jobs.
	ListJobs func(owner string) []*scheduler.Job
	// GetJob mirrors (*scheduler.Scheduler).Get.
	GetJob func(id string) (*scheduler.Job, error)
	// SaveJob mirrors (*scheduler.Scheduler).Save. scheduler_actions.go sets
	// Owner, authorizes Kind and rejects RunAsRoot from a non-admin BEFORE
	// calling this — Save itself does none of that. The real closure
	// internal/api/api.go supplies must also call the panel's own
	// injectSchedOwner (handlers_scheduler.go) before delegating to
	// Scheduler.Save, exactly as the web panel's POST/PUT handlers do, so a
	// session_backup job created from the app still gets its owner injected
	// into Args the same way.
	SaveJob func(scheduler.Job) (*scheduler.Job, error)
	// DeleteJob mirrors (*scheduler.Scheduler).Delete.
	DeleteJob func(id string) error
	// RunNow mirrors (*scheduler.Scheduler).RunNow. It does not itself
	// re-check kind authorization for manual fires (see scheduler.go's
	// fire()) — the caller must re-authorize before calling this, exactly
	// as handlers_scheduler.go's run-now branch already does.
	RunNow func(id string) (string, error)
	// NextFires mirrors (*scheduler.Scheduler).NextFires — the same cheap,
	// side-effect-free cron-expression check Save runs internally. Exposed
	// here so scheduler.job.save can attribute a bad cron expression to the
	// "schedule" field BEFORE calling SaveJob, instead of relying on
	// string-matching SaveJob's own error text: Save wraps
	// scheduler.ErrBadInput with a human sentence, not a structured field
	// name, and parsing that sentence would silently break the day its
	// wording changes.
	NextFires func(expr string, n int) ([]time.Time, error)
	// AuthorizedKinds mirrors handleSchedulerCatalog's filter: the
	// schedulable kinds this exact (user, isAdmin) pair may use, via the
	// live queue.Runner.AuthorizedFor gate — never a copy of that rule.
	AuthorizedKinds func(user string, isAdmin bool) []KindOption
	// AuditEvent records one audit entry under the panel's own action
	// vocabulary (scheduler.create/update/delete/run_now), so a mobile
	// mutation is indistinguishable in /audit from the same mutation made
	// through the web panel. Deliberately narrower than
	// httpx.AuditEvent: an sdui.ActionHandler only receives a
	// context.Context, not the *http.Request httpx.AuditEvent needs for
	// the caller's IP, so the closure internal/api supplies records the
	// event without one (see api.go) rather than plumbing the raw request
	// through sdui's action-handler signature for every screen in the
	// project.
	AuditEvent func(user, action, target string)
}

// DockerDeps is the injection seam for the six Docker screens (containers,
// images, volumes, networks, compose, prune) and their actions.
// internal/api/api.go constructs one value of this type from its own
// *docker.Client and audit log, and passes it to RegisterDocker below. Every
// field delegates straight to internal/docker or internal/api's existing
// Docker helpers (ComposeAction, ListComposeProjects) — see the doc comment
// on each field for exactly what it must mirror. There is deliberately no
// RemoveVolume/RemoveNetwork field: internal/docker exposes no single-item
// deletion for either anywhere (only VolumesPrune/NetworksPrune inside
// Prune below), and handleVolumes/handleNetworks on the web panel are
// list-only too — this seam matches that, it does not add capability the
// panel lacks.
type DockerDeps struct {
	// ListContainers mirrors handleContainers: docker.Client.ListContainers,
	// no per-viewer filtering — the web panel shows every container to every
	// authenticated user today, and this closure does the same.
	ListContainers func(ctx context.Context) ([]types.Container, error)
	// StartContainer, StopContainer, RestartContainer mirror
	// handleContainerAction's start/stop/restart branches — no admin gate on
	// the web side, so authorize on these actions is "any signed-in user",
	// exactly like handleContainerAction today.
	StartContainer   func(ctx context.Context, id string) error
	StopContainer    func(ctx context.Context, id string) error
	RestartContainer func(ctx context.Context, id string) error
	// RemoveContainer mirrors handleContainerAction's "remove" branch,
	// including the client-supplied force flag (?force=1 on the web side).
	// Unlike start/stop/restart this action is Destructive and admin-only —
	// see docker_actions.go.
	RemoveContainer func(ctx context.Context, id string, force bool) error

	// ListImages mirrors handleImages' GET branch: docker.Client.Images, no
	// per-viewer filtering.
	ListImages func(ctx context.Context) ([]image.Summary, error)
	// RemoveImage mirrors handleImages' DELETE branch, including force.
	// Destructive and admin-only — see docker_actions.go.
	RemoveImage func(ctx context.Context, id string, force bool) error

	// ListVolumes mirrors handleVolumes: docker.Client.Volumes. No removal
	// closure exists for volumes anywhere in this struct — see the type doc
	// comment above.
	ListVolumes func(ctx context.Context) (volume.ListResponse, error)
	// ListNetworks mirrors handleNetworks: docker.Client.Networks. No
	// removal closure exists for networks anywhere in this struct — see the
	// type doc comment above.
	ListNetworks func(ctx context.Context) ([]network.Summary, error)

	// ListComposeStacks mirrors handleCompose: docker.Client.ListComposeProjects.
	ListComposeStacks func(ctx context.Context) ([]docksvc.ComposeProject, error)
	// ComposeUp, ComposeDown mirror handleComposeAction's "up -d"/"down"
	// invocations, but take ONLY a stack name — never a working_dir. The
	// real closure internal/api/api.go supplies must resolve workingDir
	// itself via ListComposeStacks' own project.WorkingDir lookup before
	// calling docker.Client.ComposeAction, exactly as handleComposeAction
	// does server-side today, EXCEPT that handleComposeAction still accepts
	// a client-supplied working_dir (gated behind mustPrimary on the web
	// side only). This seam must never grow that parameter: an
	// attacker-chosen absolute path plus a crafted docker-compose.yml at
	// that path is remote code execution, and the mobile BFF
	// has no equivalent web-session-cookie-only defense to fall back on.
	// ComposeDown is Destructive and admin-only; ComposeUp is not — see
	// docker_actions.go.
	ComposeUp   func(ctx context.Context, stack string) (string, error)
	ComposeDown func(ctx context.Context, stack string) (string, error)

	// Prune mirrors handlePrune, restricted to the kinds the caller selects
	// (never PruneAll's implicit "every kind" — the prune-form screen always
	// sends an explicit, non-empty kind list, validated by
	// docker_actions.go before this is ever called). The result map uses
	// the same "<kind>"/"<kind>_error" key convention docker.Client.PruneAll
	// already uses, so a partial failure (e.g. images prune succeeds,
	// volumes prune fails) is representable without inventing a new shape.
	// Destructive and admin-only — see docker_actions.go.
	Prune func(ctx context.Context, kinds []string) (map[string]any, error)

	// AuditEvent records one audit entry under this package's own docker.*
	// action vocabulary (docker.container.remove, docker.image.remove,
	// docker.compose.down, docker.prune.run) —
	// internal/api/handlers_docker.go records none of these today, so this
	// is the first audit trail for Docker mutations from either surface,
	// not a duplicate of an existing one. Deliberately narrower than
	// httpx.AuditEvent for the same reason SchedulerDeps.AuditEvent is: an
	// sdui.ActionHandler has no *http.Request to take the caller's IP from.
	AuditEvent func(user, action, target string)
}

// SystemDeps is the injection seam for the five System screens (history,
// processes, ports, systemd, metrics) and their actions. internal/api/api.go
// constructs one value of this type from its own *metrics.Ring, audit log
// and the internal/procs / internal/sysextra packages, and passes it to
// RegisterSystem below. Every field delegates straight to code that already
// exists in internal/api/handlers_system.go, internal/api/handlers_procs.go,
// internal/procs or internal/sysextra — see the doc comment on each field.
type SystemDeps struct {
	// ListHistory mirrors handleHistory: r.ring.Snapshot(), oldest to newest.
	// Read is open to any authenticated user for every viewer — the web
	// panel applies no admin gate to GET /api/system/history, and this
	// closure matches that (system.history and the three system.metrics
	// charts all read this same closure).
	ListHistory func() []metrics.Point

	// ListProcesses mirrors handleProcs' open read: procs.List with an
	// empty Filter, sorted by CPU, no admin gate — handlers_procs.go
	// documents read as open to any authenticated user.
	ListProcesses func(ctx context.Context) ([]procs.Info, error)
	// KillProcess mirrors handlers_procs.go's handleProcsSignal PRIMARY/
	// admin branch ONLY (procs.Signal, never procs.SignalAsOwner). The web
	// panel lets a non-admin signal a pid they own via SignalAsOwner, but
	// the golden harness's binary admin/non-admin model cannot express
	// per-row, ownership-based authorization — kill is admin-only on mobile
	// as a deliberate, documented parity reduction (see system_actions.go).
	// procs.Signal's own internal IsDenied check remains the safety floor
	// (pid<=1, self, ppid, sshd/systemd/init/kthreadd/ksoftirqd) — this
	// closure adds no floor of its own, it delegates to the one that
	// already exists.
	KillProcess func(ctx context.Context, pid int32) error

	// ListPorts mirrors handleListening: sysextra.Listening(), open to any
	// authenticated user, system-wide TCP/UDP listening sockets. This is
	// deliberately NOT handlers_port.go's private listListeningPorts() (a
	// narrower, admin-only, loopback-only dev-port-forwarding feature
	// unrelated to the web panel's "Portas/Conexões" page).
	ListPorts func() ([]sysextra.Port, error)

	// ListUnits mirrors handleUnits: sysextra.ListUnits(), open read, no
	// admin gate.
	ListUnits func() ([]sysextra.Unit, error)
	// UnitAction mirrors handleUnitAction: systemctl <action> <unit>,
	// admin-only (mustPrimary) on the web panel for every action including
	// plain restart — this closure carries the same admin-only posture via
	// system_actions.go's authorize gate, never a rule invented here.
	// action is one of start/stop/restart/enable/disable, pre-validated by
	// system_actions.go against the same allowlist handleUnitAction uses.
	UnitAction func(ctx context.Context, unit, action string) (string, error)

	// AuditEvent records one audit entry under this package's own system.*
	// action vocabulary (system.process.kill, system.unit.<action>,
	// system.metrics.window). Deliberately narrower than httpx.AuditEvent
	// for the same reason SchedulerDeps.AuditEvent/DockerDeps.AuditEvent
	// are: an sdui.ActionHandler has no *http.Request to take the caller's
	// IP from.
	AuditEvent func(user, action, target string)
}

// --- Security / Network row and input types --------------------------------
//
// Unlike DockerDeps/SystemDeps (which pass native domain types like
// types.Container straight through to a row-shaping func), the four Security
// screens and the four Network screens each pull from a DIFFERENT domain
// package (internal/config, internal/secrets, internal/sessions,
// internal/auth, internal/adguard, internal/singbox, internal/netusage) with
// no shared shape — so each Deps field below returns a small Row/Input type
// defined here instead. The real closures in internal/api/api.go do the
// mapping from each domain package's native type into these; security.go's
// row-shaping functions then do the (much smaller) mapping from Row to the
// wire map[string]any, exactly like dockerContainerRow does for
// types.Container.

// UserRow is the SecurityDeps.ListUsers row shape — mirrors
// handlers_users.go's userInfo exactly (same five fields, same meaning), so
// a mobile user list carries the same information the web panel's user list
// already shows, never more.
type UserRow struct {
	Username  string
	IsPrimary bool
	IsAdmin   bool
	HasTOTP   bool
	Sessions  int
}

// UserInput is what SecurityDeps.SaveUser decodes from security.user.save's
// action body — one entry point for BOTH create and edit, the same shape
// SchedulerDeps.SaveJob uses for scheduler.job.save. The real closure
// internal/api/api.go supplies must branch on cfg.HasUser(Username): absent
// means create (validate username charset/length and password length like
// handleUserCreate, then AddUser with Password); present means edit, which
// touches ONLY the role — Password is ignored entirely on the edit path.
// Changing an existing user's password is deliberately NOT part of this
// entry point (see PLAN.md Task 1: "password-reset action separate from the
// save action since password changes deserve their own confirm step") —
// that is SecurityDeps.ResetPassword below, a distinct action with its own
// confirm step, never a field folded into this general save form. Admin is
// applied through internal/config.SetAdmin in EITHER branch, never by
// writing the User.Admin field directly — that is what makes a brand-new
// admin still go through SetAdmin's own Primary-protection rule, and what
// makes demoting an existing admin surface SetAdmin's own last-admin
// rejection as a FieldErrors instead of a hand-rolled count check (see
// security_actions.go).
type UserInput struct {
	Username string
	Password string // only read on CREATE; ignored on edit — see ResetPassword
	Admin    bool
}

// SecretKeyRow is the SecurityDeps.ListSecretKeys row shape — key name only.
// internal/secrets.Store tracks no per-key metadata (no last-set timestamp,
// no last-used timestamp): Store.List() returns a sorted []string of key
// names and nothing else, so this type carries only what actually exists
// server-side rather than inventing a shape the domain package cannot back.
// Never add a Value field here — the entire point of this screen is that a
// secret's VALUE is never readable through it (Store.Get/Export, which DO
// return values, must never be wired to any SecurityDeps field).
type SecretKeyRow struct {
	Key string
}

// SessionRow is the SecurityDeps.ListSessions row shape — one row per LIVE
// session across EVERY user, not just the caller's own (unlike
// sessions.Store.ListForUser, which is scoped to one user). The real closure
// in internal/api/api.go aggregates ListForUser over every cfg.Users entry —
// the same composition handlers_users.go's handleUsersList already performs
// to compute its own per-user session counts, not a new capability invented
// here. IsCurrent flags the row for the admin viewer's OWN current login
// (mirrors handleSessionsList's "current" field) so the client can render a
// distinct inline warning next to that row before revoking it — SDUI's
// ConfirmDestructiveComponent carries one static message per action id, not
// a per-row variant, so the row-level flag is how this screen surfaces
// "you are about to revoke your own session" without inventing an eighth
// component type.
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

// AuditFilter is what SecurityDeps.ListAuditEvents accepts. Deliberately
// minimal for v1: sdui.DataSource carries only Endpoint+Method, no
// query-template mechanism a table could bind a client-side filter field
// to, so the real closure in internal/api/api.go always calls this with a
// fixed, small Limit — never a client-supplied one. The field still exists
// as a struct (rather than a bare int parameter) so a future filter form
// can extend it without changing SecurityDeps' function signature.
type AuditFilter struct {
	Limit int
}

// AuditRow is the SecurityDeps.ListAuditEvents row shape — mirrors
// auth.Event exactly (Time/User/Action/Target/IP), sourced from the SAME
// audit.Tail/Search primitives handlers_audit.go's handleAuditTail/
// handleAuditSearch already use. Because security.audit is a whole-screen
// admin-only surface (ErrScreenNotFound for non-admin, see security.go),
// the real closure intentionally returns every event system-wide, skipping
// the per-user VisibleToUser/TailForUser filtering handleAuditTail applies
// for a non-admin caller — an admin already sees every user's audit trail
// on the web panel's own filterable /api/audit/search view, so this is not a
// new capability, just the same view surfaced on mobile.
type AuditRow struct {
	Time   int64
	User   string
	Action string
	Target string
	IP     string
}

// DeviceRow is the NetworkDeps device-table row shape — mirrors
// singbox.Device exactly (Name/UUID/Exit/Datasaver/Created).
type DeviceRow struct {
	Name      string
	UUID      string
	Exit      string
	Datasaver bool
	Created   int64
}

// UsageRow is the NetworkDeps.UsageSnapshot row shape — mirrors
// netusage.DeviceUsage exactly (Name/Port/TotalBytes/RateBps/ActiveConns).
type UsageRow struct {
	Name        string
	Port        int
	TotalBytes  int64
	RateBps     float64
	ActiveConns int
}

// SecurityDeps is the injection seam for the four Security screens (users,
// secrets, sessions, audit) and their actions. internal/api/api.go
// constructs one value of this type from its own *config.Config,
// internal/secrets.Store, internal/sessions.Store and *auth.AuditLog, and
// passes it to RegisterSecurity below. There is deliberately no
// IsLastAdmin(username string) bool field: the last-admin lockout guard
// lives INSIDE internal/config's own SetAdmin/RemoveUser (see their doc
// comments) — a count-based pre-check here would be a second, parallel
// source of truth for a rule that already exists and is already tested.
type SecurityDeps struct {
	// ListUsers mirrors handleUsersList: every user, admin flag from
	// cfg.Admins(), TOTP flag from SupabaseMFAEnabled||TOTPSecret!="", and a
	// live session count aggregated the same way handleUsersList aggregates
	// it (see SessionRow's doc comment).
	ListUsers func() []UserRow
	// SaveUser mirrors handleUserCreate (create branch) and
	// handleUserSetAdmin (edit branch, role only) combined into one entry
	// point — see UserInput's doc comment for the exact branching the real
	// closure must perform and for why password reset is NOT part of this.
	SaveUser func(UserInput) (*UserRow, error)
	// DeleteUser wraps internal/config.RemoveUser. The self-delete guard
	// (mirroring handlers_users.go's unconditional 403 "não pode deletar o
	// próprio usuário") and the Primary-target guard both live in
	// security_actions.go, BEFORE this closure is ever called — this
	// closure itself performs no guard beyond what RemoveUser's own error
	// already provides, exactly like DockerDeps.RemoveContainer performs no
	// guard beyond docker.Client's own error.
	DeleteUser func(username string) error
	// ResetPassword mirrors handleUserResetPassword exactly (config.SetPassword
	// + config.Save) — the SEPARATE action security.user.reset_password calls,
	// never security.user.save (see UserInput's doc comment). Kept as its own
	// closure rather than reusing SaveUser with a populated Password field so
	// the two actions can never be conflated at the wiring layer either.
	ResetPassword func(username, password string) error
	// ListSecretKeys mirrors internal/secrets.Store.List() — sorted key
	// names, never a value.
	ListSecretKeys func() []SecretKeyRow
	// SetSecret mirrors internal/secrets.Store.Set — create or overwrite one
	// key. The value is write-only through this seam: nothing in
	// SecurityDeps ever reads a value back out.
	SetSecret func(key, value string) error
	// DeleteSecret mirrors internal/secrets.Store.Delete.
	DeleteSecret func(key string) error
	// ListSessions aggregates sessions.Store.ListForUser across every
	// cfg.Users entry — see SessionRow's doc comment.
	ListSessions func() []SessionRow
	// RevokeSession mirrors sessions.Store.Revoke(jti) — a real tombstone
	// write that auth's Touch/HasTombstone check on every subsequent
	// authenticated request, not a cosmetic row removal. Admin-only and
	// global (any user's session, not just the caller's own), unlike
	// handleSessionRevoke's ownership check on the web panel — this screen
	// is the admin console, not the self-service session list.
	RevokeSession func(sessionID string) error
	// ListAuditEvents mirrors handleAuditTail/handleAuditSearch's underlying
	// audit.Tail/Search, but WITHOUT per-user VisibleToUser filtering — see
	// AuditRow's doc comment for why that omission is correct for an
	// admin-only screen.
	//
	// It returns an error, and not just the list, because "no events" and "the
	// audit file could not be opened" are opposite things to whoever is
	// looking: the first is the system at peace, the second is a security log
	// that stopped recording. With no error channel the two reached the screen
	// as the same empty list — and the empty-state copy had to admit the
	// ambiguity in writing. Same shape as ListDevices, just below in
	// NetworkDeps.
	ListAuditEvents func(filter AuditFilter) ([]AuditRow, error)
	// AuditEvent records one audit entry under this package's own
	// security.* action vocabulary (security.user.save/delete,
	// security.secret.set/delete, security.session.revoke), same narrower
	// shape as every other *Deps.AuditEvent in this file.
	AuditEvent func(user, action, target string)
}

// NetworkDeps is the injection seam for the four Network screens (ufw,
// adguard, devices, economia) and their actions — all four registered under
// the "security." screen id prefix by RegisterNetwork (security.go), even
// though the backing seam is a separate struct from SecurityDeps: the four
// domains (firewall, DNS filtering, VLESS/singbox device provisioning,
// per-device network usage) are four genuinely distinct subsystems with no
// shared shape, so splitting the seam mirrors the actual domain boundary —
// see PLAN.md's Blocker 3 ("rede" was never one screen, it was four).
// internal/api/api.go constructs one value of this type from
// handlers_system.go's UFW helpers, *adguard.Client, *singbox.Manager and
// *netusage.Tracker, and passes it to RegisterNetwork below.
type NetworkDeps struct {
	// UFWStatus mirrors handleUFW's GET branch: `ufw status numbered`,
	// parsed down to enabled/output. installed/error handling stays in the
	// real closure exactly as handleUFW already does it.
	UFWStatus func() (enabled bool, output string, err error)
	// UFWApplyRule mirrors handleUFWRule: action is one of
	// allow/deny/reject/delete/enable/disable, spec is forwarded VERBATIM
	// via strings.Fields into the ufw command line — never re-parsed or
	// re-validated here, exactly like handleUFWRule's own
	// mustPrimary-gated behavior (this seam's caller, security_actions.go,
	// applies the equivalent admin-only gate).
	UFWApplyRule func(action, spec string) (output string, err error)
	// AdGuardStatus mirrors adguard.Client.Status: fans /control/status and
	// /control/stats, never fails outright on a partial sub-call (see
	// adguard.Status.Errors).
	AdGuardStatus func(ctx context.Context) (*adguard.Status, error)
	// AdGuardSetProtection mirrors adguard.Client.SetProtection.
	AdGuardSetProtection func(ctx context.Context, enabled bool, durationMs int) error
	// ListDevices mirrors singbox.Manager.List.
	ListDevices func() ([]DeviceRow, error)
	// AddDevice mirrors singbox.Manager.Add. Threads ctx like every mutating
	// DockerDeps field does, matching Manager.Add's own signature. Name
	// normalization/validation (singbox.NormalizeName/ValidName, the same
	// steps handleTunnelDevices' POST branch applies) happens in
	// security_actions.go before this is called, not in this closure.
	AddDevice func(ctx context.Context, name string) (DeviceRow, error)
	// RemoveDevice mirrors singbox.Manager.Remove.
	RemoveDevice func(ctx context.Context, uuid string) error
	// SetDeviceExit mirrors singbox.Manager.SetExit (exit must be
	// singbox.ExitVPS or singbox.ExitCasa — validated in security_actions.go
	// before this is called, same posture as docker_actions.go validating
	// prune kinds before calling DockerDeps.Prune).
	SetDeviceExit func(ctx context.Context, uuid, exit string) error
	// SetDeviceDatasaver mirrors singbox.Manager.SetDatasaver ITSELF —
	// deliberately not the two extra safety gates
	// handleTunnelDeviceAction's "datasaver" case wraps around that same
	// call (a CA-ack confirmation and a live health probe of the exit's
	// compression proxy, both guarding against a real, documented outage:
	// turning this on without them broke HTTPS entirely, "it has broken
	// the tunnel repeatedly"). Those two gates are reproduced in
	// security_actions.go's handler instead, using
	// ProbeDatasaverHealthy below plus a ca_ack field on the action's own
	// input body — see that handler's doc comment for why this split
	// exists rather than folding the gates into this closure.
	SetDeviceDatasaver func(ctx context.Context, uuid string, on bool) error
	// ProbeDatasaverHealthy mirrors (*api.Router).probeDatasaverProxy — the
	// HEALTH-GATE half of the datasaver safety pair described above. Called
	// by security_actions.go before SetDeviceDatasaver whenever `on` is
	// true; never called when turning datasaver off, matching
	// handleTunnelDeviceAction's own "turning it off is never gated" comment.
	ProbeDatasaverHealthy func(ctx context.Context, exit string) (string, error)
	// RenameDevice mirrors singbox.Manager.Rename. The real closure must
	// apply singbox.NormalizeName the same way handleTunnelDeviceAction's
	// "rename" branch already does, before calling Manager.Rename.
	RenameDevice func(ctx context.Context, uuid, newName string) error
	// DeviceLink mirrors singbox.Manager.Link (the default, non-Reality
	// variant) — the same call handleTunnelDevices' POST branch makes right
	// after Manager.Add. LinkReality is only reachable on the web panel
	// behind an explicit ?variant=reality query param on a separate
	// link/qr fetch, which this v1 mobile surface does not expose (no
	// Reality-variant field in the devices screen's form) — see PLAN.md's
	// open question on this field, resolved here in favor of the default
	// call site.
	DeviceLink func(uuid string) (string, error)
	// UsageSnapshot mirrors netusage.Tracker.Snapshot — read-only,
	// conntrack-derived per-device usage.
	//
	// It returns an error for the same reason as ListAuditEvents: "no traffic"
	// and "the collector is not up" cannot reach the screen as the same empty
	// list. A tunnel with no measurement looking like a tunnel with no use is
	// exactly the error that makes someone conclude nobody is consuming a thing.
	UsageSnapshot func() ([]UsageRow, error)
	// AuditEvent records one audit entry under this package's own
	// security.* action vocabulary (security.ufw.rule, security.adguard.*,
	// security.device.*), same narrower shape as every other
	// *Deps.AuditEvent in this file.
	AuditEvent func(user, action, target string)
}

// MiscDeps is the injection seam for the four screens fanned out by Plano
// 08-06: ai.settings, jira.issues, deploy.apps, queue.jobs. Kept in one
// struct (unlike Security/Network's split into two) because none of these
// four domains is on its own large enough to earn a dedicated *Deps type —
// see misc.go's package-level doc comment for why these four are grouped,
// and for the explicit boundary against Phase 6's self-deployment mechanism
// (ops_deploy.go/ops_health.go), which this struct's closures never call.
type MiscDeps struct {
	// ai.settings: tiering de modelos — NUNCA segredos --------
	// AIModelsConfig mirrors handleAIModelsConfig's GET branch: the current
	// config plus the resolved effective model per tier (env > config >
	// default). Only model-tier strings (haiku/sonnet/opus/fable/"") ever
	// flow through this screen — internal/aimodel has no concept of an API
	// key or prompt content, so there is no secret value this closure could
	// leak even by mistake.
	AIModelsConfig func() (cur config.AIModels, effective map[string]string)
	// SaveAIModels mirrors handleAIModelsConfig's POST branch: validates
	// each tier against aimodel.Allowed (the same allowlist) and persists.
	// misc_actions.go re-validates before calling this so an invalid model
	// comes back as a field-keyed sdui.FieldErrors instead of this error's
	// generic string, but this closure keeps its own check too, matching
	// the panel's own defense in depth.
	SaveAIModels func(config.AIModels) error

	// --- jira.issues: gerencia issues (list/view/transition/comment) ------
	// Deliberately does NOT reproduce the web panel's kanban board (PLAN.md
	// Risks: "permanently, not for now, see 08-05's gameconsole precedent")
	// — this screen is a table + detail, never a board/column component.
	//
	// JiraStatus mirrors handleJiraConfig's GET branch reduced to the one
	// fact this screen needs: is this user connected, and to which project.
	// The token itself NEVER crosses this seam — only this
	// masked boolean, mirroring jira.Config.HasToken.
	JiraStatus func(user string) (connected bool, project string)
	// JiraConnect mirrors handleJiraConfig's POST branch: writes
	// site/email/token/project into the CALLER'S OWN per-user vault
	// (scope.NewUserVault), exactly like the web panel's per-user Jira
	// credential model — never a global/shared credential.
	JiraConnect func(user string, site, email, token, project string) error
	// JiraListIssues mirrors the search half of handleJiraBoard —
	// Client.Search scoped to the caller's own vault-derived client
	// (jiraClientForOwner's pattern).
	JiraListIssues func(ctx context.Context, user, jql string) ([]jira.Issue, error)
	// JiraGetIssue mirrors handleJiraIssue's GET branch.
	JiraGetIssue func(ctx context.Context, user, key string) (*jira.IssueDetail, error)
	// JiraTransitions mirrors Client.Transitions — the live, current set of
	// valid transitions for one issue. Used BOTH to populate the transition
	// form's options_source and to validate a submitted transition_id
	// before calling JiraTransition, so an invalid/stale transition comes
	// back as a form-level sdui.FieldErrors, never a raw Jira API error
	// passthrough (misc_actions.go's handleJiraIssueTransition).
	JiraTransitions func(ctx context.Context, user, key string) ([]jira.Transition, error)
	// JiraTransition mirrors Client.Transition.
	JiraTransition func(ctx context.Context, user, key, transitionID string) error
	// JiraAddComment mirrors Client.AddComment.
	JiraAddComment func(ctx context.Context, user, key, text string) (*jira.Comment, error)

	// --- deploy.apps: internal/deploy's PaaS catalog — NEVER the
	// self-deploy mechanism (ops_deploy.go, triggered by
	// `agentctl deploy`) ------------------------------------------------
	// ListDeployApps mirrors handleDeployApps' GET branch.
	ListDeployApps func() ([]deploy.App, error)
	// GetDeployApp mirrors handleDeployApp.
	GetDeployApp func(name string) (deploy.App, bool, error)
	// CreateDeployApp mirrors handleDeployApps' POST branch (Store.Create).
	CreateDeployApp func(deploy.App) (deploy.App, error)
	// TriggerRedeploy mirrors handleDeployTrigger: enqueues an app_deploy
	// queue job for this app's current branch HEAD. This triggers
	// internal/deploy's OWN PaaS app_deploy runner — a completely different
	// job kind and code path from Phase 6's SelfDeployRunner/`agentctl
	// deploy`, which this closure never touches.
	TriggerRedeploy func(user, name string) (jobID string, err error)
	// DestroyDeployApp mirrors handleDeployDestroy (Store.Destroy) —
	// Destructive, admin-only, confirm_destructive-gated.
	DestroyDeployApp func(ctx context.Context, name string) error

	// --- queue.jobs: internal/queue's generic background job queue —
	// NEVER the self-deploy status (ops_health.go, GET
	// /ops/status) -------------------------------------------------------
	// ListQueueJobs mirrors handleQueue's GET branch's owner-scoping rule
	// EXACTLY: caller passes "" for admin (every job) or v.Username for
	// non-admin (own jobs only) — the same forcing handleQueue's GET
	// applies server-side regardless of any client-supplied filter.
	ListQueueJobs func(owner string) []*queue.Job
	// GetQueueJob mirrors Queue.Get — used by misc_actions.go's cancel/rerun
	// handlers to look up Owner before authorizing (action.go's own doc
	// comment: a handler must fetch the resource via the domain package's
	// own accessor, never trust a client-supplied owner).
	GetQueueJob func(id string) (*queue.Job, error)
	// CancelQueueJob mirrors Queue.Cancel — the ownership check happens in
	// misc_actions.go BEFORE this is called; handlers_queue.go's own cancel
	// branch has no additional per-kind re-check beyond ownership, mirrored
	// here exactly (no extra AuthorizedFor gate on cancel).
	CancelQueueJob func(id string) error
	// RerunQueueJob mirrors Queue.Rerun. The caller (misc_actions.go) must
	// additionally re-check per-kind runner authorization via
	// AuthorizedForRerun below BEFORE calling this — mirroring
	// handleQueueByID's "rerun" branch, which layers Runner.AuthorizedFor
	// on top of plain ownership.
	RerunQueueJob func(id string) (*queue.Job, error)
	// AuthorizedForRerun mirrors handleQueueByID's rerun branch: looks up
	// the job's kind runner and asks Runner.AuthorizedFor(user, isAdmin).
	// Exposed separately from RerunQueueJob because the runner registry
	// (r.queueRunners) is private to internal/api — this is the one place
	// in MiscDeps a mutation needs a per-kind authorization check that
	// isn't the flat admin/non-admin binary every other *Deps.AuditEvent
	// gate in this file uses.
	AuthorizedForRerun func(user string, isAdmin bool, kind string) bool

	// AuditEvent records one audit entry under this package's own
	// ai_settings.*/jira.*/deploy.*/queue.* vocabulary, same narrower shape
	// as every other *Deps.AuditEvent in this file.
	AuditEvent func(user, action, target string)
}

// AlertRuleRow is the AlertsDeps.ListAlertRules row/table shape — mirrors
// internal/notify.Rule's fields the mobile screen actually surfaces
// (ID/Name/Enabled/TypePrefix/MinSeverity/Channels). SourcePrefix/Labels are
// deliberately absent from this seam entirely, mirroring AuditFilter's
// "deliberately minimal for v1" precedent above: a rule saved from the web
// panel's own Alertas tab with those set keeps them untouched, this seam
// simply never reads or edits them.
type AlertRuleRow struct {
	ID          string
	Name        string
	Enabled     bool
	TypePrefix  string
	MinSeverity string
	Channels    []string
}

// AlertRuleInput is what AlertsDeps.SaveAlertRule accepts. Empty ID means
// create (mirrors notify.Router.UpsertRule's own empty-ID-generates-new-id
// behavior — see internal/notify/store.go), non-empty ID means edit. There
// is no alerts.rule.edit action distinct from alerts.rule.save: exactly like
// scheduler.job.save/security.user.save, the client's generic
// tap-row-to-populate-form behavior feeds this one action for both create
// and edit.
type AlertRuleInput struct {
	ID          string
	Name        string
	Enabled     bool
	TypePrefix  string
	MinSeverity string
	Channels    []string
}

// ChannelOption is one entry of AlertsDeps.ChannelOptions — the live catalog
// of configured notify.ChannelDef instances a rule can route to (id + human
// label), sourced from internal/notify's own channel store, never a
// hardcoded list, so a channel the panel's own Alertas tab has not
// configured never appears as a selectable option here either.
type ChannelOption struct {
	Value string
	Label string
}

// EventOption is one entry of AlertsDeps.EventOptions — the type_prefix
// catalog a rule's "condition" field selects from, sourced from internal/api's
// own notify event catalog (the same catalog the panel's own Alertas tab
// rule-builder form renders), never invented here.
type EventOption struct {
	Value string
	Label string
}

// AlertsDeps is the injection seam for the alerts.rules screen.
// internal/api/api.go constructs one value of this type adapting
// internal/notify.Router — specifically Rule/UpsertRule/DeleteRule/
// ChannelDefsRedacted — NOT internal/metrics.Engine's threshold rules: see
// alerts.go's package doc comment for why (metrics.Engine has no Channels
// field and its HTTP handlers enforce zero RBAC, while notify's handlers —
// handleNotifyRules/handleNotifyRuleDelete in internal/api/handlers_notify.go
// — are mustPrimary-gated on every method including GET, matching this
// screen's whole-screen admin-only gate exactly).
type AlertsDeps struct {
	// ListAlertRules mirrors handleNotifyRules' GET branch (Router.Rules()).
	// v is accepted per the shared *Deps convention even though every real
	// call here is already admin-gated by Build itself
	// (buildAlertsRulesScreenForViewer returns ErrScreenNotFound for
	// non-admin before this closure is ever reached) — kept for signature
	// parity with every other List* closure in this file.
	ListAlertRules func(v sdui.Viewer) []AlertRuleRow
	// ChannelOptions mirrors handleNotifyCatalog/handleNotifyChannels' read
	// side — the currently configured channel instances a rule can route to.
	ChannelOptions func() []ChannelOption
	// EventOptions mirrors handleNotifyCatalog's event/type_prefix half —
	// the condition catalog the form's "condition" field selects from.
	EventOptions func() []EventOption
	// SaveAlertRule mirrors handleNotifyRules' POST branch
	// (Router.UpsertRule). notify.UpsertRule performs NO field validation
	// itself (see store.go) — alerts_actions.go validates name/condition/
	// threshold/channel BEFORE calling this, exactly like
	// handleSchedulerJobSave's pre-check pattern, since this seam is the
	// only place that validation can happen for the mobile path.
	SaveAlertRule func(AlertRuleInput) (*AlertRuleRow, error)
	// DeleteAlertRule mirrors handleNotifyRuleDelete (Router.DeleteRule).
	DeleteAlertRule func(id string) error
	// AuditEvent records one audit entry under this package's own
	// alerts.rule.* vocabulary, same narrower shape as every other
	// *Deps.AuditEvent in this file.
	AuditEvent func(user, action, target string)
}
