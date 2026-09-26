package api

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/gorilla/websocket"

	"server-control-panel/internal/adguard"
	"server-control-panel/internal/aimodel"
	"server-control-panel/internal/aiprompts"
	"server-control-panel/internal/alert"
	"server-control-panel/internal/auth"
	"server-control-panel/internal/claudeacct"
	"server-control-panel/internal/config"
	"server-control-panel/internal/deploy"
	docksvc "server-control-panel/internal/docker"
	"server-control-panel/internal/files"
	"server-control-panel/internal/gameservers"
	gitsvc "server-control-panel/internal/git"
	"server-control-panel/internal/httpmw"
	"server-control-panel/internal/httpx"
	"server-control-panel/internal/inventory"
	"server-control-panel/internal/jira"
	"server-control-panel/internal/jiraai"
	"server-control-panel/internal/metrics"
	"server-control-panel/internal/mobilebff"
	"server-control-panel/internal/mobilebff/screens"
	"server-control-panel/internal/mobilebff/sdui"
	"server-control-panel/internal/netusage"
	"server-control-panel/internal/notify"
	"server-control-panel/internal/notify/fcmpush"
	"server-control-panel/internal/procs"
	ptysvc "server-control-panel/internal/pty"
	"server-control-panel/internal/pve"
	"server-control-panel/internal/queue"
	"server-control-panel/internal/scheduler"
	"server-control-panel/internal/scope"
	"server-control-panel/internal/secrets"
	"server-control-panel/internal/sessions"
	"server-control-panel/internal/sysextra"
	"server-control-panel/internal/telemetry"
	"server-control-panel/internal/videocall"
	"server-control-panel/internal/webassets"
	"server-control-panel/internal/webpush"
	"server-control-panel/internal/whatsapp"
	"server-control-panel/internal/wsorigin"
)

// The front-end embed and buildStamp used to live here — they moved to internal/webassets/.
// The aliases are kept to preserve the remaining internal references:
var webFS = webassets.FS
var buildStamp = webassets.BuildStamp

// telemetryFork identifies the fork in the telemetry record.
// It is a source-code constant, not a build-time one: the fork IS this
// repository, and the value never varies from build to build. The copy of this
// package in the VM panel passes "vm-manager"; here it passes "vps-manager".
// That field is what lets triage add both JSONL streams together without
// confusing screens that exist on only one side.
const telemetryFork = "vps-manager"

// schedulerScreenRegisterOnce guards the scheduler.jobs screen registration
// (sdui.Register + RegisterAction, see internal/mobilebff/screens.Register) so
// that it runs ONCE per process. internal/mobilebff/sdui keeps those
// registrations in a global map that panics on a second registration of the
// same id — the right policy in production, where NewRouter runs exactly once
// at boot, but the internal/api test package builds dozens of *Router per
// process (one per test). Without this guard, the second NewRouter in any test
// of the package panics trying to register "scheduler.jobs" again, even in
// tests that have nothing to do with the scheduler.
var schedulerScreenRegisterOnce sync.Once

// dockerScreenRegisterOnce guards the six Docker screens' registration
// (screens.RegisterDocker) exactly like schedulerScreenRegisterOnce guards
// scheduler.jobs — see that var's comment for why this must be a
// package-level sync.Once rather than an init() or a plain call.
var dockerScreenRegisterOnce sync.Once

// systemScreenRegisterOnce guards the five System screens' registration
// (screens.RegisterSystem) exactly like dockerScreenRegisterOnce guards the
// Docker screens — see schedulerScreenRegisterOnce's comment for why this
// must be a package-level sync.Once rather than an init() or a plain call.
var systemScreenRegisterOnce sync.Once

// securityScreenRegisterOnce guards the four Security screens' registration
// (screens.RegisterSecurity: users/secrets/sessions/audit) exactly like
// systemScreenRegisterOnce guards the System screens — see
// schedulerScreenRegisterOnce's comment for why this must be a
// package-level sync.Once rather than an init() or a plain call.
var securityScreenRegisterOnce sync.Once

// networkScreenRegisterOnce guards the four Network screens' registration
// (screens.RegisterNetwork: ufw/adguard/devices/economia — all four
// registered under the "security." screen id prefix even though the seam
// is its own NetworkDeps struct, see deps.go's NetworkDeps doc comment)
// exactly like securityScreenRegisterOnce guards the Security screens.
var networkScreenRegisterOnce sync.Once

// miscScreenRegisterOnce guards the four screens fanned out by
// screens.RegisterMisc (ai.settings/jira.issues/deploy.apps/queue.jobs)
// exactly like networkScreenRegisterOnce guards the Network screens — see
// schedulerScreenRegisterOnce's comment for why this must be a
// package-level sync.Once rather than an init() or a plain call.
var miscScreenRegisterOnce sync.Once

// alertsScreenRegisterOnce guards the alerts.rules screen's registration
// (screens.RegisterAlerts) exactly like miscScreenRegisterOnce guards the
// four Misc screens — see schedulerScreenRegisterOnce's comment for why this
// must be a package-level sync.Once rather than an init() or a plain call.
var alertsScreenRegisterOnce sync.Once

type Router struct {
	cfg     *config.Config
	cfgMu   sync.Mutex
	auth    *auth.Service
	limiter *auth.Limiter
	lockout *auth.Lockout
	// mobileRefreshLimiter is a limiter SEPARATE from r.limiter: POST
	// /api/mobile/v1/auth/refresh is called proactively and often by design, so
	// it needs a far more generous budget than login — but "no limit at all"
	// would still be an amplification and brute-force vector.
	mobileRefreshLimiter *auth.Limiter
	audit                *auth.AuditLog
	docker               *docksvc.Client
	gameMgr              *gameservers.Manager // pagina Jogos: servidores de jogo em Docker
	secrets              *secrets.Store
	netUsage             *netusage.Tracker // per-device tunnel usage (conntrack, read-only)
	ring                 *metrics.Ring
	alerts               *metrics.Engine
	metricReg            *metrics.Registry      // metric catalogue + snapshot
	metricHist           *metrics.MetricHistory // per-metric time series
	fires                []metrics.Fire
	firesMu              sync.Mutex
	mux                  *http.ServeMux
	whatsappMgr          *whatsapp.Manager  // multi-tenant — always non-nil when r.secrets != nil
	videocall            *videocall.Service // never nil after NewRouter; TURN may be nil if coturn isn't configured
	// webpush is the shared VAPID keypair + subscription store: one keypair
	// for both videocall's incoming-call ring and notify's push channel, so
	// neither orphans the other's subscriptions. nil if key generation
	// failed at boot (both features then degrade gracefully).
	webpush    *webpush.Store
	sessionsSt *sessions.Store // kept for Close on shutdown
	handler    http.Handler    // composed after NewRouter — ServeHTTP delegates
	startTime  int64           // unix sec do NewRouter — uptime em /metrics
	// sessionOwn is the per-user ownership registry for sessions; nil
	// only when the registry file cannot be opened (boot loud-warns and
	// we degrade gracefully — primary user still sees all, non-primary
	// sees nothing, matching the closed-fail policy).
	sessionOwn *ptysvc.Ownership
	// sessReg is the session-registry sidecar (<DataDir>/session-registry.json)
	// — the tool-agnostic source of truth that replaces the session registry's
	// native catalogue. Loaded at boot and populated per session by the dtach
	// backend. nil only when the file cannot be opened.
	sessReg *ptysvc.Registry
	// queue is the background-job runner (F3). Workers + persistence +
	// runner registry. nil only if NewQueue failed at boot — handlers
	// degrade to 503 in that case.
	queue        *queue.Queue
	queueRunners map[string]queue.Runner
	deployStore  *deploy.Store // #33: PaaS Heroku-style (apps.json + repos bare)

	// Multi-node inventory. The store may be nil: the panel ALWAYS comes up,
	// and its absence becomes a 503 in the deployStoreOrNil pattern.
	inventoryStore  *inventory.Store
	inventoryPoller *inventory.Poller
	pveConfig       *pve.Config // descriptor for data/pve/pve.json; nil = hypervisor not configured
	// The three below are TEST injection points. nil in production, where each
	// falls through to the real implementation. They exist because proving the
	// ORDER of a revocation and expiry-by-clock requires test doubles, and this
	// repo uses no mocking framework.
	inventoryNow func() time.Time
	nodeVaultFn  func() (nodeVault, error)
	pveDial      func(tokenValor string) (hypervisorOps, error)
	// scheduler is the cron-driven launcher (F2) that enqueues into queue.
	// nil only when init failed; handlers degrade to 503.
	scheduler *scheduler.Scheduler
	// jiraWorkWatchers follows the Jira "work on it now" sessions in the
	// background — each watcher polls the session log for phrases suggesting the
	// user considers it finished (e.g. "it's working") and fires the transition
	// to Done. The map is keyed by session name.
	jiraWorkWatchers   map[string]*jiraWorkWatcher
	jiraWorkWatchersMu sync.Mutex
	// aiPrompts is the runtime-editable registry of the AI prompts. It is read
	// by jiraai.Runner and by the "work on it now" prompt, and written by the
	// gated GET/PUT /api/ai/prompts endpoint. Never nil after NewRouter.
	aiPrompts *aiprompts.Registry

	// claudeAccts is the per-consumer Claude account selector.
	// ConfigDirFor("jobs"|"terminal"|"fork") decides each spawn's
	// CLAUDE_CONFIG_DIR. It can be nil in a degraded boot, in which case
	// consumers inherit the default account (/root/.claude), with no regression.
	claudeAccts *claudeacct.Store

	// notify is the event-driven notification spine: it routes Events (job.*,
	// metric.threshold, scheduler.enqueue_failed) to channels (WhatsApp in v1)
	// by configurable rules. nil only if New fails at boot — handlers then
	// degrade to 503 and the queue hook becomes a no-op (it fails closed).
	notify *notify.Router
	// pushDevices/pushDevicePrefs are the Android device-registration store and
	// the per-Rule push-preference store — both built exactly once in
	// initNotify (notify_wire.go) and shared between the "push" channel
	// (fcmpush.DeviceStore / notify.DevicePrefsResolver) and the mobileDeps
	// injected into the mobile BFF (push_devices.go/notify_prefs.go), so there
	// are never two divergent views of the same mobile-devices-*.json.
	pushDevices     *mobilebff.DeviceTokenStore
	pushDevicePrefs *mobilebff.DevicePrefsStore
	// fcmSender is the *fcmpush.Sender built in initNotify (notify_wire.go)
	// from the service-account credential in the vault — kept here, rather than
	// only local to initNotify, so that videocall.Open just below reuses the
	// SAME instance in Options.FCM instead of building a second Sender and HTTP
	// client for the same destination. nil when the push credential has not
	// been provisioned yet (it degrades like every other optional field here).
	fcmSender *fcmpush.Sender
	// sentinela watches whether the home hypervisor is REACHABLE from the VPS —
	// the one point in the system that can still speak when the house goes down. See hypervisor_watcher.go.
	sentinela *hipervisorSentinela
	// sentinelaSink diverts the sentinel's output in tests. In production it is
	// nil and the event goes to the notification router.
	sentinelaSink func(notify.Event)

	// Agent status telemetry (VPSM agent-ops #3/#4). agentStatus writes the
	// shared <DataDir>/session-status.json the code-server session extension reads;
	// agentCWD maps dtach session name → cwd (the hook + aggregator resolve
	// through it); agentHookSecret gates POST /api/agent/hook; agentCostCache is
	// the aggregator's mtime cache (aggregator-goroutine-owned, no lock).
	agentStatus     *agentStatusStore
	agentCWD        *agentCWDStore
	agentHookSecret string
	agentCostCache  map[string]agentCostEntry
	// Spend ceilings (VPSM Wave-3 #55). agentBudget owns <DataDir>/agent-budget.json
	// (alert-only caps); budgetNotified throttles alerts to once-per-breach-per-period
	// (aggregator-goroutine-owned, like agentCostCache — no lock).
	agentBudget    *agentBudgetStore
	budgetNotified map[string]bool
	// telSink is the JSONL sink for screen-usage telemetry.
	// nil when the directory cannot be created at boot — the /api/telemetry route
	// is then not even registered, and the panel carries on whole, just unmeasured.
	telSink *telemetry.Sink

	// webauthnRP is the WebAuthn Relying Party (passkeys), built in initPasskey
	// from Config.PublicHostname. It lives on Router — never on *auth.Service —
	// because handleChangePassword rebuilds the whole of r.auth via
	// auth.New(...); nil when PublicHostname is empty, which leaves passkeys
	// inert as a graceful degradation. See internal/api/passkey.go and
	// internal/auth/webauthn.go.
	webauthnRP *webauthn.WebAuthn

	// mobileHub is the connection registry for /ws/mobile-events
	// (internal/mobilebff/events_hub.go) — the single multiplexed socket every
	// "live" surface of the app uses (deploy log, health/queue/alerts, notify).
	// Instantiated once in NewRouter, injected via mobilebff.Deps.Hub.
	mobileHub *mobilebff.Hub
	// pararTrabalhadores cancels the context of the background workers started
	// by StartBackgroundWorkers. nil while nobody has started them — which is
	// exactly the state of a Router built by a test.
	pararTrabalhadores context.CancelFunc

	// mobileOpsHealthStop stops the ticker of internal/mobilebff.
	// StartOpsHealthPublisher — called in Shutdown. nil if the Hub was never
	// instantiated.
	mobileOpsHealthStop func()
}

// The WebSocket Origin policy moved to internal/wsorigin/wsorigin.go
// — it is shared by api/, pty/, videocall/ and whatsapp/.
func wsCheckOriginSameHostLegacy(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		// Some clients (curl, native apps) send no Origin — allowed.
		return true
	}
	u, err := url.Parse(o)
	if err != nil {
		return false
	}
	if u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

// processStartTime — moved to webassets.ProcessStartTime. The alias is kept
// for internal references (audit_handlers and the like).
var processStartTime = webassets.ProcessStartTime

var wsUpgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin:     wsorigin.CheckSameHost,
}

