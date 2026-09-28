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

var webFS = webassets.FS
var buildStamp = webassets.BuildStamp

const telemetryFork = "server-control-panel"

var schedulerScreenRegisterOnce sync.Once

var dockerScreenRegisterOnce sync.Once

var systemScreenRegisterOnce sync.Once

var securityScreenRegisterOnce sync.Once

var networkScreenRegisterOnce sync.Once

var miscScreenRegisterOnce sync.Once

var alertsScreenRegisterOnce sync.Once

type Router struct {
	cfg                  *config.Config
	cfgMu                sync.Mutex
	auth                 *auth.Service
	limiter              *auth.Limiter
	lockout              *auth.Lockout
	mobileRefreshLimiter *auth.Limiter
	audit                *auth.AuditLog
	docker               *docksvc.Client
	gameMgr              *gameservers.Manager
	secrets              *secrets.Store
	netUsage             *netusage.Tracker
	ring                 *metrics.Ring
	alerts               *metrics.Engine
	metricReg            *metrics.Registry
	metricHist           *metrics.MetricHistory
	fires                []metrics.Fire
	firesMu              sync.Mutex
	mux                  *http.ServeMux
	whatsappMgr          *whatsapp.Manager
	videocall            *videocall.Service
	webpush              *webpush.Store
	sessionsSt           *sessions.Store
	handler              http.Handler
	startTime            int64
	sessionOwn           *ptysvc.Ownership
	sessReg              *ptysvc.Registry
	queue                *queue.Queue
	queueRunners         map[string]queue.Runner
	deployStore          *deploy.Store

	inventoryStore     *inventory.Store
	inventoryPoller    *inventory.Poller
	pveConfig          *pve.Config
	inventoryNow       func() time.Time
	nodeVaultFn        func() (nodeVault, error)
	pveDial            func(tokenValue string) (hypervisorOps, error)
	scheduler          *scheduler.Scheduler
	jiraWorkWatchers   map[string]*jiraWorkWatcher
	jiraWorkWatchersMu sync.Mutex
	aiPrompts          *aiprompts.Registry

	claudeAccts *claudeacct.Store

	notify          *notify.Router
	pushDevices     *mobilebff.DeviceTokenStore
	pushDevicePrefs *mobilebff.DevicePrefsStore
	fcmSender       *fcmpush.Sender
	sentinel        *hypervisorSentinel
	sentinelSink    func(notify.Event)

	agentStatus     *agentStatusStore
	agentCWD        *agentCWDStore
	agentHookSecret string
	agentCostCache  map[string]agentCostEntry
	agentBudget     *agentBudgetStore
	budgetNotified  map[string]bool
	telSink         *telemetry.Sink

	webauthnRP *webauthn.WebAuthn

	mobileHub   *mobilebff.Hub
	stopWorkers context.CancelFunc

	mobileOpsHealthStop func()
}

func wsCheckOriginSameHostLegacy(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
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

var processStartTime = webassets.ProcessStartTime

var wsUpgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin:     wsorigin.CheckSameHost,
}