func NewRouter(cfg *config.Config) (*Router, error) {
	authSvc := auth.New(cfg.JWTSecret, credsFromConfig(cfg))
	// Same as the boot path: handleChangePassword rebuilds the auth Service.
	// Refactor: extract this into a function the next time it is reworked.
	if cfg.SupabaseURL != "" && cfg.SupabaseAnonKey != "" {
		backend := auth.BackendSupabase
		if v := os.Getenv("VPSM_AUTH_BACKEND"); v != "" {
			backend = auth.AuthBackend(v)
		} else if cfg.AuthBackend != "" {
			backend = auth.AuthBackend(cfg.AuthBackend)
		}
		uuidMapPath := filepath.Join(cfg.DataDir, "migration-uuid-map.json")
		uuidMap, err := auth.LoadUUIDMap(uuidMapPath)
		if err != nil {
			log.Printf("auth: WARNING uuid map load failed (%v) — falling back to BackendLocal", err)
		} else if uuidMap == nil || uuidMap.Size() == 0 {
			log.Printf("auth: uuid map empty at %s — staying on BackendLocal", uuidMapPath)
		} else {
			sbClient := auth.NewSupabaseClient(cfg.SupabaseURL, cfg.SupabaseAnonKey)
			authSvc = authSvc.WithSupabase(backend, sbClient, uuidMap)
			log.Printf("auth: backend=%s supabase_url=%s uuid_map_entries=%d", backend, cfg.SupabaseURL, uuidMap.Size())
		}
	} else {
		log.Printf("auth: backend=local (no supabase_url in config)")
	}
	r := &Router{
		cfg:     cfg,
		auth:    authSvc,
		limiter: auth.NewLimiter(10, 60*time.Second),
		// Five failures trips the lock; the cooldown starts at 30s and doubles
		// every five further failures up to a ceiling of 30min. Forgetting window: 1h.
		lockout: auth.NewLockout(5, 30*time.Second, 30*time.Minute, time.Hour),
		// 120/min per IP — generous enough for the app to refresh proactively
		// without friction, but not "no limit at all".
		mobileRefreshLimiter: auth.NewLimiter(120, 60*time.Second),
		mux:                  http.NewServeMux(),
		ring:                 metrics.NewRing(1440),
		alerts:               metrics.NewEngine(),
		startTime:            time.Now().Unix(),
	}
	if err := r.alerts.Load(filepath.Join(cfg.DataDir, "alert_rules.json")); err != nil {
		log.Printf("alert rules load: %v", err)
	}
	if d, err := docksvc.New(); err == nil {
		r.docker = d
	}
	if al, err := auth.NewAuditLog(filepath.Join(cfg.DataDir, "audit.log")); err == nil {
		r.audit = al
	} else {
		log.Printf("audit log disabled: %v", err)
	}
	// The RENAME of the ownership sidecar is gone. It used to move the file from
	// its old name to `session-ownership.json` on first boot. Checked before
	// deleting: the old file no longer exists on disk.
	if own, err := ptysvc.LoadOwnership(filepath.Join(cfg.DataDir, "session-ownership.json")); err == nil {
		r.sessionOwn = own
		// Register ownership in the pty package so that backup and the watcher can
		// resolve a session's OWNER and read the right log (no globbing users/* and taking matches[0] across users).
		ptysvc.SetActiveOwnership(own)
	} else {
		log.Printf("session ownership registry disabled: %v", err)
	}
	// session-registry sidecar. A missing file means an empty map
	// (LoadRegistry tolerates it). The name deliberately differs from sessions.json (below).
	if reg, err := ptysvc.LoadRegistry(filepath.Join(cfg.DataDir, "session-registry.json")); err == nil {
		r.sessReg = reg
	} else {
		log.Printf("session registry disabled: %v", err)
	}
	// Pins the active session backend from the VPSM_SESSION_BACKEND flag (dtach
	// by default). A single point; the pty package's Session* dispatchers all route through it.
	ptysvc.InitSessionBackend(cfg.DataDir, r.sessReg)
	// Per-session log recorder: without this, a session that survived a deploy
	// has nobody recording it until someone opens a tab on it — and the hole in
	// the log reappears exactly in the window when nobody is watching. In a
	// goroutine because each recorder spawns a `dtach` client and boot must not
	// wait on that. See `internal/pty/gravador.go`.
	go ptysvc.GaranteGravadoresDasSessoesVivas(cfg.DataDir, r.sessReg, r.sessionOwn, cfg.Primary)
	if ss, err := sessions.Open(filepath.Join(cfg.DataDir, "sessions.json")); err == nil {
		r.auth = r.auth.WithSessions(ss)
		r.sessionsSt = ss
	} else {
		log.Printf("sessions store disabled: %v", err)
	}
	if st, err := secrets.Open(filepath.Join(cfg.DataDir, "secrets.vault"), cfg.JWTSecret); err == nil {
		r.secrets = st
	} else {
		log.Printf("secrets disabled: %v", err)
	}

	// AI prompt registry — runtime-editable templates persisted to
	// <DataDir>/ai_prompts.json. Initialised before the queue so NewRunner
	// can inject it. Always non-nil (falls back to compiled-in defaults).
	r.aiPrompts = aiprompts.New(cfg.DataDir)
	r.deployStore = deploy.Open(cfg.DataDir) // #33: registro de apps (apps.json com lock cross-processo (flock))

	// Multi-node inventory. BOTH of these may fail without taking the panel
	// down: a missing store becomes a 503 on the route (the deployStoreOrNil
	// pattern) and a missing descriptor becomes a seeds-only inventory. The
	// panel ALWAYS comes up — a lab that will not open because the hypervisor is
	// down is the opposite of what it is for, since the panel is exactly where
	// you go to LOOK when something has fallen over.
	if st, err := inventory.Open(cfg.DataDir); err != nil {
		log.Printf("inventory: store unavailable (%v) — /api/nodes will answer 503", err)
	} else {
		r.inventoryStore = st
	}
	if pc, err := loadPVEDescriptor(cfg.DataDir); err != nil {
		log.Printf("inventory: %v — the inventory will run with the seeds.json nodes only", err)
	} else {
		r.pveConfig = pc
	}

	// Per-consumer Claude account selector. Initialised before the queue so that
	// NewRunner can inject the jobs' account. A failure here is non-fatal:
	// claudeAccts == nil means every consumer falls back to the default account.
	if cas, err := claudeacct.Open(cfg.DataDir, cfg.ClaudeHome); err == nil {
		r.claudeAccts = cas
	} else {
		log.Printf("claude accounts disabled: %v", err)
	}

	// Background-jobs queue (F3). Workers default 3; persisted state lives
	// under <DataDir>/queue/. Failure here is non-fatal — the rest of the
	// app still boots; handlers degrade to 503.
	if q, err := queue.NewQueue(queue.Options{DataDir: cfg.DataDir, Workers: 3, MaxKeep: 500}); err == nil {
		r.queue = q
		r.queueRunners = map[string]queue.Runner{}
		register := func(run queue.Runner) {
			q.Register(run)
			r.queueRunners[run.Kind()] = run
		}
		register(queue.AptUpgradeRunner{})
		register(queue.DockerPullRunner{})
		register(queue.DockerComposePullRunner{})
		register(queue.ImagePruneRunner{})
		register(queue.BackupNowRunner{DataDir: cfg.DataDir})
		register(queue.AppDeployRunner{DataDir: cfg.DataDir})               // #33: deploy PaaS git-push (UI/rollback)
		register(queue.DeployPreviewReapRunner{DataDir: cfg.DataDir})       // #37: TTL de preview envs
		register(queue.MobileUploadStagingReapRunner{DataDir: cfg.DataDir}) // TTL de upload movel abandonado
		register(queue.ShellRunner{})
		register(queue.DockerRestartRunner{})        // reiniciar container
		register(queue.DockerComposeRestartRunner{}) // restart compose services
		register(queue.SystemdRestartRunner{})       // restart/reload de unit systemd
		register(queue.DockerPruneRunner{})          // prune de volumes/redes/builder
		register(queue.HTTPCheckRunner{})            // health-check HTTP agendado
		// Security, auditing and host health
		register(queue.SSLCheckRunner{})       // TLS certificate expiry
		register(queue.DiskCheckRunner{})      // uso de disco acima do limite
		register(queue.SecurityAuditRunner{})  // lynis audit system
		register(queue.RootkitScanRunner{})    // rkhunter / chkrootkit
		register(queue.IntegrityCheckRunner{}) // aide --check
		register(queue.TrivyScanRunner{})      // scan de CVEs (imagem/fs)
		register(queue.Fail2banReportRunner{}) // status do fail2ban
		register(queue.AuditReportRunner{})    // snapshot timers/cron/portas/logins
		register(queue.CleanupRunner{})        // higiene apt/journal/tmp
		// More operations and maintenance
		register(queue.CertRenewRunner{})                                  // certbot renew
		register(queue.RcloneSyncRunner{})                                 // espelhar pasta -> nuvem
		register(queue.DockerComposeUpRunner{})                            // compose up -d
		register(queue.GitPullRunner{})                                    // git pull de um repo
		register(queue.AptUpdateCheckRunner{})                             // relatorio de pacotes atualizaveis
		register(queue.RebootRunner{})                                     // reiniciar o servidor
		register(queue.SelfDeployRunner{})                                 // agentctl deploy disparado pelo app mobile
		register(queue.DBBackupRunner{DataDir: cfg.DataDir})               // dump de banco postgres/mysql
		register(queue.WatchdogRunner{DataDir: cfg.DataDir})               // #46: watchdog de disco + backup
		register(queue.SessionBackupRunner{Backup: r.runSessionBackupJob}) // schedulable session backup (per session/all, per user)
		register(queue.AgentRoutineRunner{Spawn: r.runAgentRoutineJob})    // scheduled agent routine (detached spawn of a Claude session)
		// The boot-time migrations from the backup subsystem's rename are
		// gone. They existed to read the old directory names and the old job
		// kind and convert them. Checked before deleting: zero old directories
		// and zero jobs with the old kind on disk. Migration code that has
		// already migrated everything is just a way for the old word to keep
		// living.
		// Jira "Iniciar AI": spawns claude CLI in the repo's cwd, posts
		// the audit report back as a comment + labels. Per-user clients
		// + repo mapping injected via closures so the worker (which
		// has no http.Request context) still authenticates as the job
		// owner.
		register(jiraai.NewRunner(
			r.jiraClientForOwner,
			r.jiraRepoMapFor,
			r.aiPrompts,
			r.jobsConfigDir, // CLAUDE_CONFIG_DIR of the account assigned to jobs
			// The model for the jira_ai tier (env > config > default ""). Read
			// fresh on every spawn → an edit in the UI applies to the next job without a restart.
			func() string { return aimodel.For(aimodel.JiraAI, r.cfg.AIModels.JiraAI) },
		))
		// Run jira_ai_analysis DETACHED in its own systemd scope so a
		// deploy/restart of vps-manager doesn't kill a 5–20min analysis. The
		// reaper merges the detached job's progress back into /api/queue.
		// Disable with VPSM_DETACH_JOBS=0; if systemd-run is missing the
		// launcher returns an error and Enqueue falls back to in-process —
		// detach is a survivability bonus, never required.
		if os.Getenv("VPSM_DETACH_JOBS") != "0" {
			if exe, eErr := os.Executable(); eErr == nil {
				r.queue.SetDetach(func(id string) (string, error) {
					return launchDetachedJob(exe, id)
				}, "jira_ai_analysis")
			} else {
				log.Printf("detach disabled: os.Executable: %v", eErr)
			}
		}
	} else {
		log.Printf("queue disabled: %v", err)
	}

	// Scheduler (F2). Needs queue to be up; reads/writes
	// <DataDir>/scheduler/jobs.json. Tick loop starts immediately.
	if r.queue != nil {
		schedPath := filepath.Join(cfg.DataDir, "scheduler", "jobs.json")
		if sc, err := scheduler.New(schedPath, scheduler.QueueEnqueuer{Q: r.queue}); err == nil {
			r.scheduler = sc
			r.scheduler.SetAlerter(r.schedulerAlerter())
			// gate the autonomous tick by the same Runner.AuthorizedFor
			// the HTTP create/update/run-now paths use. An unknown kind denies
			// (never panics). Closes the bypass at the scheduler's fire chokepoint.
			r.scheduler.SetAuthorizer(func(owner, kind string) bool {
				runner, ok := r.queueRunner(kind)
				if !ok {
					return false
				}
				return runner.AuthorizedFor(owner, r.isPrimary(owner))
			})
			sc.Start()
		} else {
			log.Printf("scheduler disabled: %v", err)
		}
	}

	// Shared Web Push store: the SAME "videocalls" root videocall
	// would otherwise open lazily, constructed one call earlier so
	// initNotify's push channel and the videocall.Open call below share one
	// VAPID keypair instead of each generating (and orphaning) their own.
	if wp, err := webpush.Open(filepath.Join(cfg.DataDir, "videocalls")); err == nil {
		r.webpush = wp
	} else {
		log.Printf("webpush disabled: %v", err)
	}

	// Notification spine. Constructed here — right after the queue — so
	// SetNotifier wires the terminal-job hook as early as possible, keeping
	// the (benign, accepted) boot window minimal. The WhatsApp channel resolves
	// r.whatsappMgr lazily, so the manager being initialised a few lines below
	// is fine.
	r.initNotify()

	// The WebAuthn Relying Party (passkeys). After initNotify:
	// FinishPasskeyRegistration fires notify.TypeDevicePairingPending, so
	// r.notify has to exist by then.
	r.initPasskey()

	// Agent status telemetry (VPSM agent-ops #3/#4). Stores load from
	// <DataDir>/session-status.json and session-cwd.json (tolerating absence).
	// The hook secret is loaded/generated here so ensureAgentHooks() can embed
	// it into the spawned sessions' settings.json. All best-effort; failures
	// degrade to "no telemetry", never fatal.
	r.agentStatus = newAgentStatusStore(filepath.Join(cfg.DataDir, "session-status.json"))
	r.agentCWD = newAgentCWDStore(filepath.Join(cfg.DataDir, "session-cwd.json"))
	// Lets the pty package resolve a session's cwd (restore recreates it in the
	// right directory, and create can inherit it). Source: session-cwd.json.
	ptysvc.SetActiveCWDResolver(r.agentCWD.Get)
	r.agentHookSecret = r.loadAgentHookSecret()
	r.agentCostCache = map[string]agentCostEntry{}
	// Spend ceilings (#55): load the alert-only caps (defaults off) + throttle map.
	r.agentBudget = newAgentBudgetStore(filepath.Join(cfg.DataDir, "agent-budget.json"))
	r.budgetNotified = map[string]bool{}
	r.ensureAgentHooks()

	// WhatsApp integration: two modes coexist in this transitional release.
	//
	//   v1 (pre-migration): single-tenant. One Service in r.whatsapp reads
	//   global vault keys and talks to the legacy gateway container on :3000,
	//   managed by the un-templated systemd unit. Webhook at
	//   /api/whatsapp/webhook, with no user in the path.
	//
	//   v2 (post-migration): multi-tenant. r.whatsappMgr lazily creates one
	//   Service per profile against a dedicated container on a registry-issued
	//   port, managed by a templated systemd unit per user. Webhook at
	//   /api/whatsapp/webhook/<user>, HMAC-signed with that user's own secret
	//   from their vault namespace.
	//
	// cfg.SchemaVersion is the switch: 2 or above selects v2, anything lower
	// stays on the legacy path. The schema migration re-aliases every global
	// "waha_*" key as "<user>:waha_*", so after v2 the Manager already finds
	// the primary user's credentials where it expects them.
	//
	// The installer cannot decrypt the vault from a shell script, so it drops a
	// sidecar manifest at data/whatsapp/secrets.put which we ingest here once
	// and then delete. The fallback user is the primary one because before the
	// migration everything belonged to them; afterwards, whoever writes the
	// manifest states the user explicitly.
	if r.secrets != nil {
		ingestWhatsAppSecretsPut(cfg.DataDir, r.secrets, "sam")

		// The Manager is always constructed. It only activates once there are
		// provisioned users; while there are none ForUser returns an error and
		// the handler answers 503 — the UI shows "WhatsApp not configured yet"
		// without bringing anything down.
		// SelfBaseURL: needed to register the extra per-session webhook on the
		// gateway. It extracts the port from cfg.Listen (e.g. ":8766" → 8766).
		// With no extractable port it uses 8766, the v2 default. v1 (:8765)
		// stays intact while v2 runs alongside it sharing the gateway container.
		// VPSM_SELF_BASE_URL can override this for deploys behind a reverse proxy.
		selfBaseURL := strings.TrimSpace(os.Getenv("VPSM_SELF_BASE_URL"))
		if selfBaseURL == "" {
			selfPort := 8766
			if listen := strings.TrimSpace(cfg.Listen); listen != "" {
				if idx := strings.LastIndex(listen, ":"); idx >= 0 {
					if p, perr := strconv.Atoi(listen[idx+1:]); perr == nil && p > 0 {
						selfPort = p
					}
				}
			}
			selfBaseURL = fmt.Sprintf("http://127.0.0.1:%d", selfPort)
		}
		mgr, err := whatsapp.NewManager(whatsapp.ManagerOptions{
			DataDir:       cfg.DataDir,
			ContainerRoot: "/var/lib/vpsm-whatsapp",
			Vault:         r.secrets,
			SelfBaseURL:   selfBaseURL,
		})
		if err != nil {
			log.Printf("whatsapp manager init: %v", err)
		} else {
			r.whatsappMgr = mgr
			// Bootstrap provisioning: after the migration (v2), every existing
			// user gets their own directories, port, vault keys and systemd
			// unit. Idempotent — re-running rewrites compose/env, which is
			// useful when the template changes. Errors are logged; they never
			// bring the panel's boot down.
			if cfg.SchemaVersion >= 2 {
				for _, u := range cfg.AllUsers() {
					su, err := scope.New(u.Username)
					if err != nil {
						log.Printf("whatsapp bootstrap skip %q: %v", u.Username, err)
						continue
					}
					if err := mgr.Provision(su); err != nil {
						log.Printf("whatsapp bootstrap provision %s: %v", su, err)
						continue
					}
					// Eagerly create the Service at boot so that the poller,
					// the LID consolidation and the worker pool run even with
					// no HTTP request at all. Without this, @lid orphans were
					// not consolidated until the user clicked Sync.
					if _, err := mgr.ForUser(su); err != nil {
						log.Printf("whatsapp bootstrap eager-start %s: %v", su, err)
					}
				}
			}
		}

		// The v1 single-tenant path was removed: schema v1 has no active user
		// left. The Manager is always in charge now — Provision migrates the
		// directories from the legacy data/whatsapp/ to
		// data/users/sam/whatsapp/ on first run (see
		// Manager.bootstrapLegacyMigration).
	}

	// Videocall: in-process signaling + room registry. TURN config is loaded
	// from /etc/vpsm/coturn.env if `vpsmctl videocall init` has been run;
	// absent that file, the service still works (P2P + Google STUN only),
	// which is fine for LAN/same-NAT tests but will fail behind symmetric NAT.
	if vc, err := videocall.Open(videocall.Options{
		DataDir: cfg.DataDir,
		TURN:    loadTURNConfig(),
		Push:    r.webpush,   // shared with notify's push channel — no second VAPID keypair
		FCM:     r.fcmSender, // same instance notify's push channel uses (r.initNotify runs above, at boot) — no second FCM Sender/credential read
	}); err != nil {
		log.Printf("videocall init: %v", err)
	} else {
		r.videocall = vc
		// Wire audit log so videocall join/leave/recording show up in
		// data/audit.log alongside other panel actions.
		if r.audit != nil {
			vc.AuditFn = func(action, user, target string) {
				r.audit.Append(auth.Event{
					Time:   time.Now().Unix(),
					User:   user,
					Action: action,
					Target: target,
				})
			}
		}
		// Wire invite issuer (auth) + single-use tracker (sessions store).
		vc.InviteIssuer = r.auth
		if r.auth.Sessions() != nil {
			vc.InviteSessionsCk = inviteSessionsAdapter{r.auth.Sessions()}
		}
		// Cloud recordings: the blobs go under /var/lib (they can run to hundreds of MB).
		// If the directory is not writable the feature switches off gracefully — the UI hides it.
		if recs, err := videocall.OpenRecordingStore(cfg.DataDir, "/var/lib/vpsm-videocalls/recordings"); err == nil {
			vc.Recordings = recs
		} else {
			log.Printf("videocall recordings disabled: %v", err)
		}
		// WhatsApp invite sender: routes to the Manager (v2) or to the legacy
		// Service (v1). Under v2 the room owner (`user`) has a gateway
		// container of their own — invites go out from THEIR account, not a
		// shared one. The closure captures r; installing or migrating the
		// gateway only needs a restart.
		vc.WhatsAppSender = func(user, jid, text string) error {
			if r.whatsappMgr == nil {
				return errors.New("whatsapp not configured")
			}
			u, err := scope.New(user)
			if err != nil {
				return err
			}
			svc, err := r.whatsappMgr.ForUser(u)
			if err != nil {
				return err
			}
			_, err = svc.Client.SendText(jid, text, "")
			return err
		}
	}

	// The metrics catalogue plus per-subsystem collectors (every subsystem is
	// already wired above). It must come before the sampler.
	r.buildMetricRegistry()

	// netUsage is CONSTRUCTED here (handlers read the pointer), but it only
	// STARTS measuring in StartBackgroundWorkers — constructing is not starting.
	r.netUsage = netusage.New(r.cfg.SingboxDevicePortsPath, filepath.Join(r.cfg.DataDir, "netusage-totals.json"))

	// The background workers do NOT start here. See StartBackgroundWorkers.

	// Public
	r.mux.HandleFunc("/api/auth/login", r.handleLogin)
	// Refresh via the HttpOnly vpsm_refresh cookie — PUBLIC on purpose: it
	// renews the JWT once the access token has already expired (it cannot demand
	// a valid JWT, or there would be no way to recover on its own). This is what
	// stops auto-logout from killing the session or the terminal in an idle tab.
	// CSRF-safe (SameSite=Lax cookie).
	r.mux.HandleFunc("/api/auth/refresh-cookie", r.handleRefreshCookie)
	r.mux.HandleFunc("/api/health", r.handleHealth)
	// Detailed health per subsystem — dashboard cards.
	r.mux.HandleFunc("/api/health/detailed", r.handleHealthDetailed)
	// Digital Asset Links — PUBLIC on purpose: Android itself fetches this
	// route without credentials to validate the native app's App Links and
	// passkeys. No redirect and no auth.Middleware: either would break the
	// verification silently. It has to stay reachable on both public hostnames
	// (panel.northwind.example and panel.host01.example) behind the reverse
	// proxy.
	r.mux.HandleFunc("/.well-known/assetlinks.json", r.handleAssetLinks)
	// Prometheus /metrics — public (counters and gauges, nothing secret).
	r.mux.HandleFunc("/metrics", r.handlePrometheusMetrics)
	// The self-hosted package repository is public, with no auth middleware,
	// for the same reason as the app-links manifest above: the client fetching
	// it has no session cookie for this panel. It is deliberately NOT
	// registered on the mux — see handlers_fdroid.go and the middleware-chain
	// comment further down this function. Go's ServeMux 301-redirects any
	// "unclean" path before dispatching, and that redirect breaks the client.
	// First-login TOTP enrolment (both primary and recovery) — public
	// endpoints gated by a short-lived setup token minted at login when either
	// secret is missing. It cannot be used to reach protected APIs.
	// Those setup endpoints were later removed along with local TOTP. MFA is
	// now opt-in through the provider path; the handlers survive here without
	// a route.
	// /recovery is an independent emergency UI. It lives outside the SPA so a
	// broken index.html or a crash in the front-end framework cannot lock you
	// out. Auth is password plus a recovery TOTP secret, separate from the
	// primary second factor, with its own 30-minute HttpOnly cookie.
	r.mux.HandleFunc("/recovery", r.handleRecoveryPage)
	r.mux.HandleFunc("/recovery/auth", r.handleRecoveryAuth)
	r.mux.HandleFunc("/recovery/term", r.handleRecoveryTerm)
	r.mux.HandleFunc("/recovery/ws/pty", r.handleRecoveryPTY)
	r.mux.HandleFunc("/recovery/action/", r.handleRecoveryAction)
	r.mux.HandleFunc("/recovery/logout", r.handleRecoveryLogout)
	// A recovery Claude: its own container, running alongside the panel, with no
	// router in the path and a login of its own — for the case where the router,
	// the host installation or the panel itself is the problem. Same entry point
	// (the recovery cookie) as the routes above.
	r.mux.HandleFunc("/recovery/claude/status", r.handleRecoveryClaudeStatus)
	r.mux.HandleFunc("/recovery/ws/claude", r.handleRecoveryClaudePTY)
	// Renews the 30-minute session while the tab is in use: expiring in the
	// middle of a repair is the worst possible moment to ask for a password plus
	// a TOTP code. The absolute ceiling since login still applies.
	r.mux.HandleFunc("/recovery/renew", r.handleRecoveryRenew)
	// WhatsApp webhook (public — HMAC-SHA512 stands in for auth).
	//
	// Under v1 (legacy), the single-tenant gateway points at
	// /api/whatsapp/webhook (with no user); the one Service validates the HMAC
	// and processes the payload.
	//
	// Under v2 (Manager), each per-user gateway points at
	// /api/whatsapp/webhook/<user> — the Manager extracts the user from the
	// path, resolves that user's Service and validates the HMAC against the
	// namespaced secret.
	if r.whatsappMgr != nil {
		r.mux.HandleFunc("/api/whatsapp/webhook/", r.whatsappMgr.HandleWebhook)
		// Alertmanager webhook → WhatsApp. Loopback-only (validated inside the
		// handler). Config in r.cfg.Alerting; UI at /admin/alerting.
		r.mux.HandleFunc("/_internal/alert", alert.NewHandler(r.whatsappMgr, &r.cfg.Alerting))
	}
	// Forward-auth for SSO (the dashboard app and the like). It is NOT under
	// auth.Middleware: the handler decides inline between 200-with-headers (a
	// valid token) and 200-without-headers (anonymous). Necessary because a 401
	// from the middleware would also break the dashboard's fallback login flow.
	r.mux.HandleFunc("/api/forward-auth", r.handleForwardAuth)

	// Claude Code hook sink (VPSM agent-ops #4). Unauthenticated by JWT but gated
	// on loopback + a shared secret in the X-Vpsm-Agent-Secret header (see
	// handleAgentHook). Exact path beats the "/api/" protected catch-all below.
	r.mux.HandleFunc("/api/agent/hook", r.handleAgentHook)

	// Technical report served gated inside the panel (admin/primary). Embedded
	// separately from web/* — see handlers_docs.go. An explicit route beats the
	// "/" catch-all on the ServeMux.
	//
	// It MUST go through auth.Middleware: that is what validates the cookie's
	// JWT and injects the user into the context. mustPrimary reads that context
	// via auth.UserFrom — without the middleware UserFrom returns "" and the
	// handler answers 401 even for the primary user (the /_docs iframe sends the
	// cookie like any other fetch). Do NOT swap it for a bare HandleFunc without
	// reintroducing the gate.
	// Do not remove without updating TestSmokeDocsGated.
	r.mux.Handle("/_docs", r.auth.Middleware(http.HandlerFunc(r.handleDocsReport)))
	// /_graph: a gated viewer for the knowledge base's graph.html files.
	// Same scheme as /_docs — auth.Middleware plus mustPrimary in the handler.
	// Do not remove without updating TestSmokeGraphGated.
	r.mux.Handle("/_graph", r.auth.Middleware(http.HandlerFunc(r.handleKnowledgeGraph)))
	// /android/install: an authenticated page carrying the add-repo QR code for
	// the self-hosted package repository. Same auth.Middleware scheme as /_docs,
	// without mustPrimary — any valid session may install the app on its own
	// device. See handlers_android_install.go.
	// Do not remove without checking TestHandleAndroidInstallPage.
	r.mux.Handle("/android/install", r.auth.Middleware(http.HandlerFunc(r.handleAndroidInstallPage)))
	// /_code: the native editor (code-server) embedded as the "VSCode" sub-tab
	// under Dev. A reverse proxy to the systemd service on 127.0.0.1:8770,
	// gated on the primary user (mustPrimary inside the handler — the editor
	// hands out a root shell). Trailing slash: the ServeMux redirects /_code →
	// /_code/ and then matches everything under the prefix. WebSocket
	// (terminal and editor) works via FlushInterval=-1 on the proxy. See
	// handlers_code.go.
	// Do not remove without updating TestSmokeCodeGated. Protected by an invariant.
	r.mux.Handle("/_code/", r.auth.Middleware(r.codeServerProxy()))
	// #21: port forwarding for the editor — /_port/<n>/ proxies to 127.0.0.1:<n> (primary-only).
	r.mux.Handle("/_port/", r.auth.Middleware(r.portForwardProxy()))
	// PWA: manifest + service worker + icons — public, no auth.
	r.mux.HandleFunc("/manifest.webmanifest", r.handleManifest)
	r.mux.HandleFunc("/sw.js", r.handleServiceWorker)
	r.mux.HandleFunc("/icon-192.png", r.handleIcon)
	r.mux.HandleFunc("/icon-512.png", r.handleIcon)
	r.mux.HandleFunc("/icon-512-maskable.png", r.handleIcon)
	r.mux.HandleFunc("/apple-touch-icon.png", r.handleIcon)
	r.mux.HandleFunc("/apple-touch-icon-152.png", r.handleIcon)
	r.mux.HandleFunc("/apple-touch-icon-167.png", r.handleIcon)
	r.mux.HandleFunc("/apple-touch-icon-precomposed.png", r.handleIcon)

	// Passkey login and registration for the native Android app.
	// PUBLIC on purpose, on the top-level mux (NEVER "protected"): login
	// happens before a session exists, and register/begin+finish are authorised
	// by a single-use ceremony token rather than by a session — see
	// internal/mobilebff/registry_public.go and internal/api/passkey.go.
	// register/finish NEVER issues a session token, even out here outside
	// auth.Middleware: the credential is created pending, and only the
	// authenticated approval screen can activate it.
	mobilebff.MountPublic(r.mux, mobilebff.Deps{Passkey: r})

	// /ws/mobile-events — same reason as /ws/videocall-guest just below: it has
	// its own one-shot ticket auth (HandleMobileEventsWS.ExtractWSAuth), not the
	// generic auth.Middleware of the "protected" mux. r.mobileHub is the
	// connection registry shared between this handler and the mobilebff.Deps.Hub
	// injected into the protected Mount just below — the same Hub, instantiated
	// once.
	r.mobileHub = mobilebff.NewHub()
	r.mux.HandleFunc("/ws/mobile-events", mobilebff.HandleMobileEventsWS(r.auth, r.mobileHub))

	// Videocall — a public entry by PIN (anonymous). The join-by-pin endpoint
	// rate-limits per IP; the guest WebSocket validates the token on its own.
	// Whoever comes in by PIN has NO access to any other panel route.
	if r.videocall != nil {
		r.mux.HandleFunc("/api/videocall/join-by-pin", r.videocall.HandleJoinByPIN)
		r.mux.HandleFunc("/ws/videocall-guest", r.videocall.HandleGuestWS)
		// Public /join page — a guest comes in with a PIN, no account needed.
		r.mux.HandleFunc("/join", r.handleJoinPage)
	}

	protected := http.NewServeMux()

	// Screen-usage telemetry. It sits INSIDE the authenticated group, next to
	// the other /api/* routes: an anonymous route is forbidden by the security
	// standard this project follows. This fork has no CSRF on /api/* and
	// authenticates by Bearer OR by the session cookie (the measurement behind
	// that is in the header of internal/telemetry/handler.go), so the front-end
	// uses fetch keepalive with Bearer and falls back to sendBeacon; the
	// handler accepts both Content-Types.
	//
	// The sink writes append-only JSONL to <DataDir>/telemetry/YYYY-MM-DD.jsonl.
	// DataDir lives in the runtime tree, outside anything version-controlled, so
	// two weeks of deploys erase nothing.
	if ts, err := telemetry.NewSink(filepath.Join(r.cfg.DataDir, "telemetry")); err != nil {
		log.Printf("telemetry: sink unavailable (%v) - /api/telemetry NOT registered", err)
	} else {
		r.telSink = ts
		protected.HandleFunc("/api/telemetry", telemetry.Handler(ts, telemetryFork))
	}

	// System
	protected.HandleFunc("/api/auth/me", r.handleMe)

	// BFF for the native Android app — the only HTTP surface the app calls.
	// Same auth tier as every route above: the "protected" mux, with no
	// exposure of its own outside it.
	// The BFF's idempotency table: exactly ONE, shared by every handler (two
	// tables pointing at the same file would clobber each other on write).
	// Without DataDir it is never created — building it with an empty path
	// would write `mobile-idempotencia.json` into the process's working
	// directory, and a nil here is a clean no-op across the whole table.
	var mobileIdem *mobilebff.Idempotencia
	if r.cfg != nil && strings.TrimSpace(r.cfg.DataDir) != "" {
		mobileIdem = mobilebff.NovaIdempotencia(r.cfg.DataDir)
	}

	mobileDeps := mobilebff.Deps{
		Auth:        r.auth,
		Cfg:         r.cfg,
		Idem:        mobileIdem,
		WhatsAppMgr: r.whatsappMgr,
		SessionOwn:  r.sessionOwn,
		Audit:       r.audit,
		Queue:       r.queue,
		Alerts:      r.alerts,
		Hub:         r.mobileHub,
		// Sessions lets POST /auth/logout (see auth_login.go) revoke by jti in
		// the SAME store the web panel's handleLogout uses — never a second,
		// mobile-only session store.
		Sessions: r.auth.Sessions(),
		// Notify lets GET/PUT /notify/preferences project the Rule catalogue
		// of the SAME Router that the "push" channel consults via
		// r.pushDevicePrefs — see initNotify in notify_wire.go.
		Notify: r.notify,
		// HealthDetailed reuses the SAME aggregation of checks as
		// GET /api/health/detailed (handlers_health.go) — see
		// internal/mobilebff/ops_health.go, which does not re-derive the list
		// of subsystems.
		HealthDetailed: r.healthDetailedSnapshot,
		// SysStats reuses the SAME collectStatsCached that GET /api/stats
		// serves to the web panel (handlers_system.go) — same collection, same
		// 3s cache, same singleflight. The app does not get a second sweep of
		// /proc: when the panel is open, both read the same snapshot. See
		// internal/mobilebff/ops_metrics.go.
		SysStats: collectStatsCached,
		// Videocall lets GET /videocall/rooms (handlers_videocall.go) list the
		// rooms of the SAME *videocall.Service the web panel uses at
		// /api/videocall/rooms — never a second read of rooms.json.
		Videocall: r.videocall,

		// Jira: the app's kanban board (internal/mobilebff/handlers_jira.go)
		// comes out of the SAME per-user client the panel's own
		// handlers_jira.go uses — jiraClientForOwner reads the personal
		// credential from the vault. Jira access was never an admin role here,
		// and still is not.
		JiraFor: r.jiraClientForOwner,
		// JiraConfigFor returns the board's configuration WITHOUT the token:
		// the struct has the field, this path does not fill it in, and no BFF
		// response carries it.
		JiraConfigFor: func(user string) jira.Config {
			u, err := scope.New(user)
			if err != nil || r.secrets == nil {
				return jira.Config{}
			}
			uv := scope.NewUserVault(r.secrets, u)
			return jira.Config{
				Site:         valOf(uv, "jira_site"),
				Email:        valOf(uv, "jira_email"),
				ProjectKey:   valOf(uv, "jira_project"),
				BoardJQL:     valOf(uv, "jira_board_jql"),
				BoardColumns: valOf(uv, "jira_board_columns"),
				HasToken:     valOf(uv, "jira_token") != "",
			}
		},
		// JiraConnect/JiraSetProject write into the SAME per-user vault that
		// handleJiraConfig's POST branch writes to — connecting from the app
		// and connecting from the panel are one account, not two.
		JiraConnect: func(user, site, email, token, project string) error {
			uv, err := r.userVault(user)
			if err != nil {
				return err
			}
			if err := uv.Set("jira_site", site); err != nil {
				return err
			}
			if err := uv.Set("jira_email", email); err != nil {
				return err
			}
			if err := uv.Set("jira_token", token); err != nil {
				return err
			}
			if project != "" {
				return uv.Set("jira_project", project)
			}
			return nil
		},
		JiraSetProject: func(user, project string) error {
			uv, err := r.userVault(user)
			if err != nil {
				return err
			}
			return uv.Set("jira_project", project)
		},
	}
	// Scheduler jobs for the mobile contract: each closure below delegates to
	// the SAME code the HTTP scheduler handlers already use. No authorisation
	// rule and no domain rule is reimplemented here — the data is only
	// reshaped into what the mobile screen layer expects.
	//
	// The registration happens once: `r` is this NewRouter's own Router, so
	// these closures stay bound to the FIRST Router built in the process. In
	// production that is the only Router there is. Tests that need to exercise
	// this behaviour call the screen builder and the action handlers directly,
	// never through a second NewRouter.
	schedulerScreenRegisterOnce.Do(func() {
		screens.Register(screens.SchedulerDeps{
			ListJobs: func(owner string) []*scheduler.Job {
				if r.scheduler == nil {
					return nil
				}
				return r.scheduler.List(owner)
			},
			GetJob: func(id string) (*scheduler.Job, error) {
				if r.scheduler == nil {
					return nil, scheduler.ErrNotFound
				}
				return r.scheduler.Get(id)
			},
			SaveJob: func(in scheduler.Job) (*scheduler.Job, error) {
				if r.scheduler == nil {
					return nil, scheduler.ErrNotFound
				}
				// injectSchedOwner is the same step the panel's POST/PUT handlers
				// already run before Save — see handlers_scheduler.go. in.Owner
				// arrives already decided by the action handler
				// (scheduler_actions.go), never as the value the client sent.
				injectSchedOwner(&in)
				return r.scheduler.Save(in)
			},
			DeleteJob: func(id string) error {
				if r.scheduler == nil {
					return scheduler.ErrNotFound
				}
				return r.scheduler.Delete(id)
			},
			RunNow: func(id string) (string, error) {
				if r.scheduler == nil {
					return "", scheduler.ErrNotFound
				}
				return r.scheduler.RunNow(id)
			},
			NextFires: func(expr string, n int) ([]time.Time, error) {
				if r.scheduler == nil {
					return nil, scheduler.ErrBadInput
				}
				return r.scheduler.NextFires(expr, n)
			},
			// AuthorizedKinds mirrors handleSchedulerCatalog exactly: same
			// source (r.queueRunners), same gate (runner.AuthorizedFor), same
			// Schedulable filter — only the output shape differs.
			AuthorizedKinds: func(user string, isAdmin bool) []screens.KindOption {
				r.cfgMu.Lock()
				kinds := make([]string, 0, len(r.queueRunners))
				for k := range r.queueRunners {
					kinds = append(kinds, k)
				}
				r.cfgMu.Unlock()
				sort.Strings(kinds)

				out := make([]screens.KindOption, 0, len(kinds))
				for _, k := range kinds {
					runner, ok := r.queueRunner(k)
					if !ok {
						continue
					}
					if !runner.AuthorizedFor(user, isAdmin) {
						continue
					}
					d := schedDescriptorFor(k)
					if !d.Schedulable {
						continue
					}
					out = append(out, screens.KindOption{Value: k, Label: d.Label})
				}
				return out
			},
			// AuditEvent writes under the same action vocabulary the web panel
			// already uses (scheduler.create/update/delete/run_now) — httpx.AuditEvent
			// requires an *http.Request for the caller's IP, which an sdui.ActionHandler
			// never receives, so this call writes without an IP instead of forcing that
			// parameter through every action-handler signature in the project.
			AuditEvent: func(user, action, target string) {
				if r.audit == nil {
					return
				}
				r.audit.Append(auth.Event{Time: time.Now().Unix(), User: user, Action: action, Target: target})
			},
		})
	})
	// Docker — each closure below delegates to the SAME *docksvc.Client
	// handlers_docker.go already uses; no domain rule is reimplemented here,
	// only reshaped into the form internal/mobilebff/screens expects (see
	// screens/deps.go). Same rationale for dockerScreenRegisterOnce that
	// schedulerScreenRegisterOnce already documents.
	dockerScreenRegisterOnce.Do(func() {
		resolveComposeWorkingDir := func(ctx context.Context, stack string) (string, error) {
			if r.docker == nil {
				return "", fmt.Errorf("docker unavailable")
			}
			projects, err := r.docker.ListComposeProjects(ctx)
			if err != nil {
				return "", err
			}
			for _, p := range projects {
				if p.Name == stack {
					return p.WorkingDir, nil
				}
			}
			return "", fmt.Errorf("compose stack not found: %s", stack)
		}

		// Every LISTING closure below returns an error when r.docker is nil
		// — never `nil, nil`. An unavailable Docker (docksvc.New failed at
		// start) returning an empty list with HTTP 200 made the app render
		// the table's empty state ("no containers...") instead of the error
		// block: the operator read "Docker answered and there is nothing"
		// when the fact was "I could not reach Docker". Those are two
		// different facts, and the screen can only tell them apart if the
		// transport does — the MUTATION closures beside these already do.
		screens.RegisterDocker(screens.DockerDeps{
			ListContainers: func(ctx context.Context) ([]types.Container, error) {
				if r.docker == nil {
					return nil, fmt.Errorf("docker unavailable")
				}
				return r.docker.ListContainers(ctx)
			},
			StartContainer: func(ctx context.Context, id string) error {
				if r.docker == nil {
					return fmt.Errorf("docker unavailable")
				}
				return r.docker.Start(ctx, id)
			},
			StopContainer: func(ctx context.Context, id string) error {
				if r.docker == nil {
					return fmt.Errorf("docker unavailable")
				}
				return r.docker.Stop(ctx, id)
			},
			RestartContainer: func(ctx context.Context, id string) error {
				if r.docker == nil {
					return fmt.Errorf("docker unavailable")
				}
				return r.docker.Restart(ctx, id)
			},
			RemoveContainer: func(ctx context.Context, id string, force bool) error {
				if r.docker == nil {
					return fmt.Errorf("docker unavailable")
				}
				return r.docker.Remove(ctx, id, force)
			},
			ListImages: func(ctx context.Context) ([]image.Summary, error) {
				if r.docker == nil {
					return nil, fmt.Errorf("docker unavailable")
				}
				return r.docker.Images(ctx)
			},
			RemoveImage: func(ctx context.Context, id string, force bool) error {
				if r.docker == nil {
					return fmt.Errorf("docker unavailable")
				}
				return r.docker.ImageRemove(ctx, id, force)
			},
			// ListVolumes: docker.Client.Volumes returns interface{} (the SDK
			// returns volume.ListResponse underneath) — the same type assertion
			// handleVolumes already makes before sanitizeList.
			ListVolumes: func(ctx context.Context) (volume.ListResponse, error) {
				if r.docker == nil {
					return volume.ListResponse{}, fmt.Errorf("docker unavailable")
				}
				raw, err := r.docker.Volumes(ctx)
				if err != nil {
					return volume.ListResponse{}, err
				}
				lr, ok := raw.(volume.ListResponse)
				if !ok {
					return volume.ListResponse{}, fmt.Errorf("unexpected volumes response type: %T", raw)
				}
				return lr, nil
			},
			ListNetworks: func(ctx context.Context) ([]network.Summary, error) {
				if r.docker == nil {
					return nil, fmt.Errorf("docker unavailable")
				}
				return r.docker.Networks(ctx)
			},
			ListComposeStacks: func(ctx context.Context) ([]docksvc.ComposeProject, error) {
				if r.docker == nil {
					return nil, fmt.Errorf("docker unavailable")
				}
				return r.docker.ListComposeProjects(ctx)
			},
			// ComposeUp/ComposeDown NEVER accept a working_dir from the
			// client — see the RCE note in deps.go. workingDir always comes
			// from the same ListComposeProjects lookup handleComposeAction
			// already uses, never from a field the mobile action's body could
			// carry.
			ComposeUp: func(ctx context.Context, stack string) (string, error) {
				if r.docker == nil {
					return "", fmt.Errorf("docker unavailable")
				}
				wd, err := resolveComposeWorkingDir(ctx, stack)
				if err != nil {
					return "", err
				}
				return r.docker.ComposeAction(stack, wd, "up -d")
			},
			ComposeDown: func(ctx context.Context, stack string) (string, error) {
				if r.docker == nil {
					return "", fmt.Errorf("docker unavailable")
				}
				wd, err := resolveComposeWorkingDir(ctx, stack)
				if err != nil {
					return "", err
				}
				return r.docker.ComposeAction(stack, wd, "down")
			},
			// Prune calls only the individual Prune<Kind> for the selected
			// kinds — never PruneAll, which would run all five
			// unconditionally. Same "<kind>"/"<kind>_error" key convention
			// docker.Client.PruneAll already uses.
			Prune: func(ctx context.Context, kinds []string) (map[string]any, error) {
				if r.docker == nil {
					return nil, fmt.Errorf("docker unavailable")
				}
				result := make(map[string]any, len(kinds))
				for _, k := range kinds {
					var (
						out interface{}
						err error
					)
					switch k {
					case "containers":
						out, err = r.docker.PruneContainers(ctx)
					case "images":
						out, err = r.docker.PruneImages(ctx)
					case "volumes":
						out, err = r.docker.PruneVolumes(ctx)
					case "build_cache":
						out, err = r.docker.PruneBuildCache(ctx)
					default:
						err = fmt.Errorf("unknown cleanup category: %s", k)
					}
					if err != nil {
						result[k+"_error"] = err.Error()
						continue
					}
					result[k] = out
				}
				return result, nil
			},
			AuditEvent: func(user, action, target string) {
				if r.audit == nil {
					return
				}
				r.audit.Append(auth.Event{Time: time.Now().Unix(), User: user, Action: action, Target: target})
			},
		})
	})
	// System — each closure below delegates to the SAME code
	// handlers_system.go/handlers_procs.go already use; no domain rule is
	// reimplemented here, only reshaped into the form
	// internal/mobilebff/screens expects (see screens/deps.go). Same rationale
	// for systemScreenRegisterOnce that dockerScreenRegisterOnce already
	// documents.
	systemScreenRegisterOnce.Do(func() {
		screens.RegisterSystem(screens.SystemDeps{
			// ListHistory mirrors handleHistory: r.ring.Snapshot().
			ListHistory: func() []metrics.Point {
				return r.ring.Snapshot()
			},
			// ListProcesses mirrors handleProcs' open read: procs.List with a
			// zero-value Filter, sorted by CPU, unpaginated (limit=0 falls
			// into procsFilterFromQuery's default, but here it is simplified
			// to "the whole list" — the mobile screen does not paginate).
			ListProcesses: func(ctx context.Context) ([]procs.Info, error) {
				infos, _, err := procs.List(ctx, procs.Filter{}, procs.SortCPU, 0, 0)
				return infos, err
			},
			// KillProcess mirrors ONLY the PRIMARY/admin branch of
			// handleProcsSignal (procs.Signal, never SignalAsOwner) — see
			// KillProcess's doc comment in deps.go for the documented
			// reduction in parity. Always SIGTERM, matching the client's
			// confirmation message.
			KillProcess: func(ctx context.Context, pid int32) error {
				sig, err := procs.SignalByName("SIGTERM")
				if err != nil {
					return err
				}
				return procs.Signal(ctx, pid, sig)
			},
			// ListPorts mirrors handleListening: sysextra.Listening().
			ListPorts: func() ([]sysextra.Port, error) {
				return sysextra.Listening()
			},
			// ListUnits mirrors handleUnits: sysextra.ListUnits().
			ListUnits: func() ([]sysextra.Unit, error) {
				return sysextra.ListUnits()
			},
			// UnitAction mirrors handleUnitAction exactly: the same name
			// validation (validUnitName), the same action allowlist, the same
			// execCmd("systemctl", action, name) — action always arrives as
			// one of the five literal strings system_actions.go registers,
			// never from client input, but the allowlist is replicated here
			// anyway so this stays byte-for-byte identical to the web
			// panel's gate.
			UnitAction: func(_ context.Context, unit, action string) (string, error) {
				if !validUnitName(unit) {
					return "", fmt.Errorf("invalid unit name")
				}
				allowed := map[string]bool{
					"start": true, "stop": true, "restart": true,
					"enable": true, "disable": true, "reload": true,
				}
				if !allowed[action] {
					return "", fmt.Errorf("invalid action")
				}
				return execCmd("systemctl", action, unit)
			},
			// AuditEvent writes under this package's system.* vocabulary
			// (system.process.kill, system.unit.<action>,
			// system.metrics.window) — httpx.AuditEvent requires an
			// *http.Request for the caller's IP, which an sdui.ActionHandler
			// never receives, so this call writes without an IP.
			AuditEvent: func(user, action, target string) {
				if r.audit == nil {
					return
				}
				r.audit.Append(auth.Event{Time: time.Now().Unix(), User: user, Action: action, Target: target})
			},
		})
	})
	// Security — four screens (users/secrets/sessions/audit), all admin-only.
	// Each closure below delegates to the SAME code
	// handlers_users.go/handlers_auth.go/handlers_audit.go already use; no
	// domain rule is reimplemented here, only reshaped into the form
	// internal/mobilebff/screens expects (see screens/deps.go).
	// Same rationale for securityScreenRegisterOnce that
	// systemScreenRegisterOnce already documents.
	securityScreenRegisterOnce.Do(func() {
		configPath := func() string { return filepath.Join(r.cfg.DataDir, "config.json") }

		screens.RegisterSecurity(screens.SecurityDeps{
			// ListUsers mirrors handleUsersList: same primaryName/adminSet
			// via cfg.Admins(), same aggregation of active sessions per user
			// via ListForUser.
			ListUsers: func() []screens.UserRow {
				r.cfgMu.Lock()
				all := r.cfg.AllUsers()
				primaryName := r.cfg.Primary
				adminSet := map[string]bool{}
				for _, name := range r.cfg.Admins() {
					adminSet[name] = true
				}
				r.cfgMu.Unlock()
				sessionsPerUser := map[string]int{}
				if r.auth.Sessions() != nil {
					now := time.Now().Unix()
					for _, u := range all {
						for _, s := range r.auth.Sessions().ListForUser(u.Username) {
							if !s.Revoked && s.ExpiresAt > now {
								sessionsPerUser[s.User]++
							}
						}
					}
				}
				out := make([]screens.UserRow, 0, len(all))
				for _, u := range all {
					out = append(out, screens.UserRow{
						Username:  u.Username,
						IsPrimary: u.Username == primaryName,
						IsAdmin:   adminSet[u.Username],
						HasTOTP:   u.SupabaseMFAEnabled || u.TOTPSecret != "",
						Sessions:  sessionsPerUser[u.Username],
					})
				}
				return out
			},
			// SaveUser covers both create (handleUserCreate) and edit
			// (handleUserSetAdmin, the role only) — see UserInput's doc
			// comment in deps.go for the branching and for why a password
			// reset is a separate action (ResetPassword below).
			// Username and password validation is the SAME as
			// handleUserCreate's, character for character.
			SaveUser: func(in screens.UserInput) (*screens.UserRow, error) {
				username := strings.TrimSpace(in.Username)
				if username == "" || len(username) > 40 {
					return nil, fmt.Errorf("invalid username (1-40 chars)")
				}
				for _, c := range username {
					if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-') {
						return nil, fmt.Errorf("username may only contain letters, digits, _ and -")
					}
				}

				r.cfgMu.Lock()
				exists := r.cfg.HasUser(username)
				if !exists {
					if len(in.Password) < 8 {
						r.cfgMu.Unlock()
						return nil, fmt.Errorf("password must be at least 8 characters")
					}
					hash, err := auth.HashPassword(in.Password)
					if err != nil {
						r.cfgMu.Unlock()
						return nil, fmt.Errorf("bcrypt: %w", err)
					}
					if err := r.cfg.AddUser(username, hash); err != nil {
						r.cfgMu.Unlock()
						return nil, err
					}
				}
				// Admin is always applied through SetAdmin — never by writing
				// the field directly — so that promoting a freshly created user
				// and demoting an existing one both go through the SAME
				// protection of the primary and the last admin that SetAdmin
				// already applies (see UserInput's doc comment in deps.go).
				if err := r.cfg.SetAdmin(username, in.Admin); err != nil {
					r.cfgMu.Unlock()
					return nil, err
				}
				saveErr := config.Save(r.cfg, configPath())
				r.auth.ReloadUsers(credsFromConfig(r.cfg))
				primaryName := r.cfg.Primary
				isAdmin := r.cfg.IsAdmin(username)
				var totp bool
				for _, u := range r.cfg.AllUsers() {
					if u.Username == username {
						totp = u.SupabaseMFAEnabled || u.TOTPSecret != ""
						break
					}
				}
				r.cfgMu.Unlock()
				if saveErr != nil {
					return nil, fmt.Errorf("save config: %w", saveErr)
				}
				// WhatsApp provisioning happens on create only — the same
				// condition as handleUserCreate. An error is logged, it does not
				// fail the save (the config has already been persisted).
				if !exists && r.whatsappMgr != nil {
					if su, err := scope.New(username); err == nil {
						if err := r.whatsappMgr.Provision(su); err != nil {
							log.Printf("security.user.save whatsapp provision %s: %v", su, err)
						}
					}
				}
				var sessionCount int
				if r.auth.Sessions() != nil {
					now := time.Now().Unix()
					for _, s := range r.auth.Sessions().ListForUser(username) {
						if !s.Revoked && s.ExpiresAt > now {
							sessionCount++
						}
					}
				}
				return &screens.UserRow{
					Username:  username,
					IsPrimary: username == primaryName,
					IsAdmin:   isAdmin,
					HasTOTP:   totp,
					Sessions:  sessionCount,
				}, nil
			},
			// DeleteUser mirrors handleUserDelete's domain path — the
			// self-delete guard and the primary guard live in
			// security_actions.go, BEFORE this closure is called (see
			// SecurityDeps.DeleteUser's doc comment in deps.go).
			DeleteUser: func(username string) error {
				if r.whatsappMgr != nil {
					if su, err := scope.New(username); err == nil {
						if err := r.whatsappMgr.Decommission(su); err != nil {
							log.Printf("security.user.delete whatsapp decommission %s: %v", su, err)
						}
					}
				}
				r.cfgMu.Lock()
				if err := r.cfg.RemoveUser(username); err != nil {
					r.cfgMu.Unlock()
					return err
				}
				saveErr := config.Save(r.cfg, configPath())
				r.auth.ReloadUsers(credsFromConfig(r.cfg))
				r.cfgMu.Unlock()
				if saveErr != nil {
					return fmt.Errorf("save config: %w", saveErr)
				}
				if r.auth.Sessions() != nil {
					r.auth.Sessions().RevokeAllExcept(username, "")
				}
				if um := r.auth.UUIDMap(); um != nil {
					if um.Remove(username) {
						if err := auth.SaveUUIDMap(um, filepath.Join(r.cfg.DataDir, "migration-uuid-map.json")); err != nil {
							log.Printf("security.user.delete: uuid_map save failed: %v", err)
						}
					}
				}
				return nil
			},
			// ResetPassword mirrors handleUserResetPassword exactly: same
			// length validation, same SetPassword+Save+ReloadUsers.
			// A SEPARATE action from SaveUser (see UserInput's doc comment).
			ResetPassword: func(username, password string) error {
				if username == "" || len(password) < 8 {
					return fmt.Errorf("empty username or password < 8 chars")
				}
				hash, err := auth.HashPassword(password)
				if err != nil {
					return fmt.Errorf("bcrypt: %w", err)
				}
				r.cfgMu.Lock()
				updated := r.cfg.SetPassword(username, hash)
				var saveErr error
				if updated {
					saveErr = config.Save(r.cfg, configPath())
					r.auth.ReloadUsers(credsFromConfig(r.cfg))
				}
				r.cfgMu.Unlock()
				if !updated {
					return fmt.Errorf("user not found")
				}
				if saveErr != nil {
					return fmt.Errorf("save config: %w", saveErr)
				}
				return nil
			},
			// ListSecretKeys/SetSecret/DeleteSecret mirror
			// internal/secrets.Store — they never expose a value (see
			// SecretKeyRow's doc comment in deps.go).
			ListSecretKeys: func() []screens.SecretKeyRow {
				if r.secrets == nil {
					return nil
				}
				keys := r.secrets.List()
				out := make([]screens.SecretKeyRow, 0, len(keys))
				for _, k := range keys {
					out = append(out, screens.SecretKeyRow{Key: k})
				}
				return out
			},
			SetSecret: func(key, value string) error {
				if r.secrets == nil {
					return fmt.Errorf("secrets vault unavailable")
				}
				return r.secrets.Set(key, value)
			},
			DeleteSecret: func(key string) error {
				if r.secrets == nil {
					return fmt.Errorf("secrets vault unavailable")
				}
				return r.secrets.Delete(key)
			},
			// ListSessions aggregates sessions.Store.ListForUser across all of
			// cfg.Users — the SAME composition handleUsersList already uses to
			// count sessions, only returning the whole row instead of just the
			// count. IsCurrent is never set here: this closure has no access to
			// the current request's jti — security.go resolves IsCurrent in the
			// rows route's handler, which DOES receive the
			// *http.Request.
			ListSessions: func() []screens.SessionRow {
				if r.auth.Sessions() == nil {
					return nil
				}
				r.cfgMu.Lock()
				all := r.cfg.AllUsers()
				r.cfgMu.Unlock()
				out := make([]screens.SessionRow, 0, len(all))
				for _, u := range all {
					for _, s := range r.auth.Sessions().ListForUser(u.Username) {
						if s.Revoked {
							continue
						}
						out = append(out, screens.SessionRow{
							ID:        s.JTI,
							User:      s.User,
							IP:        s.IP,
							UserAgent: s.UserAgent,
							IssuedAt:  s.IssuedAt,
							LastSeen:  s.LastSeen,
							ExpiresAt: s.ExpiresAt,
						})
					}
				}
				return out
			},
			// RevokeSession mirrors sessions.Store.Revoke — a real tombstone
			// write, checked by auth's Touch/HasTombstone on the next
			// authenticated request from that session, not a cosmetic row
			// removal.
			RevokeSession: func(sessionID string) error {
				if r.auth.Sessions() == nil {
					return fmt.Errorf("sessions unavailable")
				}
				r.auth.Sessions().Revoke(sessionID)
				return nil
			},
			// ListAuditEvents returns SYSTEM-WIDE events (auth.AuditLog.Tail,
			// with no TenantScope) — security.audit is an admin-only screen in
			// its entirety, so there is no per-user filtering to apply (see
			// AuditRow's doc comment in deps.go).
			ListAuditEvents: func(filter screens.AuditFilter) ([]screens.AuditRow, error) {
				// A missing audit log is an ERROR, not an empty list: a
				// security record that could not be opened must not reach the
				// screen wearing the same face as "nothing happened".
				if r.audit == nil {
					return nil, fmt.Errorf("audit unavailable")
				}
				limit := filter.Limit
				if limit <= 0 || limit > 1000 {
					limit = 200
				}
				events := r.audit.Tail(limit)
				out := make([]screens.AuditRow, 0, len(events))
				for _, e := range events {
					out = append(out, screens.AuditRow{
						Time:   e.Time,
						User:   e.User,
						Action: e.Action,
						Target: e.Target,
						IP:     e.IP,
					})
				}
				return out, nil
			},
			AuditEvent: func(user, action, target string) {
				if r.audit == nil {
					return
				}
				r.audit.Append(auth.Event{Time: time.Now().Unix(), User: user, Action: action, Target: target})
			},
		})
	})
	// Network — four screens (ufw/adguard/devices/economia), registered under
	// the "security." id prefix even though the seam is a struct of its own
	// (NetworkDeps) — see the NetworkDeps doc comment in deps.go. Each closure
	// below delegates to the SAME code handlers_system.go (UFW),
	// handlers_adguard.go, handlers_singbox.go and internal/netusage already
	// use.
	networkScreenRegisterOnce.Do(func() {
		screens.RegisterNetwork(screens.NetworkDeps{
			// UFWStatus mirrors handleUFW's GET branch. Unlike the web panel
			// (which returns installed:false with a 200 when ufw is not
			// installed), this closure returns execCmd's error directly —
			// security.go decides how to show "not installed" from it.
			UFWStatus: func() (bool, string, error) {
				out, err := execCmd("ufw", "status", "numbered")
				if err != nil {
					return false, out, err
				}
				return strings.Contains(out, "Status: active"), out, nil
			},
			// UFWApplyRule mirrors handleUFWRule's action→args mapping byte
			// for byte (the mustPrimary gate lives in security_actions.go).
			UFWApplyRule: func(action, spec string) (string, error) {
				var args []string
				switch action {
				case "enable":
					args = []string{"--force", "enable"}
				case "disable":
					args = []string{"disable"}
				case "allow", "deny", "reject":
					if spec == "" {
						return "", fmt.Errorf("spec required")
					}
					args = append([]string{action}, strings.Fields(spec)...)
				case "delete":
					if spec == "" {
						return "", fmt.Errorf("spec required (rule number or full rule)")
					}
					args = append([]string{"--force", "delete"}, strings.Fields(spec)...)
				default:
					return "", fmt.Errorf("invalid action")
				}
				return execCmd("ufw", args...)
			},
			AdGuardStatus: func(ctx context.Context) (*adguard.Status, error) {
				cli, ok := r.adguardClient()
				if !ok {
					return nil, fmt.Errorf("AdGuard credentials not found (set the %s and %s secrets)", adguardUserSecret, adguardPassSecret)
				}
				return cli.Status(ctx)
			},
			AdGuardSetProtection: func(ctx context.Context, enabled bool, durationMs int) error {
				cli, ok := r.adguardClient()
				if !ok {
					return fmt.Errorf("AdGuard credentials not found (set the %s and %s secrets)", adguardUserSecret, adguardPassSecret)
				}
				_, err := cli.SetProtection(ctx, enabled, durationMs)
				return err
			},
			ListDevices: func() ([]screens.DeviceRow, error) {
				devs, err := r.singboxManager().List()
				if err != nil {
					return nil, err
				}
				out := make([]screens.DeviceRow, 0, len(devs))
				for _, d := range devs {
					out = append(out, screens.DeviceRow{Name: d.Name, UUID: d.UUID, Exit: d.Exit, Datasaver: d.Datasaver, Created: d.Created})
				}
				return out, nil
			},
			AddDevice: func(ctx context.Context, name string) (screens.DeviceRow, error) {
				d, err := r.singboxManager().Add(ctx, name)
				if err != nil {
					return screens.DeviceRow{}, err
				}
				return screens.DeviceRow{Name: d.Name, UUID: d.UUID, Exit: d.Exit, Datasaver: d.Datasaver, Created: d.Created}, nil
			},
			RemoveDevice: func(ctx context.Context, uuid string) error {
				return r.singboxManager().Remove(ctx, uuid)
			},
			SetDeviceExit: func(ctx context.Context, uuid, exit string) error {
				return r.singboxManager().SetExit(ctx, uuid, exit)
			},
			SetDeviceDatasaver: func(ctx context.Context, uuid string, on bool) error {
				return r.singboxManager().SetDatasaver(ctx, uuid, on)
			},
			// ProbeDatasaverHealthy is literally probeDatasaverProxy — see
			// the doc comment on NetworkDeps.ProbeDatasaverHealthy in deps.go
			// for why this seam exists (probeDatasaverProxy is a PRIVATE
			// method on *Router, unreachable from internal/mobilebff/screens).
			ProbeDatasaverHealthy: func(ctx context.Context, exit string) (string, error) {
				return r.probeDatasaverProxy(ctx, exit)
			},
			RenameDevice: func(ctx context.Context, uuid, newName string) error {
				return r.singboxManager().Rename(ctx, uuid, newName)
			},
			DeviceLink: func(uuid string) (string, error) {
				devs, err := r.singboxManager().List()
				if err != nil {
					return "", err
				}
				for _, d := range devs {
					if d.UUID == uuid {
						return r.singboxManager().Link(d)
					}
				}
				return "", fmt.Errorf("device not found")
			},
			UsageSnapshot: func() ([]screens.UsageRow, error) {
				// Same reason as ListAuditEvents: a meter that is down must not
				// look like a tunnel nobody is using.
				if r.netUsage == nil {
					return nil, fmt.Errorf("usage metering unavailable")
				}
				snap := r.netUsage.Snapshot()
				out := make([]screens.UsageRow, 0, len(snap))
				for _, u := range snap {
					out = append(out, screens.UsageRow{Name: u.Name, Port: u.Port, TotalBytes: u.TotalBytes, RateBps: u.RateBps, ActiveConns: u.ActiveConns})
				}
				return out, nil
			},
			AuditEvent: func(user, action, target string) {
				if r.audit == nil {
					return
				}
				r.audit.Append(auth.Event{Time: time.Now().Unix(), User: user, Action: action, Target: target})
			},
		})
	})
	// miscScreenRegisterOnce.Do wires ai.settings/jira.issues/deploy.apps/
	// queue.jobs (Plano 08-06). deploy.apps/queue.jobs manage
	// internal/deploy's PaaS app catalog and internal/queue's generic job
	// queue — never Phase 6's self-deploy trigger (ops_deploy.go's
	// POST /ops/deploy) or its status surface (ops_health.go's
	// GET /ops/status), a sibling, non-overlapping mechanism.
	miscScreenRegisterOnce.Do(func() {
		screens.RegisterMisc(screens.MiscDeps{
			// AIModelsConfig/SaveAIModels mirror handleAIModelsConfig
			// (handlers_ai_models.go) exactly — same admin-only config,
			// never a secret (config.AIModels carries only model-tier
			// strings).
			AIModelsConfig: func() (config.AIModels, map[string]string) {
				r.cfgMu.Lock()
				cur := r.cfg.AIModels
				r.cfgMu.Unlock()
				return cur, map[string]string{
					"suggest": aimodel.For(aimodel.Suggest, cur.Suggest),
					"jira_ai": aimodel.For(aimodel.JiraAI, cur.JiraAI),
				}
			},
			SaveAIModels: func(m config.AIModels) error {
				r.cfgMu.Lock()
				defer r.cfgMu.Unlock()
				r.cfg.AIModels = m
				return config.Save(r.cfg, filepath.Join(r.cfg.DataDir, "config.json"))
			},

			// JiraStatus/JiraConnect mirror jiraClientFor/handleJiraConfig's
			// vault reads (handlers_jira.go) — the token is written but
			// NEVER read back into a return value here.
			JiraStatus: func(user string) (bool, string) {
				u, err := scope.New(user)
				if err != nil || r.secrets == nil {
					return false, ""
				}
				uv := scope.NewUserVault(r.secrets, u)
				return valOf(uv, "jira_token") != "", valOf(uv, "jira_project")
			},
			JiraConnect: func(user, site, email, token, project string) error {
				u, err := scope.New(user)
				if err != nil {
					return err
				}
				if r.secrets == nil {
					return fmt.Errorf("vault unavailable")
				}
				uv := scope.NewUserVault(r.secrets, u)
				if err := uv.Set("jira_site", site); err != nil {
					return err
				}
				if err := uv.Set("jira_email", email); err != nil {
					return err
				}
				if err := uv.Set("jira_token", token); err != nil {
					return err
				}
				if project != "" {
					if err := uv.Set("jira_project", project); err != nil {
						return err
					}
				}
				return nil
			},
			JiraListIssues: func(ctx context.Context, user, jql string) ([]jira.Issue, error) {
				cli, err := r.jiraClientForOwner(user)
				if err != nil {
					return nil, err
				}
				issues, _, err := cli.Search(ctx, jql, 0, 50)
				return issues, err
			},
			JiraGetIssue: func(ctx context.Context, user, key string) (*jira.IssueDetail, error) {
				cli, err := r.jiraClientForOwner(user)
				if err != nil {
					return nil, err
				}
				return cli.GetIssue(ctx, key)
			},
			JiraTransitions: func(ctx context.Context, user, key string) ([]jira.Transition, error) {
				cli, err := r.jiraClientForOwner(user)
				if err != nil {
					return nil, err
				}
				return cli.Transitions(ctx, key)
			},
			JiraTransition: func(ctx context.Context, user, key, transitionID string) error {
				cli, err := r.jiraClientForOwner(user)
				if err != nil {
					return err
				}
				return cli.Transition(ctx, key, transitionID)
			},
			JiraAddComment: func(ctx context.Context, user, key, text string) (*jira.Comment, error) {
				cli, err := r.jiraClientForOwner(user)
				if err != nil {
					return nil, err
				}
				return cli.AddComment(ctx, key, text)
			},

			// ListDeployApps/GetDeployApp/CreateDeployApp/DestroyDeployApp
			// mirror handleDeployApps/handleDeployApp/handleDeployDestroy
			// (handlers_deploy.go) exactly — internal/deploy's PaaS app
			// catalog, an arbitrary OTHER app this VPS hosts.
			ListDeployApps: func() ([]deploy.App, error) {
				if r.deployStore == nil {
					return nil, fmt.Errorf("deploy subsystem unavailable")
				}
				return r.deployStore.List()
			},
			GetDeployApp: func(name string) (deploy.App, bool, error) {
				if r.deployStore == nil {
					return deploy.App{}, false, fmt.Errorf("deploy subsystem unavailable")
				}
				return r.deployStore.Get(name)
			},
			CreateDeployApp: func(a deploy.App) (deploy.App, error) {
				if r.deployStore == nil {
					return deploy.App{}, fmt.Errorf("deploy subsystem unavailable")
				}
				// deploy-app-create-form (misc.go) has no autodeploy field —
				// same default as handleDeployApps when body.Autodeploy arrives
				// nil: true.
				a.Autodeploy = true
				return r.deployStore.Create(a)
			},
			// TriggerRedeploy mirrors handleDeployTrigger's UI-trigger branch
			// (no ref/commit override — redeploys the app's own configured
			// branch) via the SAME "app_deploy" queue kind enqueueDeploy
			// itself uses. Never internal/mobilebff's self-deploy job.
			TriggerRedeploy: func(user, name string) (string, error) {
				if r.deployStore == nil {
					return "", fmt.Errorf("deploy subsystem unavailable")
				}
				if r.queue == nil {
					return "", fmt.Errorf("queue unavailable")
				}
				app, found, err := r.deployStore.Get(name)
				if err != nil || !found {
					return "", fmt.Errorf("app does not exist")
				}
				spec := deploy.Spec{App: app.Name, Trigger: "ui", DeployID: deploy.NewID()}
				args, _ := json.Marshal(map[string]any{
					"app": spec.App, "ref": spec.Ref, "commit": spec.Commit,
					"preview": spec.Preview, "trigger": spec.Trigger, "deploy_id": spec.DeployID,
				})
				job, err := r.queue.Enqueue("app_deploy", args, user, "deploy-ui")
				if err != nil {
					return "", err
				}
				return job.ID, nil
			},
			DestroyDeployApp: func(ctx context.Context, name string) error {
				if r.deployStore == nil {
					return fmt.Errorf("deploy subsystem unavailable")
				}
				return r.deployStore.Destroy(ctx, name, io.Discard)
			},

			// ListQueueJobs/GetQueueJob/CancelQueueJob/RerunQueueJob/
			// AuthorizedForRerun mirror handleQueue/handleQueueByID
			// (handlers_queue.go) exactly — internal/queue's generic job
			// catalogue, which the deploy screen's own app_deploy jobs also
			// flow through like any other job, with no special-casing.
			ListQueueJobs: func(owner string) []*queue.Job {
				if r.queue == nil {
					return nil
				}
				return r.queue.List(owner, "", 200)
			},
			GetQueueJob: func(id string) (*queue.Job, error) {
				if r.queue == nil {
					return nil, fmt.Errorf("queue unavailable")
				}
				return r.queue.Get(id)
			},
			CancelQueueJob: func(id string) error {
				if r.queue == nil {
					return fmt.Errorf("queue unavailable")
				}
				return r.queue.Cancel(id)
			},
			RerunQueueJob: func(id string) (*queue.Job, error) {
				if r.queue == nil {
					return nil, fmt.Errorf("queue unavailable")
				}
				return r.queue.Rerun(id)
			},
			AuthorizedForRerun: func(user string, isAdmin bool, kind string) bool {
				runner, ok := r.queueRunner(kind)
				if !ok {
					return false
				}
				return runner.AuthorizedFor(user, isAdmin)
			},

			AuditEvent: func(user, action, target string) {
				if r.audit == nil {
					return
				}
				r.audit.Append(auth.Event{Time: time.Now().Unix(), User: user, Action: action, Target: target})
			},
		})
	})
	// alertsScreenRegisterOnce.Do wires alerts.rules (plan
	// 08-07) — every closure below adapts internal/notify.Router (Rules/
	// UpsertRule/DeleteRule/ChannelDefsRedacted), the SAME engine
	// handleNotifyRules/handleNotifyRuleDelete (handlers_notify.go) already
	// use for the panel's own Alertas tab, and notifyEventCatalog
	// (handlers_notify.go) for the condition catalog — never a
	// reimplementation of rule storage or matching. r.notify may be nil
	// (notify.New failed at boot, see notify_wire.go's initNotify), so every
	// closure degrades to an empty/no-op result exactly like every other
	// r.audit-guarded closure above.
	alertsScreenRegisterOnce.Do(func() {
		screens.RegisterAlerts(screens.AlertsDeps{
			ListAlertRules: func(_ sdui.Viewer) []screens.AlertRuleRow {
				if r.notify == nil {
					return nil
				}
				rules := r.notify.Rules()
				out := make([]screens.AlertRuleRow, 0, len(rules))
				for _, rl := range rules {
					out = append(out, screens.AlertRuleRow{
						ID: rl.ID, Name: rl.Name, Enabled: rl.Enabled,
						TypePrefix: rl.TypePrefix, MinSeverity: rl.MinSeverity, Channels: rl.Channels,
					})
				}
				return out
			},
			ChannelOptions: func() []screens.ChannelOption {
				if r.notify == nil {
					return nil
				}
				defs := r.notify.ChannelDefsRedacted()
				out := make([]screens.ChannelOption, 0, len(defs))
				for _, d := range defs {
					out = append(out, screens.ChannelOption{Value: d.ID, Label: d.Name})
				}
				return out
			},
			EventOptions: func() []screens.EventOption {
				out := make([]screens.EventOption, 0, len(notifyEventCatalog))
				for _, e := range notifyEventCatalog {
					out = append(out, screens.EventOption{Value: e["type_prefix"], Label: e["label"]})
				}
				return out
			},
			SaveAlertRule: func(in screens.AlertRuleInput) (*screens.AlertRuleRow, error) {
				if r.notify == nil {
					return nil, fmt.Errorf("alerts unavailable")
				}
				saved, err := r.notify.UpsertRule(notify.Rule{
					ID: in.ID, Name: in.Name, Enabled: in.Enabled,
					TypePrefix: in.TypePrefix, MinSeverity: in.MinSeverity, Channels: in.Channels,
				})
				if err != nil {
					return nil, err
				}
				return &screens.AlertRuleRow{
					ID: saved.ID, Name: saved.Name, Enabled: saved.Enabled,
					TypePrefix: saved.TypePrefix, MinSeverity: saved.MinSeverity, Channels: saved.Channels,
				}, nil
			},
			DeleteAlertRule: func(id string) error {
				if r.notify == nil {
					return fmt.Errorf("alerts unavailable")
				}
				return r.notify.DeleteRule(id)
			},
			AuditEvent: func(user, action, target string) {
				if r.audit == nil {
					return
				}
				r.audit.Append(auth.Event{Time: time.Now().Unix(), User: user, Action: action, Target: target})
			},
		})
	})
	mobilebff.Mount(protected, mobileDeps)
	// Publishes a snapshot of OpsStatus on the "ops.health" channel only while
	// somebody is subscribed to it (see StartOpsHealthPublisher).
	// Closed in Shutdown.
	r.mobileOpsHealthStop = mobilebff.StartOpsHealthPublisher(mobileDeps)

	// Games: game servers (generic and multi-title; one adapter per title).
	//
	// The migration runs BEFORE New: New reads the inventory at construction
	// time, and migrating afterwards would leave the Manager holding the old
	// version in memory until the next Reload — a window in which the panel
	// would believe no server has a node.
	// It is idempotent and a no-op when the file does not exist, so the cost on
	// every boot is a single read.
	if _, err := gameservers.MigrarInventarioParaNo(filepath.Join(r.cfg.DataDir, "gameservers.json")); err != nil {
		log.Printf("game inventory migration failed (the panel still comes up, but the servers will have no node): %v", err)
	}
	r.gameMgr = gameservers.New(r.cfg.DataDir, r.docker)
	protected.HandleFunc("/api/gameservers", r.handleGameServers)
	protected.HandleFunc("/api/gameservers/_inventory", r.handleGameInventory)
	protected.HandleFunc("/api/gameservers/", r.handleGameServerSub)
	protected.HandleFunc("/api/auth/refresh", r.handleRefresh)
	protected.HandleFunc("/api/auth/logout", r.handleLogout)
	protected.HandleFunc("/api/auth/ws-ticket", r.handleWSTicket)
	protected.HandleFunc("/api/auth/mobile-pair", r.handleMobilePairStart)
	protected.HandleFunc("/api/auth/sessions", r.handleSessionsList)
	protected.HandleFunc("/api/auth/sessions/revoke", r.handleSessionRevoke)
	protected.HandleFunc("/api/auth/sessions/revoke-all", r.handleSessionRevokeAll)
	// Paired devices (the Android app's passkeys). This is the approval side of
	// pairing by QR code; see the top-of-section docstring for "Dispositivos
	// pareados" in handlers_auth.go.
	protected.HandleFunc("/api/auth/mobile-sessions", r.handleListMobileSessions)
	protected.HandleFunc("/api/auth/mobile-sessions/approve", r.handleApproveMobileCredential)
	protected.HandleFunc("/api/auth/mobile-sessions/deny", r.handleDenyMobileCredential)
	protected.HandleFunc("/api/auth/mobile-sessions/revoke", r.handleRevokeMobileSession)
	protected.HandleFunc("/api/auth/change-password", r.handleChangePassword)
	protected.HandleFunc("/api/auth/totp/status", r.handleTOTPStatus)
	protected.HandleFunc("/api/auth/totp/enroll", r.handleTOTPEnroll)
	protected.HandleFunc("/api/auth/totp/confirm", r.handleTOTPConfirm)
	// Phase 4: MFA TOTP via Supabase GoTrue
	protected.HandleFunc("/api/auth/mfa/enroll-start", r.handleMFAEnrollStart)
	protected.HandleFunc("/api/auth/mfa/enroll-verify", r.handleMFAEnrollVerify)
	protected.HandleFunc("/api/auth/mfa/disable", r.handleMFADisable)
	protected.HandleFunc("/api/auth/mfa/status", r.handleMFAStatus)
	protected.HandleFunc("/api/auth/totp/disable", r.handleTOTPDisable)
	protected.HandleFunc("/api/system/stats", r.handleStats)
	protected.HandleFunc("/api/system/history", r.handleHistory)
	protected.HandleFunc("/api/system/listening", r.handleListening)
	protected.HandleFunc("/api/system/connections", r.handleConnections)
	protected.HandleFunc("/api/system/units", r.handleUnits)
	protected.HandleFunc("/api/system/unit-status", r.handleUnitStatus)
	protected.HandleFunc("/api/system/journal", r.handleJournal)
	protected.HandleFunc("/api/system/unit-restart", r.handleUnitRestart)
	protected.HandleFunc("/api/system/unit-action", r.handleUnitAction)
	protected.HandleFunc("/api/system/apt", r.handleApt)
	protected.HandleFunc("/api/system/reboot", r.handleReboot)
	protected.HandleFunc("/api/system/logs", r.handleSystemLogs)
	protected.HandleFunc("/ws/system/log-tail", r.handleSystemLogTail)
	protected.HandleFunc("/api/system/ufw", r.handleUFW)
	protected.HandleFunc("/api/system/ufw-rule", r.handleUFWRule)
	protected.HandleFunc("/api/system/cron", r.handleCron)

	// Process Manager (htop web) — see internal/procs + handlers_procs.go
	protected.HandleFunc("/api/procs", r.handleProcs)
	protected.HandleFunc("/api/procs/signal", r.handleProcsSignal)
	protected.HandleFunc("/ws/procs", r.handleProcsStream)

	// Maintenance TODOs — see internal/todos + handlers_todos.go
	protected.HandleFunc("/api/todos", r.handleTodos)
	protected.HandleFunc("/api/todos/", r.handleTodoByID)
	protected.HandleFunc("/api/todos/seed", r.handleTodosSeed)

	// Background Jobs Queue (F3) — see internal/queue + handlers_queue.go
	protected.HandleFunc("/api/queue", r.handleQueue)
	protected.HandleFunc("/api/queue/", r.handleQueueByID)
	protected.HandleFunc("/ws/queue/", r.handleQueueWS)

	// Job Scheduler (F2) — see internal/scheduler + handlers_scheduler.go
	protected.HandleFunc("/api/scheduler/jobs", r.handleSchedulerJobs)
	protected.HandleFunc("/api/scheduler/jobs/", r.handleSchedulerJobByID)
	protected.HandleFunc("/api/scheduler/preview", r.handleSchedulerPreview)
	protected.HandleFunc("/api/scheduler/catalog", r.handleSchedulerCatalog)                         // schedulable kinds filtered by authz
	protected.HandleFunc("/api/scheduler/options", r.handleSchedulerOptions)                         // dynamic dropdown options (units/containers/compose/images)
	protected.HandleFunc("/api/fs/browse", r.handleFSBrowse)                                         // navegador de pastas da VPS
	protected.HandleFunc("/api/backup/remotes", r.handleBackupRemotes)                               // remotes rclone (nuvem)
	protected.HandleFunc("/api/backup/remote-browse", r.handleBackupRemoteBrowse)                    // navegar pastas do remote
	protected.HandleFunc("/api/backup/remote-connect", r.handleBackupRemoteConnect)                  // conectar nuvem (cria remote rclone)
	protected.HandleFunc("/api/backup/remote-authorize", r.handleBackupRemoteAuthorize)              // inicia OAuth in-app
	protected.HandleFunc("/api/backup/remote-authorize/status", r.handleBackupRemoteAuthorizeStatus) // status do OAuth in-app

	// Jira Cloud kanban (J1+J2) — see internal/jira + handlers_jira{,_extra}.go
	protected.HandleFunc("/api/jira/config", r.handleJiraConfig)
	protected.HandleFunc("/api/jira/health", r.handleJiraHealth)
	protected.HandleFunc("/api/jira/projects", r.handleJiraProjects)
	protected.HandleFunc("/api/jira/issuetypes", r.handleJiraIssueTypes)
	protected.HandleFunc("/api/jira/board", r.handleJiraBoard)
	protected.HandleFunc("/api/jira/users", r.handleJiraUsers)
	protected.HandleFunc("/api/jira/issue", r.handleJiraIssue)
	protected.HandleFunc("/api/jira/issue/", r.handleJiraIssue)
	protected.HandleFunc("/api/jira/picker", r.handleJiraPicker)
	protected.HandleFunc("/api/jira/priorities", r.handleJiraPriorities)
	protected.HandleFunc("/api/jira/linktypes", r.handleJiraLinkTypes)
	protected.HandleFunc("/api/jira/issuelink", r.handleJiraIssueLinkCreate)
	protected.HandleFunc("/api/jira/issuelink/", r.handleJiraIssueLinkDelete)
	protected.HandleFunc("/api/jira/avatar", r.handleJiraAvatar)
	protected.HandleFunc("/api/jira/attachment/", r.handleJiraAttachment)
	protected.HandleFunc("/api/jira/project/", r.handleJiraProjectMeta)
	protected.HandleFunc("/api/jira/confluence/spaces", r.handleJiraConfluenceSpaces)
	protected.HandleFunc("/api/jira/confluence/pages", r.handleJiraConfluencePages)
	protected.HandleFunc("/api/jira/confluence/page/", r.handleJiraConfluencePage)
	// silence unused-import linter if any path falls through
	_ = jira.ErrNotConfigured

	protected.HandleFunc("/api/docker/compose/create", r.handleComposeCreate)

	// Docker
	protected.HandleFunc("/api/docker/info", r.handleDockerInfo)
	protected.HandleFunc("/api/docker/disk-usage", r.handleDiskUsage)
	protected.HandleFunc("/api/docker/containers", r.handleContainers)
	protected.HandleFunc("/api/docker/containers/", r.handleContainerAction)
	protected.HandleFunc("/api/docker/images", r.handleImages)
	protected.HandleFunc("/api/docker/volumes", r.handleVolumes)
	protected.HandleFunc("/api/docker/networks", r.handleNetworks)
	protected.HandleFunc("/api/docker/compose", r.handleCompose)
	protected.HandleFunc("/api/docker/compose/action", r.handleComposeAction)
	// #33-#38: Heroku-style PaaS (git-push deploy). All primary-only.
	// Multi-node inventory. The subroutes land on the same handler because a
	// guest's ID contains a slash ("lxc/207") and the ServeMux cannot split that.
	protected.HandleFunc("/api/nodes", r.handleNodes)
	protected.HandleFunc("/api/nodes/", r.handleNodes)
	// Proxmox tab. Same reason as the two lines above: the snapshot route takes
	// the guest ID ("lxc/207") as a parameter, and the subroutes (tasks,
	// tasks/log, disks, permissions) land on the same handler.
	protected.HandleFunc("/api/proxmox", r.handleProxmox)
	protected.HandleFunc("/api/proxmox/", r.handleProxmox)
	// Remote console for a guest. It goes on `protected` and NOT on r.mux:
	// unlike /ws/stt/transcribe, which is public on purpose and validates three
	// token types by hand, this endpoint hands out a SHELL. It requires the
	// panel session via auth.Middleware plus the primary-user gate inside the
	// handler, and it refuses `?token=` in the URL, because a query string ends
	// up in access logs and would become a replayable shell credential.
	protected.HandleFunc("/ws/proxmox/console", r.handleProxmoxConsole)

	protected.HandleFunc("/api/deploy/apps", r.handleDeployApps)
	protected.HandleFunc("/api/deploy/app", r.handleDeployApp)
	protected.HandleFunc("/api/deploy/app/deploy", r.handleDeployTrigger)
	protected.HandleFunc("/api/deploy/app/rollback", r.handleDeployRollback)
	protected.HandleFunc("/api/deploy/app/destroy", r.handleDeployDestroy)
	protected.HandleFunc("/api/deploy/app/env", r.handleDeployEnv)
	protected.HandleFunc("/api/deploy/app/log", r.handleDeployLog)
	protected.HandleFunc("/api/deploy/catalog", r.handleDeployCatalog)
	protected.HandleFunc("/api/deploy/catalog/create", r.handleDeployCatalogCreate)
	protected.HandleFunc("/api/deploy/app/preview/teardown", r.handleDeployPreviewTeardown)
	protected.HandleFunc("/api/dev/ports", r.handleDevPorts)           // #21: portas em escuta
	protected.HandleFunc("/api/agent/sessions", r.handleAgentSessions) // #32/#52: kanban+custo
	protected.HandleFunc("/api/docker/compose/file", r.handleComposeFile)
	protected.HandleFunc("/api/docker/prune", r.handlePrune)
	protected.HandleFunc("/api/docker/pull", r.handlePull)
	protected.HandleFunc("/ws/logs/", r.handleLogStream)
	protected.HandleFunc("/ws/stats/", r.handleStatsStream)

	// Persistent browser instances (the list the UI uses to fill the switcher)
	protected.HandleFunc("/api/browser-instances", r.handleBrowserInstances)
	// Per-instance sub-endpoints: /api/browser-instances/{name}/state | /resize
	protected.HandleFunc("/api/browser-instances/", r.handleBrowserInstanceAction)

	// Terminal layout state (per user). It used to live only in the browser's
	// localStorage; now it follows the profile. terminal_state.go has the schema and the details.
	protected.HandleFunc("/api/terminal/state", r.handleTerminalState)
	protected.HandleFunc("/api/terminal/snapshot/", r.handleTerminalSnapshot)
	protected.HandleFunc("/api/terminal/workspace/", r.handleTerminalWorkspace)

	// Bandwidth for the current session (in/out counters accumulated since this
	// JTI's first request). The front-end polls it periodically for the status bar.
	protected.HandleFunc("/api/session/bandwidth", r.handleSessionBandwidth)
	protected.HandleFunc("/api/session/bandwidth/reset", r.handleSessionBandwidthReset)

	// Claude / Config
	protected.HandleFunc("/api/claude/overview", r.handleClaude)
	protected.HandleFunc("/api/claude/mode", r.handleClaudeMode)
	protected.HandleFunc("/api/claude/panic", r.handleClaudePanic)
	protected.HandleFunc("/api/claude/session/fork", r.handleClaudeSessionFork)
	protected.HandleFunc("/api/claude/session/restart", r.handleClaudeSessionRestart)
	// Per-consumer Claude account selector (gated with mustPrimary in the handlers).
	protected.HandleFunc("/api/claude/accounts", r.handleClaudeAccounts)
	protected.HandleFunc("/api/claude/accounts/usage", r.handleClaudeAccountsUsage)
	protected.HandleFunc("/api/claude/accounts/ratelimits", r.handleClaudeAccountsRateLimits)
	protected.HandleFunc("/api/claude/accounts/assign", r.handleClaudeAccountAssign)
	protected.HandleFunc("/api/claude/accounts/login-terminal", r.handleClaudeAccountLoginTerminal)
	protected.HandleFunc("/api/claude/accounts/session-swap", r.handleClaudeAccountSessionSwap)
	// Consolidation onto a single provider — the bridge (/api/private-ai/),
	// Venice (/api/venice/) and the AI-toolkit preflight routes were removed.
	// The private-ai-api service still runs on the host (other projects use
	// it); it is simply no longer exposed through this UI.
	// private-ai-tokens: token management for private-ai-api (Claude AI →
	// Tokens). A server-side proxy; the admin token comes from the secrets vault.
	protected.HandleFunc("/api/private-ai/tokens", r.handlePrivateAITokens)
	protected.HandleFunc("/api/private-ai/tokens/", r.handlePrivateAITokenAction)
	protected.HandleFunc("/api/private-ai/status", r.handlePrivateAIStatus)

	// adguard: the DNS filtering panel (Security → AdGuard). A server-side proxy
	// to the local AdGuard Home admin API; credentials stay in the vault, never in the browser.
	protected.HandleFunc("/api/adguard/status", r.handleAdguardStatus)
	protected.HandleFunc("/api/adguard/protection", r.handleAdguardProtection)

	// tunnel: device manager for the sing-box tunnel (Security → Devices).
	protected.HandleFunc("/api/tunnel/devices", r.handleTunnelDevices)
	protected.HandleFunc("/api/tunnel/devices/", r.handleTunnelDeviceAction)
	protected.HandleFunc("/api/tunnel/usage", r.handleTunnelUsage) // consumo por-aparelho em tempo real

	// datasaver: a per-device compression proxy (Security → Economia).
	// State lives in files on the host; the CA is downloadable; bypass restarts the proxies.
	protected.HandleFunc("/api/datasaver/status", r.handleDatasaverStatus)
	protected.HandleFunc("/api/datasaver/settings", r.handleDatasaverSettings)
	protected.HandleFunc("/api/datasaver/bypass", r.handleDatasaverBypass)
	protected.HandleFunc("/api/datasaver/ca", r.handleDatasaverCA)
	// Runtime-editable AI prompts (gated with mustPrimary in the handler).
	protected.HandleFunc("/api/ai/prompts", r.handleAIPrompts)
	protected.HandleFunc("/api/exec", r.handleExec)
	protected.HandleFunc("/api/config", r.handleConfig)
	protected.HandleFunc("/api/vpsm/health", r.handleVPSMHealth)

	// Audit
	protected.HandleFunc("/api/audit/tail", r.handleAuditTail)
	protected.HandleFunc("/api/audit/search", r.handleAuditSearch)
	protected.HandleFunc("/api/audit/actions", r.handleAuditActions)

	// STT (speech-to-text) — whisper.cpp plus a Silero VAD proxy.
	// The front-end opens a WebSocket here; the handler proxies to the local
	// /opt/stt/stt-proxy.py.
	// /api/stt/health is now PUBLIC. It used to be protected, but guests in a
	// video call need to probe for local whisper to decide which backend to use
	// (without the probe they default to web-speech, which can fail in
	// Brave/Firefox). It is only a health metric (active_sessions, slots_busy,
	// vad_threshold) — nothing sensitive. It matches /ws/stt/transcribe, which
	// is likewise public with manual validation of three token types.
	r.mux.HandleFunc("/api/stt/health", r.handleSTTHealth)
	// /ws/stt/transcribe is PUBLIC but validates three token types (a regular
	// JWT, a videocall invite and a videocall guest token) by hand. Guests
	// joining by PIN or by invite need STT for global transcription to work.
	// Without it a guest's speech is never transcribed, and the "everyone sees
	// everything said" flow only ever captures authenticated users.
	r.mux.HandleFunc("/ws/stt/transcribe", r.handleSTTTranscribe)

	// Alerts
	protected.HandleFunc("/api/metrics/rules", r.handleAlertList)
	protected.HandleFunc("/api/metrics/rules/add", r.handleAlertAdd)
	protected.HandleFunc("/api/metrics/rules/remove", r.handleAlertRemove)
	protected.HandleFunc("/api/metrics/fires", r.handleAlertFires)
	protected.HandleFunc("/api/metrics/catalog", r.handleMetricsCatalog)
	protected.HandleFunc("/api/metrics/snapshot", r.handleMetricsSnapshot)
	protected.HandleFunc("/api/metrics/series", r.handleMetricsSeries)
	protected.HandleFunc("/api/ai/suggest-alert", r.handleAISuggestAlert)

	// Files (sub-mux)
	protected.Handle("/api/files/", http.StripPrefix("/api/files", files.Handler()))

	// Git (sub-mux) — a visual Git client. Every route is primary-only
	// (httpx.MustPrimary) with an allowlist and a per-repo identity; reads
	// (graph/status/diff) and writes (stage/commit/branch) over permitted repos.
	// Additive: removable by this line plus the nav item and the template.
	protected.Handle("/api/git/", http.StripPrefix("/api/git", gitsvc.Handler(r.cfg, r.audit, r.secrets)))

	// Secrets (sub-mux, mounted only if open worked).
	// Tenant boundary: each request resolves the user from the JWT and operates
	// on a namespaced UserVault (on-disk keys: "<user>:<logical>"). The .vault
	// on disk is still one single file — the isolation is logical, not
	// physical. Global keys (JWT_SECRET) pass straight through, but UserVault's
	// List does not expose them to the UI.
	if r.secrets != nil {
		protected.Handle("/api/secrets/", http.StripPrefix("/api/secrets",
			http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				user := auth.UserFrom(req)
				if user == "" {
					writeErr(w, 401, "unauthorized")
					return
				}
				u, err := scope.New(user)
				if err != nil {
					writeErr(w, 400, "invalid user")
					return
				}
				// Audits set/reveal/delete (never the value); the reserved
				// "Sistema" group is gated to the primary user only; size is capped.
				scope.NewUserVault(r.secrets, u).Handler(scope.HandlerOpts{
					Audit:            func(action, target string) { r.auditEvent(req, user, action, target) },
					AllowSystemGroup: r.isPrimary(user),
					MaxValueBytes:    64 << 10,
				}).ServeHTTP(w, req)
			})))
	}

	// Terminals
	protected.HandleFunc("/ws/shell", r.handleHostShell)
	protected.HandleFunc("/ws/container/", r.handleContainerShell)
	protected.HandleFunc("/api/terminal/sessions", r.handleTerminalSessions)
	protected.HandleFunc("/api/terminal/create", r.handleTerminalCreate)                // "Nova sessão" form (cwd/account)
	protected.HandleFunc("/api/terminal/code-restore-ping", r.handleCodeRestorePing)    // gatilho de restore do code-server no reload
	protected.HandleFunc("/api/terminal/assign-session", r.handleTerminalAssignSession) // reassigns the audience (admin-only)
	protected.HandleFunc("/api/terminal/scrollback", r.handleTerminalScrollback)
	protected.HandleFunc("/api/terminal/log-bruto", r.handleTerminalRawLog)    // primer do painel: bytes crus (reserva)
	protected.HandleFunc("/api/terminal/historico", r.handleTerminalHistorico) // panel primer: rendered scrollback
	protected.HandleFunc("/api/terminal/kill-session", r.handleTerminalKillSession)
	// Which sessions are running an OLD version of the Claude Code CLI. The CLI
	// says "Update installed · Restart to update" and the notice stays there
	// forever without ever saying WHICH sessions need restarting — here that
	// becomes a fact.
	protected.HandleFunc("/api/claude/versoes", r.handleClaudeVersoes)
	protected.HandleFunc("/api/claude/recovery/restart", r.handleClaudeRecoveryRestart)
	// The canonical attachment route (any type at all). The old name stays
	// registered on the SAME handler because already-open tabs (cached JS) and
	// the code-server session extension keep posting to it.
	protected.HandleFunc("/api/terminal/upload", r.handleTerminalUpload)
	protected.HandleFunc("/api/terminal/paste-image", r.handleTerminalUpload)
	// Session manager:
	// rename/detach plus backup/restore.
	protected.HandleFunc("/api/terminal/rename-session", r.handleTerminalRenameSession)
	protected.HandleFunc("/api/terminal/preview", r.handleTerminalPreview)
	protected.HandleFunc("/api/terminal/backup", r.handleTerminalBackup)
	protected.HandleFunc("/api/terminal/backups", r.handleTerminalBackupsList)
	protected.HandleFunc("/api/terminal/restore", r.handleTerminalRestore)
	protected.HandleFunc("/api/terminal/backup-delete", r.handleTerminalBackupDelete)

	// WhatsApp behind auth — REST and WebSocket.
	//
	// v1 (legacy): every route is mounted straight onto the global mux and all
	// requests reach the single Service, with no isolation. That is acceptable
	// only because before the migration exactly one profile exists.
	//
	// v2 (Manager): one handler mounted at the prefix resolves the user from
	// the session and dispatches to that user's own Service mux. The WebSocket
	// follows the same pattern so the right user's broadcaster is used —
	// without it, one user would open the socket and see another's events.
	auditWA := func(req *http.Request, action, target string) {
		r.auditEvent(req, auth.UserFrom(req), action, target)
	}
	userFromReq := func(req *http.Request) (scope.User, bool) {
		raw := auth.UserFrom(req)
		if raw == "" {
			return "", false
		}
		u, err := scope.New(raw)
		if err != nil {
			return "", false
		}
		return u, true
	}
	if r.whatsappMgr != nil {
		protected.Handle("/api/whatsapp/", r.whatsappMgr.ProtectedHandler(auditWA, userFromReq))
		protected.Handle("/ws/whatsapp", r.whatsappMgr.ProtectedHandler(auditWA, userFromReq))
		// The avatar is mounted on the public mux (a browser sends no auth on
		// <img>), but v2 needs the user — it resolves them from the JWT cookie,
		// without raising a 401 when it is absent (it returns 404).
		r.mux.HandleFunc("/api/whatsapp/avatar/", func(w http.ResponseWriter, req *http.Request) {
			// Timing-oracle defence: a uniform 404 plus 0-5ms of jitter on any failure.
			notFound := func() {
				var jb [1]byte
				_, _ = rand.Read(jb[:])
				time.Sleep(time.Duration(jb[0]) * 20 * time.Microsecond)
				http.NotFound(w, req)
			}
			tok, _ := req.Cookie("vpsm_token")
			if tok == nil {
				notFound()
				return
			}
			sub, err := r.auth.Parse(tok.Value)
			if err != nil || sub == "" {
				notFound()
				return
			}
			u, err := scope.New(sub)
			if err != nil {
				notFound()
				return
			}
			svc, err := r.whatsappMgr.ForUser(u)
			if err != nil {
				notFound()
				return
			}
			svc.HandleAvatar(w, req)
		})
	}

	// Peer-to-peer video call (WebRTC). It routes signaling, manages persistent
	// rooms in data/videocalls/rooms.json and issues time-limited TURN
	// credentials. Media travels peer-to-peer; the server only sees SDP/ICE,
	// never the video bytes.
	if r.videocall != nil {
		protected.HandleFunc("/api/videocall/rooms", r.videocall.HandleRooms)
		protected.HandleFunc("/api/videocall/members", r.videocall.HandleMembers)
		protected.HandleFunc("/api/videocall/turn", r.videocall.HandleTURN)
		protected.HandleFunc("/api/videocall/invite", r.videocall.HandleInvite)
		protected.HandleFunc("/api/videocall/invite/consume", r.videocall.HandleInviteConsume)
		protected.HandleFunc("/api/videocall/history", r.videocall.HandleHistory)
		protected.HandleFunc("/api/videocall/sessions", r.videocall.HandleRecordSession)
		// Which devices ring when a call comes in.
		protected.HandleFunc("/api/videocall/devices", r.videocall.HandleDevices)
		protected.HandleFunc("/api/videocall/push/public-key", r.videocall.HandlePushPublicKey)
		protected.HandleFunc("/api/videocall/push/subscribe", r.videocall.HandlePushSubscribe)
		protected.HandleFunc("/api/videocall/push/unsubscribe", r.videocall.HandlePushUnsubscribe)
		// Cloud recordings (list + item; the item dispatches internally by path)
		protected.HandleFunc("/api/videocall/recordings", r.videocall.HandleRecordingsRouter)
		protected.HandleFunc("/api/videocall/recordings/", r.videocall.HandleRecordingItemRouter)
		// WhatsApp invite (when the gateway is configured)
		protected.HandleFunc("/api/videocall/invite/whatsapp", r.videocall.HandleInviteWhatsApp)
		// Anonymous PIN: gen/revoke (protected — owner only). Join and the WebSocket are public below.
		protected.HandleFunc("/api/videocall/pin", r.videocall.HandlePIN)
		// Kick: owner-only removal of a peer from the call.
		protected.HandleFunc("/api/videocall/kick", r.videocall.HandleKick)
		// Polish transcript: rewrites a block of speech via Claude Haiku.
		protected.HandleFunc("/api/videocall/transcript/polish", r.videocall.HandleTranscriptPolish)
		protected.HandleFunc("/ws/videocall", r.videocall.HandleWS)
		protected.HandleFunc("/ws/videocall-presence", r.videocall.HandlePresenceWS)
	}

	// User management (auth.Middleware requires a login; no RBAC for now — any
	// authenticated user can make changes, which is acceptable while single-tenant).
	protected.HandleFunc("/api/users", r.handleUsersList)
	protected.HandleFunc("/api/users/create", r.handleUserCreate)
	protected.HandleFunc("/api/users/delete", r.handleUserDelete)
	protected.HandleFunc("/api/users/set-admin", r.handleUserSetAdmin)
	protected.HandleFunc("/api/users/reset-password", r.handleUserResetPassword)
	protected.HandleFunc("/api/users/disable-2fa", r.handleUserDisable2FA)
	protected.HandleFunc("/api/users/revoke-sessions", r.handleUserRevokeSessions)

	// Alerting config (admin only, primary user)
	protected.HandleFunc("/api/admin/alerting", r.handleAlertingConfig)
	protected.HandleFunc("/api/admin/alerting/test", r.handleAlertingTest)

	// AI model tiering: edit which model each tier uses (admin only)
	protected.HandleFunc("/api/admin/ai-models", r.handleAIModelsConfig)

	// Notification spine — event-driven rules/channels/history.
	// All primary-only (mustPrimary inside each handler).
	protected.HandleFunc("/api/notify/rules", r.handleNotifyRules)
	protected.HandleFunc("/api/notify/rules/delete", r.handleNotifyRuleDelete)
	protected.HandleFunc("/api/notify/channels", r.handleNotifyChannels)
	protected.HandleFunc("/api/notify/channels/delete", r.handleNotifyChannelDelete)
	protected.HandleFunc("/api/notify/channels/test", r.handleNotifyChannelTest)
	protected.HandleFunc("/api/notify/dryrun", r.handleNotifyDryRun)
	protected.HandleFunc("/api/notify/events", r.handleNotifyEvents)
	protected.HandleFunc("/api/notify/inbox", r.handleNotifyInbox)
	protected.HandleFunc("/api/notify/catalog", r.handleNotifyCatalog)

	// Per-user preferences (UI layout and so on). Each user writes to their own
	// data/users/<user>/prefs.json — the scope is isolated by the auth middleware.
	protected.HandleFunc("/api/user/prefs", r.handleUserPrefs)

	r.mux.Handle("/api/", r.auth.Middleware(protected))
	r.mux.Handle("/ws/", r.auth.Middleware(protected))

	// Tunnelled browser (Ultraviolet + Wisp): an isolated Node service on
	// 127.0.0.1:8090, exposed here under /browser/ on the same origin and
	// protected by the panel's own login. Go's ReverseProxy covers both the HTTP
	// traffic and the WebSocket upgrade on /browser/wisp/.
	r.mux.Handle("/browser/", r.auth.Middleware(browserProxy()))

	// Persistent browser (noVNC + TigerVNC + Vivaldi): multi-instance.
	// Routing: /browser-persistent/<name>/... → 127.0.0.1:<port>, discovered in
	// <DataDir>/users/<user>/browser-instances.json (per-user; the v1→v2
	// migration moved the legacy data/browser-instances.json there).
	// Compatibility with /browser-persistent/* (no name) is kept — it routes to
	// "default".
	// A critical HTTP handler — do not remove without updating the tests in api_browser_test.go.
	r.mux.Handle("/browser-persistent/", r.auth.Middleware(r.browserPersistentProxy()))

	sub, _ := fs.Sub(webFS, "web")
	fileServer := http.FileServer(http.FS(sub))
	// vendorCached wraps fileServer to set the right Cache-Control:
	//   - /vendor/vpsm/app/* and /tailwind.css: change between deploys ->
	//     revalidate every time (cache-busting via ?v=__VPSM_BUILD__ on the
	//     <script src>).
	//   - /vendor/<lib>/*: versions pinned in fixed files (xterm, alpine,
	//     monaco) -> immutable + one year.
	vendorCached := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		p := req.URL.Path
		// Minified when available AND PROVABLY FRESH: `make minify` writes
		// <x>.min.js next to <x>.js and stamps the source's sha256 into it;
		// webassets.MinificadoDe accepts only the pair whose stamp matches the
		// <x>.js from the same embed. With no minified file, no stamp, or a
		// stale stamp, the original is served — deliberately fail-open.
		//
		// The choice used to be made by PRESENCE, and that took the app down:
		// the .min.js is a generated, untracked artefact, so a bundle from four
		// days earlier kept being served against a fresh index.html and the
		// front-end framework threw ReferenceError at boot. See
		// internal/webassets/minificado.go.
		//
		// ?raw=1 returns the original: the LIVE invariant check greps the served
		// asset literally, and minification rewrites whitespace and quotes. Same
		// public content, just not minified.
		if strings.HasPrefix(p, "/vendor/vpsm/app/") && strings.HasSuffix(p, ".js") &&
			!strings.HasSuffix(p, ".min.js") && req.URL.Query().Get("raw") != "1" {
			if mp, ok := webassets.MinificadoDe(strings.TrimPrefix(p, "/")); ok {
				r2 := req.Clone(req.Context())
				r2.URL.Path = "/" + mp
				req = r2
				p = r2.URL.Path
			}
		}
		if strings.HasPrefix(p, "/vendor/vpsm/") || p == "/tailwind.css" {
			// no-cache means always revalidate, BUT with a real ETag. embed.FS
			// has a zero ModTime, so http.ServeContent emitted neither ETag nor
			// Last-Modified and every reload re-downloaded the whole body. ETag =
			// buildStamp (it changes on each build) -> a reload becomes a 304 when
			// nothing changed.
			//
			// The ETag also distinguishes the encoding; otherwise a cache holding
			// the brotli variant would revalidate against the gzip one and get a
			// 304 for a body it cannot read.
			etag := `"` + buildStamp + `"`
			if webassets.AceitaBrotli(req) {
				etag = `"` + buildStamp + `-br"`
			}
			w.Header().Set("ETag", etag)
			w.Header().Set("Cache-Control", "public, no-cache")
			if req.Header.Get("If-None-Match") == etag {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			// Brotli at maximum level, compressed once per build. These are
			// exactly the files a user re-downloads on EVERY deploy (the ETag
			// changes), and at 40-90 deploys a day that happens constantly:
			// 154 KB → 125 KB for the app bundle. Restricted to the app's own
			// code — putting monaco (13 MB) in this cache would trade bandwidth
			// for memory.
			if corpo, err := fs.ReadFile(sub, strings.TrimPrefix(p, "/")); err == nil {
				if ct := mime.TypeByExtension(path.Ext(p)); ct != "" {
					w.Header().Set("Content-Type", ct)
				}
				if webassets.ServeBrotliAsset(w, req, p+":"+buildStamp, corpo) {
					return
				}
				// Brotli did not pay off (the client does not support it, or it
				// did not compress): the Content-Type is already set and fileServer carries on as usual.
			}
		} else if strings.HasPrefix(p, "/vendor/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		fileServer.ServeHTTP(w, req)
	})
	// "/" does mobile detection plus override. Other routes (assets, /m, etc.)
	// go straight to fileServer. indexInjector intercepts "/" and "/index.html"
	// to replace __VPSM_BUILD__ with the current buildStamp (the front-end
	// purges incompatible state after a deploy).
	// The separate mobile app (/m/) was removed — phones get the desktop UI,
	// with no redirect.
	// uvLeakRedirect rescues root-relative assets leaking out of the tunnelled
	// browser — /_next/... from a proxied site, say — before fileServer answers
	// 404. It is gated on the Referer, so ordinary requests pass straight
	// through.
	r.mux.Handle("/", uvLeakRedirect(noStoreHTML(indexInjector(vendorCached))))

	// Everything is wrapped in the bandwidth tracker, including index.html,
	// the stylesheet and every asset — any byte that leaves this process for a
	// user. The session id comes from the auth cookie without checking
	// revocation, because the only job here is attributing bytes to a session,
	// not deciding whether that session may act.
	// Middleware chain, outermost first — the order is load-bearing:
	//   withLogging          → request and duration
	//   securityHeaders      → CSP, HSTS, X-Frame-Options
	//   CompressMiddleware   → transparent gzip, skipped for WebSocket and SSE
	//   MaxBodyMiddleware    → a global body-size limit on routes that read one
	//   bandwidthMiddleware  → bytes attributed per session
	//   fdroidGate           → intercepts the package repository before the mux,
	//                          because ServeMux 301-redirects any "unclean"
	//                          path before dispatching, and that redirect
	//                          breaks the client fetching from it
	//   r.mux                → routing
	handler := httpmw.Bandwidth(r.auth, r.fdroidGate(r.mux))
	handler = httpmw.MaxBody(handler)
	handler = httpmw.Compress(handler)
	handler = securityHeaders(handler)
	handler = withLogging(handler)
	r.handler = handler
	return r, nil
}

// ServeHTTP delegates to the handler chain composed in NewRouter.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	// Public demo: deny by default. This is the one point every request
	// passes through, which is why the check lives here instead of on each
	// route — a route added later is refused until someone allowlists it.
	// Inert unless DEMO_MODE is set. See demo_mode.go.
	if demoMode() && !demoAllows(req) {
		demoDeny(w, req)
		return
	}
	r.handler.ServeHTTP(w, req)
}

// Shutdown shuts down the Router's internal subsystems (scheduler, queue,
// session store, videocall).
// StartBackgroundWorkers starts the background workers (collectors, pollers,
// pushers, watchers). It deliberately lives OUTSIDE NewRouter.
//
// Building a Router and starting its workers are two different things, and
// conflating them was expensive: the workers came up on context.Background(),
// nobody could stop them (not even Shutdown itself), and every test that built
// a Router left dozens of goroutines alive until the binary exited — reading
// `r.cfg` while the test was writing to it. The race detector reported a DATA
// RACE between `intake_email_pusher.go` and the test that adjusts the config,
// and the visible symptom was a different test failing on every run: the
// background pusher sent one extra POST to whichever test server was up.
//
// Kept separate, a test gets a Router that is inert by construction — not by
// luck of scheduling — and the real process gets a shutdown that actually
// shuts things down.
//
// Calling it twice cancels the previous batch before starting the new one.
func (r *Router) StartBackgroundWorkers(ctx context.Context) {
	if r == nil {
		return
	}
	if r.pararTrabalhadores != nil {
		r.pararTrabalhadores()
	}
	ctx, cancel := context.WithCancel(ctx)
	r.pararTrabalhadores = cancel

	r.startMetricsCollector(ctx)
	r.startInventoryPoller(ctx) // discovery + stamping
	r.startSessionBackupCollector(ctx)
	r.startHypervisorWatcher(ctx)     // a casa caiu e ninguem avisou (2026-08-21): a sentinela mora no VPS de proposito
	r.startAgentStatusAggregator(ctx) // VPSM agent-ops #3: cost/token aggregator → session-status.json
	r.startLeakWatcher(ctx)           // tunnel leak watchdog — home-exit traffic must never leave through the VPS
	if r.netUsage != nil {
		r.netUsage.Start(ctx) // consumo por-aparelho em tempo real (conntrack read-only)
	}
	r.startDatasaverWatcher(ctx) // auto-reverts data saving to direct if the proxy goes down (connectivity > compression)
}