func NewRouter(cfg *config.Config) (*Router, error) {
	authSvc := auth.New(cfg.JWTSecret, credsFromConfig(cfg))
	if cfg.SupabaseURL != "" && cfg.SupabaseAnonKey != "" {
		backend := auth.BackendSupabase
		if v := os.Getenv("PANEL_AUTH_BACKEND"); v != "" {
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
		cfg:                  cfg,
		auth:                 authSvc,
		limiter:              auth.NewLimiter(10, 60*time.Second),
		lockout:              auth.NewLockout(5, 30*time.Second, 30*time.Minute, time.Hour),
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
	if own, err := ptysvc.LoadOwnership(filepath.Join(cfg.DataDir, "session-ownership.json")); err == nil {
		r.sessionOwn = own
		ptysvc.SetActiveOwnership(own)
	} else {
		log.Printf("session ownership registry disabled: %v", err)
	}
	if reg, err := ptysvc.LoadRegistry(filepath.Join(cfg.DataDir, "session-registry.json")); err == nil {
		r.sessReg = reg
	} else {
		log.Printf("session registry disabled: %v", err)
	}
	ptysvc.InitSessionBackend(cfg.DataDir, r.sessReg)
	go ptysvc.EnsureLiveSessionRecorders(cfg.DataDir, r.sessReg, r.sessionOwn, cfg.Primary)
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

	r.aiPrompts = aiprompts.New(cfg.DataDir)
	r.deployStore = deploy.Open(cfg.DataDir)

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

	if cas, err := claudeacct.Open(cfg.DataDir, cfg.ClaudeHome); err == nil {
		r.claudeAccts = cas
	} else {
		log.Printf("claude accounts disabled: %v", err)
	}

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
		register(queue.AppDeployRunner{DataDir: cfg.DataDir})
		register(queue.DeployPreviewReapRunner{DataDir: cfg.DataDir})
		register(queue.MobileUploadStagingReapRunner{DataDir: cfg.DataDir})
		register(queue.ShellRunner{})
		register(queue.DockerRestartRunner{})
		register(queue.DockerComposeRestartRunner{})
		register(queue.SystemdRestartRunner{})
		register(queue.DockerPruneRunner{})
		register(queue.HTTPCheckRunner{})
		register(queue.SSLCheckRunner{})
		register(queue.DiskCheckRunner{})
		register(queue.SecurityAuditRunner{})
		register(queue.RootkitScanRunner{})
		register(queue.IntegrityCheckRunner{})
		register(queue.TrivyScanRunner{})
		register(queue.Fail2banReportRunner{})
		register(queue.AuditReportRunner{})
		register(queue.CleanupRunner{})
		register(queue.CertRenewRunner{})
		register(queue.RcloneSyncRunner{})
		register(queue.DockerComposeUpRunner{})
		register(queue.GitPullRunner{})
		register(queue.AptUpdateCheckRunner{})
		register(queue.RebootRunner{})
		register(queue.SelfDeployRunner{})
		register(queue.DBBackupRunner{DataDir: cfg.DataDir})
		register(queue.WatchdogRunner{DataDir: cfg.DataDir})
		register(queue.SessionBackupRunner{Backup: r.runSessionBackupJob})
		register(queue.AgentRoutineRunner{Spawn: r.runAgentRoutineJob})
		register(jiraai.NewRunner(
			r.jiraClientForOwner,
			r.jiraRepoMapFor,
			r.aiPrompts,
			r.jobsConfigDir,
			func() string { return aimodel.For(aimodel.JiraAI, r.cfg.AIModels.JiraAI) },
		))
		if os.Getenv("PANEL_DETACH_JOBS") != "0" {
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

	if r.queue != nil {
		schedPath := filepath.Join(cfg.DataDir, "scheduler", "jobs.json")
		if sc, err := scheduler.New(schedPath, scheduler.QueueEnqueuer{Q: r.queue}); err == nil {
			r.scheduler = sc
			r.scheduler.SetAlerter(r.schedulerAlerter())
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

	if wp, err := webpush.Open(filepath.Join(cfg.DataDir, "videocalls")); err == nil {
		r.webpush = wp
	} else {
		log.Printf("webpush disabled: %v", err)
	}

	r.initNotify()

	r.initPasskey()

	r.agentStatus = newAgentStatusStore(filepath.Join(cfg.DataDir, "session-status.json"))
	r.agentCWD = newAgentCWDStore(filepath.Join(cfg.DataDir, "session-cwd.json"))
	ptysvc.SetActiveCWDResolver(r.agentCWD.Get)
	r.agentHookSecret = r.loadAgentHookSecret()
	r.agentCostCache = map[string]agentCostEntry{}
	r.agentBudget = newAgentBudgetStore(filepath.Join(cfg.DataDir, "agent-budget.json"))
	r.budgetNotified = map[string]bool{}
	r.ensureAgentHooks()

	if r.secrets != nil {
		ingestWhatsAppSecretsPut(cfg.DataDir, r.secrets, "sam")

		selfBaseURL := strings.TrimSpace(os.Getenv("PANEL_SELF_BASE_URL"))
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
			ContainerRoot: "/var/lib/panel-whatsapp",
			Vault:         r.secrets,
			SelfBaseURL:   selfBaseURL,
		})
		if err != nil {
			log.Printf("whatsapp manager init: %v", err)
		} else {
			r.whatsappMgr = mgr
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
					if _, err := mgr.ForUser(su); err != nil {
						log.Printf("whatsapp bootstrap eager-start %s: %v", su, err)
					}
				}
			}
		}

	}

	if vc, err := videocall.Open(videocall.Options{
		DataDir: cfg.DataDir,
		TURN:    loadTURNConfig(),
		Push:    r.webpush,
		FCM:     r.fcmSender,
	}); err != nil {
		log.Printf("videocall init: %v", err)
	} else {
		r.videocall = vc
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
		vc.InviteIssuer = r.auth
		if r.auth.Sessions() != nil {
			vc.InviteSessionsCk = inviteSessionsAdapter{r.auth.Sessions()}
		}
		if recs, err := videocall.OpenRecordingStore(cfg.DataDir, "/var/lib/panel-videocalls/recordings"); err == nil {
			vc.Recordings = recs
		} else {
			log.Printf("videocall recordings disabled: %v", err)
		}
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

	r.buildMetricRegistry()

	r.netUsage = netusage.New(r.cfg.SingboxDevicePortsPath, filepath.Join(r.cfg.DataDir, "netusage-totals.json"))

	r.mux.HandleFunc("/api/auth/login", r.handleLogin)
	r.mux.HandleFunc("/api/auth/refresh-cookie", r.handleRefreshCookie)
	r.mux.HandleFunc("/api/health", r.handleHealth)
	r.mux.HandleFunc("/api/health/detailed", r.handleHealthDetailed)
	r.mux.HandleFunc("/.well-known/assetlinks.json", r.handleAssetLinks)
	r.mux.HandleFunc("/metrics", r.handlePrometheusMetrics)
	r.mux.HandleFunc("/recovery", r.handleRecoveryPage)
	r.mux.HandleFunc("/recovery/auth", r.handleRecoveryAuth)
	r.mux.HandleFunc("/recovery/term", r.handleRecoveryTerm)
	r.mux.HandleFunc("/recovery/ws/pty", r.handleRecoveryPTY)
	r.mux.HandleFunc("/recovery/action/", r.handleRecoveryAction)
	r.mux.HandleFunc("/recovery/logout", r.handleRecoveryLogout)
	r.mux.HandleFunc("/recovery/claude/status", r.handleRecoveryClaudeStatus)
	r.mux.HandleFunc("/recovery/ws/claude", r.handleRecoveryClaudePTY)
	r.mux.HandleFunc("/recovery/renew", r.handleRecoveryRenew)
	if r.whatsappMgr != nil {
		r.mux.HandleFunc("/api/whatsapp/webhook/", r.whatsappMgr.HandleWebhook)
		r.mux.HandleFunc("/_internal/alert", alert.NewHandler(r.whatsappMgr, &r.cfg.Alerting))
	}
	r.mux.HandleFunc("/api/forward-auth", r.handleForwardAuth)

	r.mux.HandleFunc("/api/agent/hook", r.handleAgentHook)

	r.mux.Handle("/_docs", r.auth.Middleware(http.HandlerFunc(r.handleDocsReport)))
	r.mux.Handle("/_graph", r.auth.Middleware(http.HandlerFunc(r.handleKnowledgeGraph)))
	r.mux.Handle("/android/install", r.auth.Middleware(http.HandlerFunc(r.handleAndroidInstallPage)))
	r.mux.Handle("/_code/", r.auth.Middleware(r.codeServerProxy()))
	r.mux.Handle("/_port/", r.auth.Middleware(r.portForwardProxy()))
	r.mux.HandleFunc("/manifest.webmanifest", r.handleManifest)
	r.mux.HandleFunc("/sw.js", r.handleServiceWorker)
	r.mux.HandleFunc("/icon-192.png", r.handleIcon)
	r.mux.HandleFunc("/icon-512.png", r.handleIcon)
	r.mux.HandleFunc("/icon-512-maskable.png", r.handleIcon)
	r.mux.HandleFunc("/apple-touch-icon.png", r.handleIcon)
	r.mux.HandleFunc("/apple-touch-icon-152.png", r.handleIcon)
	r.mux.HandleFunc("/apple-touch-icon-167.png", r.handleIcon)
	r.mux.HandleFunc("/apple-touch-icon-precomposed.png", r.handleIcon)

	mobilebff.MountPublic(r.mux, mobilebff.Deps{Passkey: r})

	r.mobileHub = mobilebff.NewHub()
	r.mux.HandleFunc("/ws/mobile-events", mobilebff.HandleMobileEventsWS(r.auth, r.mobileHub))

	if r.videocall != nil {
		r.mux.HandleFunc("/api/videocall/join-by-pin", r.videocall.HandleJoinByPIN)
		r.mux.HandleFunc("/ws/videocall-guest", r.videocall.HandleGuestWS)
		r.mux.HandleFunc("/join", r.handleJoinPage)
	}

	protected := http.NewServeMux()

	if ts, err := telemetry.NewSink(filepath.Join(r.cfg.DataDir, "telemetry")); err != nil {
		log.Printf("telemetry: sink unavailable (%v) - /api/telemetry NOT registered", err)
	} else {
		r.telSink = ts
		protected.HandleFunc("/api/telemetry", telemetry.Handler(ts, telemetryFork))
	}

	protected.HandleFunc("/api/auth/me", r.handleMe)

	var mobileIdem *mobilebff.Idempotency
	if r.cfg != nil && strings.TrimSpace(r.cfg.DataDir) != "" {
		mobileIdem = mobilebff.NewIdempotency(r.cfg.DataDir)
	}

	mobileDeps := mobilebff.Deps{
		Auth:           r.auth,
		Cfg:            r.cfg,
		Idem:           mobileIdem,
		WhatsAppMgr:    r.whatsappMgr,
		SessionOwn:     r.sessionOwn,
		Audit:          r.audit,
		Queue:          r.queue,
		Alerts:         r.alerts,
		Hub:            r.mobileHub,
		Sessions:       r.auth.Sessions(),
		Notify:         r.notify,
		HealthDetailed: r.healthDetailedSnapshot,
		SysStats:       collectStatsCached,
		Videocall:      r.videocall,

		JiraFor: r.jiraClientForOwner,
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
			AuditEvent: func(user, action, target string) {
				if r.audit == nil {
					return
				}
				r.audit.Append(auth.Event{Time: time.Now().Unix(), User: user, Action: action, Target: target})
			},
		})
	})
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
	systemScreenRegisterOnce.Do(func() {
		screens.RegisterSystem(screens.SystemDeps{
			ListHistory: func() []metrics.Point {
				return r.ring.Snapshot()
			},
			ListProcesses: func(ctx context.Context) ([]procs.Info, error) {
				infos, _, err := procs.List(ctx, procs.Filter{}, procs.SortCPU, 0, 0)
				return infos, err
			},
			KillProcess: func(ctx context.Context, pid int32) error {
				sig, err := procs.SignalByName("SIGTERM")
				if err != nil {
					return err
				}
				return procs.Signal(ctx, pid, sig)
			},
			ListPorts: func() ([]sysextra.Port, error) {
				return sysextra.Listening()
			},
			ListUnits: func() ([]sysextra.Unit, error) {
				return sysextra.ListUnits()
			},
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
			AuditEvent: func(user, action, target string) {
				if r.audit == nil {
					return
				}
				r.audit.Append(auth.Event{Time: time.Now().Unix(), User: user, Action: action, Target: target})
			},
		})
	})
	securityScreenRegisterOnce.Do(func() {
		configPath := func() string { return filepath.Join(r.cfg.DataDir, "config.json") }

		screens.RegisterSecurity(screens.SecurityDeps{
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
			RevokeSession: func(sessionID string) error {
				if r.auth.Sessions() == nil {
					return fmt.Errorf("sessions unavailable")
				}
				r.auth.Sessions().Revoke(sessionID)
				return nil
			},
			ListAuditEvents: func(filter screens.AuditFilter) ([]screens.AuditRow, error) {
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
	networkScreenRegisterOnce.Do(func() {
		screens.RegisterNetwork(screens.NetworkDeps{
			UFWStatus: func() (bool, string, error) {
				out, err := execCmd("ufw", "status", "numbered")
				if err != nil {
					return false, out, err
				}
				return strings.Contains(out, "Status: active"), out, nil
			},
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
	miscScreenRegisterOnce.Do(func() {
		screens.RegisterMisc(screens.MiscDeps{
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
				a.Autodeploy = true
				return r.deployStore.Create(a)
			},
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
	r.mobileOpsHealthStop = mobilebff.StartOpsHealthPublisher(mobileDeps)

	if _, err := gameservers.MigrateInventoryToNode(filepath.Join(r.cfg.DataDir, "gameservers.json")); err != nil {
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
	protected.HandleFunc("/api/auth/mobile-sessions", r.handleListMobileSessions)
	protected.HandleFunc("/api/auth/mobile-sessions/approve", r.handleApproveMobileCredential)
	protected.HandleFunc("/api/auth/mobile-sessions/deny", r.handleDenyMobileCredential)
	protected.HandleFunc("/api/auth/mobile-sessions/revoke", r.handleRevokeMobileSession)
	protected.HandleFunc("/api/auth/change-password", r.handleChangePassword)
	protected.HandleFunc("/api/auth/totp/status", r.handleTOTPStatus)
	protected.HandleFunc("/api/auth/totp/enroll", r.handleTOTPEnroll)
	protected.HandleFunc("/api/auth/totp/confirm", r.handleTOTPConfirm)
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

	protected.HandleFunc("/api/procs", r.handleProcs)
	protected.HandleFunc("/api/procs/signal", r.handleProcsSignal)
	protected.HandleFunc("/ws/procs", r.handleProcsStream)

	protected.HandleFunc("/api/todos", r.handleTodos)
	protected.HandleFunc("/api/todos/", r.handleTodoByID)
	protected.HandleFunc("/api/todos/seed", r.handleTodosSeed)

	protected.HandleFunc("/api/queue", r.handleQueue)
	protected.HandleFunc("/api/queue/", r.handleQueueByID)
	protected.HandleFunc("/ws/queue/", r.handleQueueWS)

	protected.HandleFunc("/api/scheduler/jobs", r.handleSchedulerJobs)
	protected.HandleFunc("/api/scheduler/jobs/", r.handleSchedulerJobByID)
	protected.HandleFunc("/api/scheduler/preview", r.handleSchedulerPreview)
	protected.HandleFunc("/api/scheduler/catalog", r.handleSchedulerCatalog)
	protected.HandleFunc("/api/scheduler/options", r.handleSchedulerOptions)
	protected.HandleFunc("/api/fs/browse", r.handleFSBrowse)
	protected.HandleFunc("/api/backup/remotes", r.handleBackupRemotes)
	protected.HandleFunc("/api/backup/remote-browse", r.handleBackupRemoteBrowse)
	protected.HandleFunc("/api/backup/remote-connect", r.handleBackupRemoteConnect)
	protected.HandleFunc("/api/backup/remote-authorize", r.handleBackupRemoteAuthorize)
	protected.HandleFunc("/api/backup/remote-authorize/status", r.handleBackupRemoteAuthorizeStatus)

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
	_ = jira.ErrNotConfigured

	protected.HandleFunc("/api/docker/compose/create", r.handleComposeCreate)

	protected.HandleFunc("/api/docker/info", r.handleDockerInfo)
	protected.HandleFunc("/api/docker/disk-usage", r.handleDiskUsage)
	protected.HandleFunc("/api/docker/containers", r.handleContainers)
	protected.HandleFunc("/api/docker/containers/", r.handleContainerAction)
	protected.HandleFunc("/api/docker/images", r.handleImages)
	protected.HandleFunc("/api/docker/volumes", r.handleVolumes)
	protected.HandleFunc("/api/docker/networks", r.handleNetworks)
	protected.HandleFunc("/api/docker/compose", r.handleCompose)
	protected.HandleFunc("/api/docker/compose/action", r.handleComposeAction)
	protected.HandleFunc("/api/nodes", r.handleNodes)
	protected.HandleFunc("/api/nodes/", r.handleNodes)
	protected.HandleFunc("/api/proxmox", r.handleProxmox)
	protected.HandleFunc("/api/proxmox/", r.handleProxmox)
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
	protected.HandleFunc("/api/dev/ports", r.handleDevPorts)
	protected.HandleFunc("/api/agent/sessions", r.handleAgentSessions)
	protected.HandleFunc("/api/docker/compose/file", r.handleComposeFile)
	protected.HandleFunc("/api/docker/prune", r.handlePrune)
	protected.HandleFunc("/api/docker/pull", r.handlePull)
	protected.HandleFunc("/ws/logs/", r.handleLogStream)
	protected.HandleFunc("/ws/stats/", r.handleStatsStream)

	protected.HandleFunc("/api/browser-instances", r.handleBrowserInstances)
	protected.HandleFunc("/api/browser-instances/", r.handleBrowserInstanceAction)

	protected.HandleFunc("/api/terminal/state", r.handleTerminalState)
	protected.HandleFunc("/api/terminal/snapshot/", r.handleTerminalSnapshot)
	protected.HandleFunc("/api/terminal/workspace/", r.handleTerminalWorkspace)

	protected.HandleFunc("/api/session/bandwidth", r.handleSessionBandwidth)
	protected.HandleFunc("/api/session/bandwidth/reset", r.handleSessionBandwidthReset)

	protected.HandleFunc("/api/claude/overview", r.handleClaude)
	protected.HandleFunc("/api/claude/session/fork", r.handleClaudeSessionFork)
	protected.HandleFunc("/api/claude/session/restart", r.handleClaudeSessionRestart)
	protected.HandleFunc("/api/claude/accounts", r.handleClaudeAccounts)
	protected.HandleFunc("/api/claude/accounts/usage", r.handleClaudeAccountsUsage)
	protected.HandleFunc("/api/claude/accounts/ratelimits", r.handleClaudeAccountsRateLimits)
	protected.HandleFunc("/api/claude/accounts/assign", r.handleClaudeAccountAssign)
	protected.HandleFunc("/api/claude/accounts/login-terminal", r.handleClaudeAccountLoginTerminal)
	protected.HandleFunc("/api/claude/accounts/session-swap", r.handleClaudeAccountSessionSwap)
	protected.HandleFunc("/api/private-ai/tokens", r.handlePrivateAITokens)
	protected.HandleFunc("/api/private-ai/tokens/", r.handlePrivateAITokenAction)
	protected.HandleFunc("/api/private-ai/status", r.handlePrivateAIStatus)

	protected.HandleFunc("/api/adguard/status", r.handleAdguardStatus)
	protected.HandleFunc("/api/adguard/protection", r.handleAdguardProtection)

	protected.HandleFunc("/api/tunnel/devices", r.handleTunnelDevices)
	protected.HandleFunc("/api/tunnel/devices/", r.handleTunnelDeviceAction)
	protected.HandleFunc("/api/tunnel/usage", r.handleTunnelUsage)

	protected.HandleFunc("/api/datasaver/status", r.handleDatasaverStatus)
	protected.HandleFunc("/api/datasaver/settings", r.handleDatasaverSettings)
	protected.HandleFunc("/api/datasaver/bypass", r.handleDatasaverBypass)
	protected.HandleFunc("/api/datasaver/ca", r.handleDatasaverCA)
	protected.HandleFunc("/api/ai/prompts", r.handleAIPrompts)
	protected.HandleFunc("/api/exec", r.handleExec)
	protected.HandleFunc("/api/config", r.handleConfig)
	protected.HandleFunc("/api/panel/health", r.handlePanelHealth)

	protected.HandleFunc("/api/audit/tail", r.handleAuditTail)
	protected.HandleFunc("/api/audit/search", r.handleAuditSearch)
	protected.HandleFunc("/api/audit/actions", r.handleAuditActions)

	r.mux.HandleFunc("/api/stt/health", r.handleSTTHealth)
	r.mux.HandleFunc("/ws/stt/transcribe", r.handleSTTTranscribe)

	protected.HandleFunc("/api/metrics/rules", r.handleAlertList)
	protected.HandleFunc("/api/metrics/rules/add", r.handleAlertAdd)
	protected.HandleFunc("/api/metrics/rules/remove", r.handleAlertRemove)
	protected.HandleFunc("/api/metrics/fires", r.handleAlertFires)
	protected.HandleFunc("/api/metrics/catalog", r.handleMetricsCatalog)
	protected.HandleFunc("/api/metrics/snapshot", r.handleMetricsSnapshot)
	protected.HandleFunc("/api/metrics/series", r.handleMetricsSeries)
	protected.HandleFunc("/api/ai/suggest-alert", r.handleAISuggestAlert)

	protected.Handle("/api/files/", http.StripPrefix("/api/files", files.Handler()))

	protected.Handle("/api/git/", http.StripPrefix("/api/git", gitsvc.Handler(r.cfg, r.audit, r.secrets)))

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
				scope.NewUserVault(r.secrets, u).Handler(scope.HandlerOpts{
					Audit:            func(action, target string) { r.auditEvent(req, user, action, target) },
					AllowSystemGroup: r.isPrimary(user),
					MaxValueBytes:    64 << 10,
				}).ServeHTTP(w, req)
			})))
	}

	protected.HandleFunc("/ws/shell", r.handleHostShell)
	protected.HandleFunc("/ws/container/", r.handleContainerShell)
	protected.HandleFunc("/api/terminal/sessions", r.handleTerminalSessions)
	protected.HandleFunc("/api/terminal/create", r.handleTerminalCreate)
	protected.HandleFunc("/api/terminal/code-restore-ping", r.handleCodeRestorePing)
	protected.HandleFunc("/api/terminal/assign-session", r.handleTerminalAssignSession)
	protected.HandleFunc("/api/terminal/scrollback", r.handleTerminalScrollback)
	protected.HandleFunc("/api/terminal/raw-log", r.handleTerminalRawLog)
	protected.HandleFunc("/api/terminal/history", r.handleTerminalHistory)
	protected.HandleFunc("/api/terminal/kill-session", r.handleTerminalKillSession)
	protected.HandleFunc("/api/claude/versions", r.handleClaudeVersions)
	protected.HandleFunc("/api/claude/recovery/restart", r.handleClaudeRecoveryRestart)
	protected.HandleFunc("/api/terminal/upload", r.handleTerminalUpload)
	protected.HandleFunc("/api/terminal/paste-image", r.handleTerminalUpload)
	protected.HandleFunc("/api/terminal/rename-session", r.handleTerminalRenameSession)
	protected.HandleFunc("/api/terminal/preview", r.handleTerminalPreview)
	protected.HandleFunc("/api/terminal/backup", r.handleTerminalBackup)
	protected.HandleFunc("/api/terminal/backups", r.handleTerminalBackupsList)
	protected.HandleFunc("/api/terminal/restore", r.handleTerminalRestore)
	protected.HandleFunc("/api/terminal/backup-delete", r.handleTerminalBackupDelete)

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
		r.mux.HandleFunc("/api/whatsapp/avatar/", func(w http.ResponseWriter, req *http.Request) {
			notFound := func() {
				var jb [1]byte
				_, _ = rand.Read(jb[:])
				time.Sleep(time.Duration(jb[0]) * 20 * time.Microsecond)
				http.NotFound(w, req)
			}
			tok, _ := req.Cookie("panel_token")
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

	if r.videocall != nil {
		protected.HandleFunc("/api/videocall/rooms", r.videocall.HandleRooms)
		protected.HandleFunc("/api/videocall/members", r.videocall.HandleMembers)
		protected.HandleFunc("/api/videocall/turn", r.videocall.HandleTURN)
		protected.HandleFunc("/api/videocall/invite", r.videocall.HandleInvite)
		protected.HandleFunc("/api/videocall/invite/consume", r.videocall.HandleInviteConsume)
		protected.HandleFunc("/api/videocall/history", r.videocall.HandleHistory)
		protected.HandleFunc("/api/videocall/sessions", r.videocall.HandleRecordSession)
		protected.HandleFunc("/api/videocall/devices", r.videocall.HandleDevices)
		protected.HandleFunc("/api/videocall/push/public-key", r.videocall.HandlePushPublicKey)
		protected.HandleFunc("/api/videocall/push/subscribe", r.videocall.HandlePushSubscribe)
		protected.HandleFunc("/api/videocall/push/unsubscribe", r.videocall.HandlePushUnsubscribe)
		protected.HandleFunc("/api/videocall/recordings", r.videocall.HandleRecordingsRouter)
		protected.HandleFunc("/api/videocall/recordings/", r.videocall.HandleRecordingItemRouter)
		protected.HandleFunc("/api/videocall/invite/whatsapp", r.videocall.HandleInviteWhatsApp)
		protected.HandleFunc("/api/videocall/pin", r.videocall.HandlePIN)
		protected.HandleFunc("/api/videocall/kick", r.videocall.HandleKick)
		protected.HandleFunc("/api/videocall/transcript/polish", r.videocall.HandleTranscriptPolish)
		protected.HandleFunc("/ws/videocall", r.videocall.HandleWS)
		protected.HandleFunc("/ws/videocall-presence", r.videocall.HandlePresenceWS)
	}

	protected.HandleFunc("/api/users", r.handleUsersList)
	protected.HandleFunc("/api/users/create", r.handleUserCreate)
	protected.HandleFunc("/api/users/delete", r.handleUserDelete)
	protected.HandleFunc("/api/users/set-admin", r.handleUserSetAdmin)
	protected.HandleFunc("/api/users/reset-password", r.handleUserResetPassword)
	protected.HandleFunc("/api/users/disable-2fa", r.handleUserDisable2FA)
	protected.HandleFunc("/api/users/revoke-sessions", r.handleUserRevokeSessions)

	protected.HandleFunc("/api/admin/alerting", r.handleAlertingConfig)
	protected.HandleFunc("/api/admin/alerting/test", r.handleAlertingTest)

	protected.HandleFunc("/api/admin/ai-models", r.handleAIModelsConfig)

	protected.HandleFunc("/api/notify/rules", r.handleNotifyRules)
	protected.HandleFunc("/api/notify/rules/delete", r.handleNotifyRuleDelete)
	protected.HandleFunc("/api/notify/channels", r.handleNotifyChannels)
	protected.HandleFunc("/api/notify/channels/delete", r.handleNotifyChannelDelete)
	protected.HandleFunc("/api/notify/channels/test", r.handleNotifyChannelTest)
	protected.HandleFunc("/api/notify/dryrun", r.handleNotifyDryRun)
	protected.HandleFunc("/api/notify/events", r.handleNotifyEvents)
	protected.HandleFunc("/api/notify/inbox", r.handleNotifyInbox)
	protected.HandleFunc("/api/notify/catalog", r.handleNotifyCatalog)

	protected.HandleFunc("/api/user/prefs", r.handleUserPrefs)

	r.mux.Handle("/api/", r.auth.Middleware(protected))
	r.mux.Handle("/ws/", r.auth.Middleware(protected))

	r.mux.Handle("/browser/", r.auth.Middleware(browserProxy()))

	r.mux.Handle("/browser-persistent/", r.auth.Middleware(r.browserPersistentProxy()))

	sub, _ := fs.Sub(webFS, "web")
	fileServer := http.FileServer(http.FS(sub))
	vendorCached := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		p := req.URL.Path
		if strings.HasPrefix(p, "/vendor/panel/app/") && strings.HasSuffix(p, ".js") &&
			!strings.HasSuffix(p, ".min.js") && req.URL.Query().Get("raw") != "1" {
			if mp, ok := webassets.MinifiedOf(strings.TrimPrefix(p, "/")); ok {
				r2 := req.Clone(req.Context())
				r2.URL.Path = "/" + mp
				req = r2
				p = r2.URL.Path
			}
		}
		if strings.HasPrefix(p, "/vendor/panel/") || p == "/tailwind.css" {
			etag := `"` + buildStamp + `"`
			if webassets.AcceptsBrotli(req) {
				etag = `"` + buildStamp + `-br"`
			}
			w.Header().Set("ETag", etag)
			w.Header().Set("Cache-Control", "public, no-cache")
			if req.Header.Get("If-None-Match") == etag {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			if body, err := fs.ReadFile(sub, strings.TrimPrefix(p, "/")); err == nil {
				if ct := mime.TypeByExtension(path.Ext(p)); ct != "" {
					w.Header().Set("Content-Type", ct)
				}
				if webassets.ServeBrotliAsset(w, req, p+":"+buildStamp, body) {
					return
				}
			}
		} else if strings.HasPrefix(p, "/vendor/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		fileServer.ServeHTTP(w, req)
	})
	r.mux.Handle("/", uvLeakRedirect(noStoreHTML(indexInjector(vendorCached))))

	handler := httpmw.Bandwidth(r.auth, r.fdroidGate(r.mux))
	handler = httpmw.MaxBody(handler)
	handler = httpmw.Compress(handler)
	handler = securityHeaders(handler)
	handler = withLogging(handler)
	r.handler = handler
	return r, nil
}

func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if demoMode() && !demoAllows(req) {
		demoDeny(w, req)
		return
	}
	r.handler.ServeHTTP(w, req)
}

func (r *Router) StartBackgroundWorkers(ctx context.Context) {
	if r == nil {
		return
	}
	if r.stopWorkers != nil {
		r.stopWorkers()
	}
	ctx, cancel := context.WithCancel(ctx)
	r.stopWorkers = cancel

	r.startMetricsCollector(ctx)
	r.startInventoryPoller(ctx)
	r.startSessionBackupCollector(ctx)
	r.startHypervisorWatcher(ctx)
	r.startAgentStatusAggregator(ctx)
	r.startLeakWatcher(ctx)
	if r.netUsage != nil {
		r.netUsage.Start(ctx)
	}
	r.startDatasaverWatcher(ctx)
}

func (r *Router) Shutdown(ctx context.Context) {
	if r == nil {
		return
	}
	if r.stopWorkers != nil {
		r.stopWorkers()
		r.stopWorkers = nil
	}
	if r.scheduler != nil {
		r.scheduler.Stop()
	}
	if r.queue != nil {
		r.queue.Shutdown(ctx)
	}
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

func securityHeaders(next http.Handler) http.Handler { return httpmw.SecurityHeaders(next) }

func indexInjector(next http.Handler) http.Handler { return webassets.IndexInjector(next) }

func noStoreHTML(next http.Handler) http.Handler { return httpmw.NoStoreHTML(next) }

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
			cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			snap := r.metricReg.Gather(cctx)
			cancel()
			v := snap.Values
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

	r.persistAlertRules()
}

func withLogging(next http.Handler) http.Handler { return httpmw.WithLogging(next) }

func writeJSON(w http.ResponseWriter, v interface{}) { httpx.WriteJSON(w, v) }

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

func writeErr(w http.ResponseWriter, code int, msg string) { httpx.WriteErr(w, code, msg) }

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

func (r *Router) auditEvent(req *http.Request, user, action, target string) {
	httpx.AuditEvent(r.audit, req, user, action, target)
}

func (r *Router) mustPrimary(w http.ResponseWriter, req *http.Request) (string, bool) {
	return httpx.MustPrimary(w, req, r.cfg, r.audit)
}

func (r *Router) isPrimary(user string) bool { return httpx.IsPrimary(r.cfg, user) }

func setAuthCookie(w http.ResponseWriter, token string) { httpx.SetAuthCookie(w, token) }
func clearAuthCookie(w http.ResponseWriter)             { httpx.ClearAuthCookie(w) }

func setSupabaseAccessCookie(w http.ResponseWriter, access string, ttlSeconds int) {
	httpx.SetSupabaseAccessCookie(w, access, ttlSeconds)
}
func clearSupabaseAccessCookie(w http.ResponseWriter)          { httpx.ClearSupabaseAccessCookie(w) }
func readSupabaseAccessCookie(req *http.Request) string        { return httpx.ReadSupabaseAccessCookie(req) }
func setSupabaseRefreshCookie(w http.ResponseWriter, r string) { httpx.SetSupabaseRefreshCookie(w, r) }
func clearSupabaseRefreshCookie(w http.ResponseWriter)         { httpx.ClearSupabaseRefreshCookie(w) }
func readSupabaseRefreshCookie(req *http.Request) string       { return httpx.ReadSupabaseRefreshCookie(req) }
func setCookieFlag(w http.ResponseWriter, set bool)            { httpx.SetCookieFlag(w, set) }

func setDeviceCookie(w http.ResponseWriter, secret string) { httpx.SetDeviceCookie(w, secret) }
func clearDeviceCookie(w http.ResponseWriter)              { httpx.ClearDeviceCookie(w) }
func readDeviceCookie(req *http.Request) string            { return httpx.ReadDeviceCookie(req) }

func execCmd(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, name, args...)
	out, err := c.CombinedOutput()
	return string(out), err
}

func execCmdCtx(ctx context.Context, name string, args ...string) (string, error) {
	c := exec.CommandContext(ctx, name, args...)
	out, err := c.CombinedOutput()
	return string(out), err
}

func execCmdLong(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	c := exec.CommandContext(ctx, name, args...)
	c.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")
	out, err := c.CombinedOutput()
	return string(out), err
}

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

func safeUploadName(name string) string {
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
		default:
			b.WriteRune('_')
		}
	}
	out := strings.Trim(b.String(), ".")
	for strings.Contains(out, "__") {
		out = strings.ReplaceAll(out, "__", "_")
	}
	if out == "" {
		return ""
	}
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
		file, header, err = req.FormFile("image")
		if err != nil {
			writeErr(w, 400, "field 'file' is required")
			return
		}
	}
	defer file.Close()

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
	writePasteInbox(filepath.Dir(paths.Uploads), outPath)
	writeJSON(w, map[string]any{
		"path": outPath,
		"name": filepath.Base(outPath),
		"size": int64(n) + written,
		"type": ct,
	})
}

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

func (r *Router) handleCron(w http.ResponseWriter, req *http.Request) {
	if _, ok := r.mustPrimary(w, req); !ok {
		return
	}
	switch req.Method {
	case http.MethodGet:
		out, err := execCmd("crontab", "-l")
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
	const composeRoot = "/opt/compose"
	dir := filepath.Join(composeRoot, body.ProjectName)
	if !strings.HasPrefix(dir, composeRoot+string(filepath.Separator)) {
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

func loadTURNConfig() *videocall.TURNConfig {
	const envPath = "/etc/panel/coturn.env"
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
	slash := strings.Index(ref, "/")
	if slash <= 0 {
		return true
	}
	host := ref[:slash]
	if !strings.ContainsAny(host, ".:") {
		return true
	}
	for _, p := range allowed {
		if strings.HasPrefix(ref, p) {
			return true
		}
	}
	return false
}

func (r *Router) handleForwardAuth(w http.ResponseWriter, req *http.Request) {
	token := ""
	if h := req.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		token = strings.TrimPrefix(h, "Bearer ")
	}
	if token == "" {
		if c, err := req.Cookie("panel_token"); err == nil {
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
			return map[string]any{}, nil
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