func (r *Router) Shutdown(ctx context.Context) {
	if r == nil {
		return
	}
	// Stop the background workers BEFORE anything else: while they run, the
	// collector is still writing, the pusher is still POSTing and the watcher is
	// still firing alerts — all against a Router that is already being
	// dismantled underneath them.
	if r.pararTrabalhadores != nil {
		r.pararTrabalhadores()
		r.pararTrabalhadores = nil
	}
	// Stop the scheduler first so no cron tick enqueues new work while we
	// drain, then bound-drain the queue (caps internally at min(4s, ctx)).
	// Jobs still running when the cap expires are marked StatusInterrupted —
	// the boot reconcile offers them for re-run instead of losing them.
	if r.scheduler != nil {
		r.scheduler.Stop()
	}
	if r.queue != nil {
		r.queue.Shutdown(ctx)
	}
	// Close the notify worker AFTER the queue drains: jobs cancelled during the
	// drain still fire the terminal hook (hook 2/5) → Dispatch, which needs the
	// worker alive to accept into its buffer.
	if r.notify != nil {
		r.notify.Close()
	}
	if r.videocall != nil {
		_ = r.videocall.Close()
	}
	if r.sessionsSt != nil {
		_ = r.sessionsSt.Close()
	}
	if r.telSink != nil {
		_ = r.telSink.Close()
	}
	if r.mobileOpsHealthStop != nil {
		r.mobileOpsHealthStop()
	}
}

// securityHeaders moved to internal/httpmw/security.go (SecurityHeaders).
// The wrapper is kept for compatibility with the call site below in registerRoutes/NewRouter.
func securityHeaders(next http.Handler) http.Handler { return httpmw.SecurityHeaders(next) }

// indexInjector moved to internal/webassets/pages.go.
func indexInjector(next http.Handler) http.Handler { return webassets.IndexInjector(next) }

// noStoreHTML moved to internal/httpmw/nostore.go (NoStoreHTML).
func noStoreHTML(next http.Handler) http.Handler { return httpmw.NoStoreHTML(next) }

// startMetricsCollector runs a periodic collector in a goroutine. ctx cancels
// collection at shutdown (gracefully — without it the goroutine stayed alive
// until the process died, blocking an in-flight collection and a RingBuffer
// write). It is currently called with context.Background(); main.go could pass
// the server's shutdown ctx so the cancellation propagates.
func (r *Router) startMetricsCollector(ctx context.Context) {
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("metrics collector panic: %v", rec)
			}
		}()
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				log.Printf("metrics collector: shutting down (ctx done)")
				return
			case <-t.C:
			}
			// Collect ALL the metrics in the catalogue (respecting each
			// collector's own cadence) and evaluate the rules over that
			// snapshot. The timeout is generous to accommodate expensive
			// collectors (docker, claude); one that overruns returns nil and
			// the Registry reuses its last cache.
			cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			snap := r.metricReg.Gather(cctx)
			cancel()
			v := snap.Values
			// The Point feeds the four History charts (/api/system/history).
			r.ring.Push(metrics.Point{
				T:       snap.T,
				CPU:     v["sys.cpu"],
				MemUsed: uint64(v["sys.mem_used"]),
				MemPct:  v["sys.mem_pct"],
				Load1:   v["sys.load1"],
				DiskPct: v["sys.disk.max"],
			})
			r.metricHist.Push(snap)
			r.recordFires(r.alerts.Evaluate(snap))
		}
	}()
}

// recordFires stores metric fires in the legacy ring (r.fires — the source the
// "Disparos recentes" UI reads via f.Rule/Value/Time) AND dual-writes them to
// the notify spine so thresholds reach configured channels. The ring
// write is preserved and happens FIRST — removing it is a later step, the UI
// depends on it today. Extracted from the metrics goroutine so the ring-populated
// invariant is unit-testable.
//
// Fires are now EDGE transitions: a fire arrives only when a rule
// crosses its threshold or normalizes, not on every tick. Resolved fires are
// recovery signals — they go to the notify spine (so a "recovered" rule can
// notify) but NOT to the legacy "Disparos recentes" ring, which means crossings.
// On any transition we persist the engine state so the new ActiveSince/LastFired
// survive a deploy/restart — this is what closes the re-notify-on-every-deploy
// vector. Persist runs AFTER Evaluate has mutated and unlocked the engine, so
// Save's List() (RLock) reads the already-updated state without nesting locks.
func (r *Router) recordFires(fires []metrics.Fire) {
	if len(fires) == 0 {
		return
	}
	var crossings []metrics.Fire
	for _, f := range fires {
		if !f.Resolved {
			crossings = append(crossings, f)
		}
	}
	if len(crossings) > 0 {
		r.firesMu.Lock()
		r.fires = append(r.fires, crossings...)
		if len(r.fires) > 200 {
			r.fires = r.fires[len(r.fires)-200:]
		}
		r.firesMu.Unlock()
	}

	if r.notify != nil {
		for _, f := range fires {
			r.notify.Dispatch(metricEvent(f))
		}
	}

	// A fire means a state transition happened: persist ActiveSince/LastFired.
	r.persistAlertRules()
}

func withLogging(next http.Handler) http.Handler { return httpmw.WithLogging(next) }

// writeJSON delegates to httpx.WriteJSON. The alias is kept to reduce churn
// across the 100+ call sites — once each handler moves into its own domain
// package, this becomes a direct httpx.WriteJSON call.
func writeJSON(w http.ResponseWriter, v interface{}) { httpx.WriteJSON(w, v) }

// ingestWhatsAppSecretsPut consumes the sidecar manifest dropped by
// scripts/whatsapp/install.sh (which can't decrypt the AES-GCM vault itself).
// On success the file is removed so the credentials don't linger in plaintext.
// Errors are logged but never fatal — the user can still type the secrets via
// the Secrets UI as a fallback.
//
// Manifest schema after v2: it may be a map[string]string (legacy, which
// assumes fallbackUser) or {"user": "<u>", "secrets": {k:v}}. Keys go into the
// vault namespaced as "<u>:<k>".
func ingestWhatsAppSecretsPut(dataDir string, vault *secrets.Store, fallbackUser string) {
	path := filepath.Join(dataDir, "whatsapp", "secrets.put")
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var typed struct {
		User    string            `json:"user"`
		Secrets map[string]string `json:"secrets"`
	}
	var manifest map[string]string
	user := fallbackUser
	if err := json.Unmarshal(data, &typed); err == nil && typed.Secrets != nil {
		manifest = typed.Secrets
		if typed.User != "" {
			user = typed.User
		}
	} else if err := json.Unmarshal(data, &manifest); err != nil {
		log.Printf("whatsapp secrets.put parse: %v", err)
		return
	}
	if user == "" {
		log.Printf("whatsapp secrets.put: no user (manifest has no user, empty fallback); skipping")
		return
	}
	u, err := scope.New(user)
	if err != nil {
		log.Printf("whatsapp secrets.put: invalid user %q: %v", user, err)
		return
	}
	uv := scope.NewUserVault(vault, u)
	for k, v := range manifest {
		if err := uv.Set(k, v); err != nil {
			log.Printf("whatsapp secrets vault.Set %s:%s: %v", user, k, err)
			return
		}
	}
	if err := os.Remove(path); err != nil {
		log.Printf("whatsapp secrets.put remove: %v", err)
	}
	log.Printf("whatsapp: ingested %d secrets for user %s from %s", len(manifest), user, path)
}

// writeErr delegates to httpx.WriteErr. Same reason as writeJSON: a
// transitional alias until the handlers move into their own packages.
func writeErr(w http.ResponseWriter, code int, msg string) { httpx.WriteErr(w, code, msg) }

// setUserSupabaseMFA updates the local mirror of a user's MFA status and
// persists the config when it changed. Idempotent, and safe to call for a user
// that does not exist (it is then a no-op). Called from:
//   - handleMFAEnrollVerify (after the provider verifies) → true
//   - handleMFADisable      (after the factor is deleted) → false
//   - handleLogin           (after authentication)        → sync with reality
//
// Why it exists: TOTP no longer lives in the local config, only at the identity
// provider. But the admin screen has to show "● on" or "○ off" per user, and
// there is no way to ask the provider about *another* user without holding
// their access token. This mirror is what lets the screen read locally.
// userHasSupabaseMFA reads the local mirror: does this user have MFA on? Used
// during login to fail CLOSED when the lookup to the provider fails — a user
// known to have 2FA must not get in without the second factor merely because
// the provider blinked.
func (r *Router) userHasSupabaseMFA(username string) bool {
	if username == "" {
		return false
	}
	r.cfgMu.Lock()
	defer r.cfgMu.Unlock()
	for i := range r.cfg.Users {
		if r.cfg.Users[i].Username == username {
			return r.cfg.Users[i].SupabaseMFAEnabled
		}
	}
	return false
}

// userIsAppOnly reads the account's "app-only" flag under cfgMu — the same
// read protocol as userHasSupabaseMFA, because r.cfg is mutated at runtime
// (password change, MFA, user CRUD) and reading it without the lock is a race.
//
// It is called by the WEB PANEL's entry points (handleLogin,
// handleRecoveryAuth, handleRefreshCookie). The app's own path (MobileLogin,
// passkey) does NOT call it — that is precisely the path such an account is
// meant to use.
func (r *Router) userIsAppOnly(username string) bool {
	if username == "" {
		return false
	}
	r.cfgMu.Lock()
	defer r.cfgMu.Unlock()
	return r.cfg.IsAppOnly(username)
}

func (r *Router) setUserSupabaseMFA(username string, enabled bool) {
	if username == "" {
		return
	}
	r.cfgMu.Lock()
	defer r.cfgMu.Unlock()
	changed := false
	for i := range r.cfg.Users {
		if r.cfg.Users[i].Username == username {
			if r.cfg.Users[i].SupabaseMFAEnabled != enabled {
				r.cfg.Users[i].SupabaseMFAEnabled = enabled
				changed = true
			}
			break
		}
	}
	if changed {
		if err := config.Save(r.cfg, filepath.Join(r.cfg.DataDir, "config.json")); err != nil {
			log.Printf("setUserSupabaseMFA: save error: %v", err)
		}
	}
}

// auditEvent delegates to httpx.AuditEvent. It is kept as a method to preserve
// the 60+ r.auditEvent(...) call sites. As each handler moves out it will call
// httpx.AuditEvent directly with its own Deps.AuditLog.
func (r *Router) auditEvent(req *http.Request, user, action, target string) {
	httpx.AuditEvent(r.audit, req, user, action, target)
}

// mustPrimary is the canonical RBAC gate for admin-only endpoints.
// Returns (caller, true) when the caller is authenticated AND is the
// configured primary. Writes 401/403 and returns ok=false otherwise.
// Idiomatic use:
//
//	caller, ok := r.mustPrimary(w, req)
//	if !ok { return }
//
// Audit: every denied call is logged with a stable action prefix so the
// /audit page can surface attempted privilege escalations.
func (r *Router) mustPrimary(w http.ResponseWriter, req *http.Request) (string, bool) {
	return httpx.MustPrimary(w, req, r.cfg, r.audit)
}

// isPrimary reports whether user is the config.Primary account — the one
// that inherits legacy untagged sessions (and, in time, any other
// resource that pre-dates per-user namespacing). Empty config.Primary or
// empty user → false. Used by terminal handlers + HostShell so the
// "vpsm-<user>-" ACL is widened only for the primary user.
func (r *Router) isPrimary(user string) bool { return httpx.IsPrimary(r.cfg, user) }

// ---------- Auth ----------

// setAuthCookie issues the JWT also as an HttpOnly cookie so non-script-driven
// navigations (notably same-origin iframes like the /browser/ tab) authenticate
// automatically. The SPA continues using the Bearer header from localStorage —
// both channels are valid; auth.Middleware reads either (see auth.go:229,235).
// Path=/ is required so /browser/* is covered, not just /api/auth/*.
// The cookies moved to internal/httpx/cookies.go. These wrappers preserve the
// current call sites until the handlers are extracted into their domain packages.
func setAuthCookie(w http.ResponseWriter, token string) { httpx.SetAuthCookie(w, token) }
func clearAuthCookie(w http.ResponseWriter)             { httpx.ClearAuthCookie(w) }

// The Supabase cookies and the flag moved to internal/httpx/cookies.go.
func setSupabaseAccessCookie(w http.ResponseWriter, access string, ttlSeconds int) {
	httpx.SetSupabaseAccessCookie(w, access, ttlSeconds)
}
func clearSupabaseAccessCookie(w http.ResponseWriter)          { httpx.ClearSupabaseAccessCookie(w) }
func readSupabaseAccessCookie(req *http.Request) string        { return httpx.ReadSupabaseAccessCookie(req) }
func setSupabaseRefreshCookie(w http.ResponseWriter, r string) { httpx.SetSupabaseRefreshCookie(w, r) }
func clearSupabaseRefreshCookie(w http.ResponseWriter)         { httpx.ClearSupabaseRefreshCookie(w) }
func readSupabaseRefreshCookie(req *http.Request) string       { return httpx.ReadSupabaseRefreshCookie(req) }
func setCookieFlag(w http.ResponseWriter, set bool)            { httpx.SetCookieFlag(w, set) }

// Trusted-device cookie — thin wrappers over httpx.
func setDeviceCookie(w http.ResponseWriter, secret string) { httpx.SetDeviceCookie(w, secret) }
func clearDeviceCookie(w http.ResponseWriter)              { httpx.ClearDeviceCookie(w) }
func readDeviceCookie(req *http.Request) string            { return httpx.ReadDeviceCookie(req) }

// execCmd runs a short command, returning combined stdout+stderr.
// execCmd runs a command with a 30s hard timeout. The previous version
// used context.Background() with NO timeout — a hung subprocess (DNS
// lookup, locked apt, ss with stuck socket) could pile up forever.
//
// Callers that need to attach the request lifecycle should use
// execCmdCtx instead so a client disconnect kills the subprocess.
func execCmd(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, name, args...)
	out, err := c.CombinedOutput()
	return string(out), err
}

// execCmdCtx is the request-scoped variant: cancellation propagates to
// the subprocess via the passed context. Use this from HTTP handlers so
// long-running commands die when the client closes the connection.
func execCmdCtx(ctx context.Context, name string, args ...string) (string, error) {
	c := exec.CommandContext(ctx, name, args...)
	out, err := c.CombinedOutput()
	return string(out), err
}

// execCmdLong runs a command with a 10-minute timeout. Used for apt operations
// that can take a while on first run / on large upgrades.
func execCmdLong(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	c := exec.CommandContext(ctx, name, args...)
	c.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")
	out, err := c.CombinedOutput()
	return string(out), err
}

// handleExec runs a shell command as the vps-manager process owner (root).
// PRIMARY-ONLY by design — this is direct RCE if exposed to non-admins.
// Request context drives a 60s hard timeout so a runaway subprocess dies
// when the client disconnects.
// ---------- Audit ----------

// handleAudit* extracted into handlers_audit.go.

// ---------- Terminals ----------

// The PWA handlers (manifest, sw.js, icons, /join) moved to internal/webassets/.
// These wrappers preserve the references in NewRouter.
func (r *Router) handleJoinPage(w http.ResponseWriter, req *http.Request) {
	webassets.HandleJoinPage(w, req)
}
func (r *Router) handleManifest(w http.ResponseWriter, req *http.Request) {
	webassets.HandleManifest(w, req)
}
func (r *Router) handleServiceWorker(w http.ResponseWriter, req *http.Request) {
	webassets.HandleServiceWorker(w, req)
}
func (r *Router) handleIcon(w http.ResponseWriter, req *http.Request) { webassets.HandleIcon(w, req) }

// safeUploadName turns a client-supplied name into a basename that is safe to
// write inside the tenant's upload directory. The name is HOSTILE by
// definition (it comes from a browser, or from a forged POST):
// "../../etc/cron.d/x", "a/b", names with NUL or control characters, enormous
// names, or empty ones.
//
// The rules: basename only; characters outside the allowlist become "_";
// leading dots are stripped (no ".bashrc", and no ""); name and extension are
// both length-capped. It returns "" when nothing usable is left — the caller
// then falls back to a synthetic name.
func safeUploadName(name string) string {
	// The last segment only, honouring BOTH the POSIX and the Windows separator
	// (an upload from Windows sends "C:\\Users\\x\\a.pdf" in the header).
	name = strings.TrimSpace(name)
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '.' || r == '-' || r == '_':
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f:
			// control characters: dropped (not turned into "_", to keep the name clean)
		default:
			// Accents, spaces, emoji, Cyrillic: all become "_". No attempt at
			// transliteration — the goal is a name that is predictable in the shell.
			b.WriteRune('_')
		}
	}
	out := strings.Trim(b.String(), ".")
	// Collapse repeated "__" so that CJK text does not produce absurd names.
	for strings.Contains(out, "__") {
		out = strings.ReplaceAll(out, "__", "_")
	}
	if out == "" {
		return ""
	}
	// Caps the length while preserving the extension (the extension is what
	// tells the reader, human or tool, what the file is).
	const maxName = 96
	if len(out) > maxName {
		ext := filepath.Ext(out)
		if len(ext) > 16 {
			ext = ""
		}
		keep := maxName - len(ext)
		if keep < 1 {
			keep = 1
		}
		out = out[:keep] + ext
	}
	return out
}

// handleTerminalUpload takes ANY file coming from the terminal — pasted,
// dropped or picked — and writes it under the user's upload directory. It
// returns the absolute path, which the client then types into the pane: that is
// how a document reaches the process running there, whatever it is.
//
// This used to accept image/* only, and the route was named for pasting images.
// The restriction protected nothing — the file is written, never executed and
// never served — while blocking the most useful case: handing a PDF, a CSV or a
// log to whatever is reading in that pane. The old route stays registered as an
// alias, because cached tabs and the editor extension still post to it.
//
// Contract: multipart, field "file" (or "image", for the legacy alias), 25 MiB
// at most — the global body limit cuts anything larger before it reaches here.
func (r *Router) handleTerminalUpload(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	user := auth.UserFrom(req)
	if user == "" {
		writeErr(w, 401, "unauthorized")
		return
	}
	const maxUpload = 25 << 20
	req.Body = http.MaxBytesReader(w, req.Body, maxUpload)
	if err := req.ParseMultipartForm(8 << 20); err != nil {
		writeErr(w, 413, "file larger than 25MB or invalid form: "+err.Error())
		return
	}
	file, header, err := req.FormFile("file")
	if err != nil {
		// Legacy: older clients (a cached tab, the code-server extension) send "image".
		file, header, err = req.FormFile("image")
		if err != nil {
			writeErr(w, 400, "field 'file' is required")
			return
		}
	}
	defer file.Close()

	// Sniffing is only there to TELL the client (and to choose an extension when
	// the name has none). It is not a gate: any type is accepted.
	buf := make([]byte, 512)
	n, _ := file.Read(buf)
	ct := http.DetectContentType(buf[:n])

	u, err := scope.New(user)
	if err != nil {
		writeErr(w, 400, "invalid user: "+err.Error())
		return
	}
	paths := scope.PathsFor(r.cfg.DataDir, u)
	if err := os.MkdirAll(paths.Uploads, 0o700); err != nil {
		writeErr(w, 500, "create uploads dir: "+err.Error())
		return
	}

	// The name: keep the user's own wherever possible — "contrato-cliente.pdf"
	// carries intent that "paste-1787….bin" does not, and whoever reads the path
	// in the terminal (person or AI) navigates by it. The timestamp prefix avoids
	// collisions and keeps the folder sortable by arrival.
	base := ""
	if header != nil {
		base = safeUploadName(header.Filename)
	}
	if base == "" {
		ext := ".bin"
		switch ct {
		case "image/png":
			ext = ".png"
		case "image/jpeg":
			ext = ".jpg"
		case "image/gif":
			ext = ".gif"
		case "image/webp":
			ext = ".webp"
		case "application/pdf":
			ext = ".pdf"
		default:
			if strings.HasPrefix(ct, "text/") {
				ext = ".txt"
			}
		}
		base = "paste" + ext
	}
	outPath := filepath.Join(paths.Uploads, fmt.Sprintf("%d-%s", time.Now().UnixNano(), base))

	// Belt and braces: even with the name sanitised, confirm the destination
	// really did land INSIDE the tenant's upload directory before opening it for
	// writing. A future bug in safeUploadName then becomes an error rather than a
	// write outside the scope.
	if !strings.HasPrefix(filepath.Clean(outPath), filepath.Clean(paths.Uploads)+string(os.PathSeparator)) {
		writeErr(w, 400, "invalid file name")
		return
	}
	out, err := os.OpenFile(outPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		writeErr(w, 500, "create file: "+err.Error())
		return
	}
	defer out.Close()
	// buf has already consumed the reader's first n bytes — write it out before the rest.
	if _, err := out.Write(buf[:n]); err != nil {
		writeErr(w, 500, "write: "+err.Error())
		return
	}
	written, err := io.Copy(out, file)
	if err != nil {
		writeErr(w, 500, "write: "+err.Error())
		return
	}
	r.auditEvent(req, user, "terminal.upload", outPath)
	// Signals the path to the code-server extension's bridge, which fs.watches
	// this file and injects the path into the ACTIVE terminal. Best-effort: the
	// path also comes back in the JSON, so the web client does not depend on it.
	writePasteInbox(filepath.Dir(paths.Uploads), outPath)
	writeJSON(w, map[string]any{
		"path": outPath,
		"name": filepath.Base(outPath),
		"size": int64(n) + written,
		"type": ct,
	})
}

// writePasteInbox writes (atomically, via temp + rename) the last pasted path
// into the signal file <userDir>/paste-inbox. The code-server extension watches
// that file (fs.watch) and injects the path into code-server's active terminal.
// Errors are logged and ignored — the path also comes back in the JSON, so the
// paste webview still serves as a fallback.
func writePasteInbox(userDir, path string) {
	inbox := filepath.Join(userDir, "paste-inbox")
	tmp := inbox + ".tmp"
	if err := os.WriteFile(tmp, []byte(path+"\n"), 0o600); err != nil {
		log.Printf("paste-inbox: write tmp: %v", err)
		return
	}
	if err := os.Rename(tmp, inbox); err != nil {
		log.Printf("paste-inbox: rename: %v", err)
		_ = os.Remove(tmp)
	}
}

func (r *Router) handleContainerShell(w http.ResponseWriter, req *http.Request) {
	if !r.dockerReady(w) {
		return
	}
	id := strings.TrimPrefix(req.URL.Path, "/ws/container/")
	if id == "" {
		writeErr(w, 400, "container id required")
		return
	}
	ptysvc.ContainerShell(w, req, r.docker.Raw(), id)
}

// fmtSscan is a tiny helper to parse integer query params without importing fmt twice
func fmtSscan(s string, v *int) (int, error) {
	var n int
	var neg bool
	for _, c := range s {
		if c == '-' && n == 0 {
			neg = true
			continue
		}
		if c < '0' || c > '9' {
			return 0, errBadInt
		}
		n = n*10 + int(c-'0')
	}
	if neg {
		n = -n
	}
	*v = n
	return 1, nil
}

var errBadInt = &parseErr{"bad int"}

type parseErr struct{ s string }

func (p *parseErr) Error() string { return p.s }

func credsFromConfig(c *config.Config) []auth.Credential {
	all := c.AllUsers()
	out := make([]auth.Credential, len(all))
	for i, u := range all {
		out[i] = auth.Credential{Username: u.Username, PasswordHash: u.PasswordHash}
	}
	return out
}

// handleCron reads or writes the root crontab. GET returns the current
// crontab as text; POST replaces it. The user-controlled body is fed via
// stdin to `crontab -` which lets crontab itself validate syntax.
func (r *Router) handleCron(w http.ResponseWriter, req *http.Request) {
	// crontab edits run as root via `crontab -` → primary-only.
	// GET is gated too: viewing the root crontab leaks operational info
	// (job schedules, credentials in command lines) — admins only.
	if _, ok := r.mustPrimary(w, req); !ok {
		return
	}
	switch req.Method {
	case http.MethodGet:
		out, err := execCmd("crontab", "-l")
		// "no crontab for root" returns non-zero — treat as empty
		if err != nil && strings.Contains(out, "no crontab") {
			out, err = "", nil
		}
		if err != nil {
			writeErr(w, 500, err.Error()+" "+out)
			return
		}
		writeJSON(w, map[string]any{"content": out})
	case http.MethodPost:
		var body struct {
			Content string `json:"content"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			writeErr(w, 400, "bad json")
			return
		}
		// crontab - reads from stdin. crontab validates the content.
		c := exec.CommandContext(req.Context(), "crontab", "-")
		c.Stdin = strings.NewReader(body.Content)
		out, err := c.CombinedOutput()
		r.auditEvent(req, auth.UserFrom(req), "cron.write", "")
		if err != nil {
			writeErr(w, 400, "crontab rejected: "+err.Error()+" "+string(out))
			return
		}
		writeJSON(w, map[string]string{"status": "ok"})
	default:
		writeErr(w, 405, "method not allowed")
	}
}

// handleComposeCreate writes a compose.yml from a form-based payload.
// Body: {project_name, dir, content}. `dir` defaults to /opt/compose/<name>.
// The content goes straight to disk; the wizard on the front builds it.
func (r *Router) handleComposeCreate(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	var body struct {
		ProjectName string `json:"project_name"`
		Dir         string `json:"dir"`
		Content     string `json:"content"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	if body.ProjectName == "" {
		writeErr(w, 400, "project_name required")
		return
	}
	for _, c := range body.ProjectName {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_') {
			writeErr(w, 400, "project_name must be [A-Za-z0-9_-]")
			return
		}
	}
	// Path safety v2: NEVER trust body.Dir from the client. Earlier check
	// (IsAbs + no `..`) let an authenticated user write compose.yml to
	// /etc/cron.d/, /root/.ssh/authorized_keys etc → direct RCE-as-root.
	// Fix: derive the dir from the (already validated) project_name and
	// pin it under /opt/compose/. body.Dir is silently ignored when set.
	const composeRoot = "/opt/compose"
	dir := filepath.Join(composeRoot, body.ProjectName)
	if !strings.HasPrefix(dir, composeRoot+string(filepath.Separator)) {
		// Defence-in-depth: filepath.Join already normalises, but verify
		// the result is genuinely under composeRoot before mkdir.
		writeErr(w, 400, "internal: derived path escaped compose root")
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		writeErr(w, 500, "mkdir: "+err.Error())
		return
	}
	target := filepath.Join(dir, "compose.yml")
	if err := os.WriteFile(target, []byte(body.Content), 0o644); err != nil {
		writeErr(w, 500, "write: "+err.Error())
		return
	}
	r.auditEvent(req, auth.UserFrom(req), "compose.create", body.ProjectName)
	writeJSON(w, map[string]string{"status": "ok", "path": target})
}

// loadTURNConfig reads /etc/vpsm/coturn.env (written by `vpsmctl videocall
// init`) and returns the TURN config the videocall service uses to mint
// time-limited credentials. Returns nil when the file is missing — the
// videocall feature still works P2P-only, just without symmetric-NAT
// fallback.
//
// Expected env keys:
//
//	TURN_SECRET      shared HMAC secret (matches coturn `static-auth-secret`)
//	TURN_PUBLIC_HOST hostname or IP clients reach the TURN server on
//	TURN_PORT        UDP listen port (default 3478)
//	TURN_PORT_TLS    TLS-TCP listen port (optional; default empty = TLS off)
func loadTURNConfig() *videocall.TURNConfig {
	const envPath = "/etc/vpsm/coturn.env"
	b, err := os.ReadFile(envPath)
	if err != nil {
		return nil
	}
	kv := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			continue
		}
		k := strings.TrimSpace(line[:eq])
		v := strings.TrimSpace(line[eq+1:])
		// Drop surrounding quotes if present (compose .env syntax).
		v = strings.Trim(v, "\"'")
		kv[k] = v
	}
	secret := kv["TURN_SECRET"]
	host := kv["TURN_PUBLIC_HOST"]
	if secret == "" || host == "" {
		return nil
	}
	port := kv["TURN_PORT"]
	if port == "" {
		port = "3478"
	}
	urls := []string{
		fmt.Sprintf("turn:%s:%s?transport=udp", host, port),
		fmt.Sprintf("turn:%s:%s?transport=tcp", host, port),
		"stun:stun.l.google.com:19302",
	}
	if portTLS := kv["TURN_PORT_TLS"]; portTLS != "" {
		urls = append(urls, fmt.Sprintf("turns:%s:%s?transport=tcp", host, portTLS))
	}
	return &videocall.TURNConfig{Secret: secret, Hosts: urls}
}

// inviteSessionsAdapter adapts the existing sessions.Store to the minimal
// interface videocall needs for single-use invite tracking. Reuses the
// store's tombstone semantics — once revoked, the jti can never be
// re-added (defends against replay).
type inviteSessionsAdapter struct{ s *sessions.Store }

func (a inviteSessionsAdapter) Add(jti, user string, expiresAt int64) {
	now := time.Now().Unix()
	a.s.Add(sessions.Session{
		JTI:       jti,
		User:      user,
		IssuedAt:  now,
		LastSeen:  now,
		ExpiresAt: expiresAt,
	})
}

func (a inviteSessionsAdapter) IsValid(jti string) bool {
	return a.s.Has(jti) && !a.s.HasTombstone(jti)
}

func (a inviteSessionsAdapter) Tombstone(jti string) {
	a.s.Revoke(jti)
}

// pullRefAllowed restricts `docker pull` to a curated set of registries. It
// blocks both arbitrary URLs (an attacker-controlled mirror) and shell
// metacharacters. Refs with no domain prefix (`nginx:latest`) fall through to
// Docker Hub (docker.io) by default — those are allowed.
func pullRefAllowed(ref string) bool {
	if ref == "" || len(ref) > 256 {
		return false
	}
	for _, ch := range ref {
		if ch < 0x20 || ch == ' ' || ch == ';' || ch == '|' || ch == '&' || ch == '$' || ch == '`' || ch == '\\' || ch == '"' || ch == '\'' {
			return false
		}
	}
	allowed := []string{
		"docker.io/", "ghcr.io/", "quay.io/", "lscr.io/",
		"registry.k8s.io/", "mcr.microsoft.com/", "gcr.io/",
		"public.ecr.aws/", "registry.gitlab.com/",
	}
	// A ref with no `/` before the `:` is Docker Hub shorthand (`nginx:latest`,
	// `library/nginx`). If it contains a `.` or a `:port` before the first `/`
	// it names an explicit host — and that requires the allowlist.
	slash := strings.Index(ref, "/")
	if slash <= 0 {
		return true
	}
	host := ref[:slash]
	if !strings.ContainsAny(host, ".:") {
		// `library/nginx` style — Docker Hub
		return true
	}
	for _, p := range allowed {
		if strings.HasPrefix(ref, p) {
			return true
		}
	}
	return false
}

// ---------- Alerting config (admin only) ----------

// handleAlertingConfig GET returns the current config and the list of
// available users; POST updates it (with validation) and persists it to
// config.json.
// Restricted to r.cfg.Primary — only the primary admin may touch global alerts.

// handleForwardAuth serves the reverse proxy's forward-auth middleware. It
// ALWAYS returns 200. With a valid session it sets the identity headers the
// proxy passes on to the dashboard app; without one it returns 200 and no
// headers, and the app falls back to its own login form.
//
// Why not 401? In forward-auth, a 401 blocks the whole request — including the
// login page that was supposed to be the fallback. Returning 200 with no
// headers preserves it.
//
// Validation is inline rather than going through the usual helper, because
// this route sits outside the auth middleware: the proxy calls it with
// whatever cookies the user has, valid or not, and this handler decides.
func (r *Router) handleForwardAuth(w http.ResponseWriter, req *http.Request) {
	token := ""
	if h := req.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		token = strings.TrimPrefix(h, "Bearer ")
	}
	if token == "" {
		if c, err := req.Cookie("vpsm_token"); err == nil {
			token = c.Value
		}
	}
	if token == "" {
		w.WriteHeader(http.StatusOK)
		return
	}
	user, _, err := r.auth.ParseWithJTI(token)
	if err != nil || user == "" {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.Header().Set("X-WEBAUTH-USER", user)
	w.Header().Set("X-WEBAUTH-EMAIL", user+"@northwind.example")
	w.Header().Set("X-WEBAUTH-NAME", user)
	role := "Viewer"
	if r.isPrimary(user) {
		role = "Admin"
	}
	w.Header().Set("X-WEBAUTH-ROLE", role)
	w.WriteHeader(http.StatusOK)
}

// ---------- User prefs (UI layout etc.) ----------

// handleUserPrefs serves GET and POST on /api/user/prefs.
//
// GET: the query "?key=processes" returns {"key":"processes","value":<json>}.
// With no key it returns the whole map {processes:..., otherKey:...}.
//
// POST: a body of {"key":"processes","value":{...}} merges — it reads the
// current file, replaces that key only and writes atomically (write tmp +
// rename).
//
// Scope: per user. Each user writes to data/users/<user>/prefs.json (0o600).
// Without auth.UserFrom() the middleware has already rejected the request, so
// the handler assumes it is valid.
func (r *Router) handleUserPrefs(w http.ResponseWriter, req *http.Request) {
	user := auth.UserFrom(req)
	if user == "" {
		writeErr(w, 401, "unauthorized")
		return
	}
	uname, err := scope.New(user)
	if err != nil {
		writeErr(w, 400, "invalid user")
		return
	}
	dir := filepath.Join(r.cfg.DataDir, "users", string(uname))
	path := filepath.Join(dir, "prefs.json")

	readPrefs := func() (map[string]any, error) {
		b, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				return map[string]any{}, nil
			}
			return nil, err
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			return map[string]any{}, nil // corrupted file — start from scratch
		}
		return m, nil
	}

	switch req.Method {
	case http.MethodGet:
		m, err := readPrefs()
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		key := req.URL.Query().Get("key")
		if key != "" {
			writeJSON(w, map[string]any{"key": key, "value": m[key]})
			return
		}
		writeJSON(w, m)
	case http.MethodPost:
		var body struct {
			Key   string `json:"key"`
			Value any    `json:"value"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			writeErr(w, 400, "bad json")
			return
		}
		if body.Key == "" {
			writeErr(w, 400, "key required")
			return
		}
		m, err := readPrefs()
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		m[body.Key] = body.Value
		if err := os.MkdirAll(dir, 0o700); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		out, err := json.MarshalIndent(m, "", "  ")
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, out, 0o600); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		if err := os.Rename(tmp, path); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		writeJSON(w, map[string]any{"status": "ok"})
	default:
		writeErr(w, 405, "method not allowed")
	}
}
