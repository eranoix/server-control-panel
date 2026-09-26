package config

import (
	"fmt"
)

type User struct {
	Username     string `json:"username"`
	PasswordHash string `json:"password_hash"`
	TOTPSecret   string `json:"totp_secret,omitempty"`
	// RecoveryTOTPSecret is a SEPARATE TOTP secret used only by /recovery.
	// Independent from TOTPSecret so a compromise of the primary session/secret
	// doesn't grant access to the rollback/restart actions. Enrolled together
	// with the primary in the first-login wizard.
	RecoveryTOTPSecret string `json:"recovery_totp_secret,omitempty"`
	// SupabaseMFAEnabled is a local mirror of the MFA status held in Supabase.
	// It is kept because the admin user list (handleUsersList) has no access
	// token for other users and so cannot query /auth/v1/user live. Updated on
	// (a) a successful handleMFAEnrollVerify → true, (b) handleMFADisable →
	// false, (c) a successful handleLogin → resynced against the verified
	// factor reported by Supabase. The local TOTPSecret is no longer
	// populated; this is what the admin UI reads instead.
	SupabaseMFAEnabled bool `json:"supabase_mfa_enabled,omitempty"`

	// Admin marks this user as a system administrator — full privilege parity
	// with the Primary (the IsAdmin gate). It generalises the original
	// single-admin model (only the Primary was privileged) to multiple
	// admins. The Primary is ALWAYS an admin regardless of this flag; the flag
	// only promotes additional users. Editable through /api/users/set-admin
	// (gated by mustPrimary) or `vpsmctl admin add/remove`.
	Admin bool `json:"admin,omitempty"`

	// AppOnly marks the account as EXCLUSIVE to the native Android app: it
	// authenticates through the mobile path (MobileLogin/passkey) and is
	// refused at every entry point of the web panel. It exists because the
	// panel and the app share the SAME credential verifier
	// (auth.Service.VerifyDetailed) — without this marker, whoever gets into
	// one gets into the other, and there is no way to hand someone the app
	// without handing over the whole panel (root terminal, deploy,
	// code-server).
	//
	// The refusal happens EARLY in the panel handler and returns the same
	// generic invalid-credential message: saying "this account is app-only"
	// would confirm to an attacker that the user exists. The real reason goes
	// to the audit log, which is internal.
	//
	// omitempty: a normal account never writes the field — the config of
	// anyone who does not use this stays byte-identical to before.
	AppOnly bool `json:"app_only,omitempty"`
}

// Alerting controls the dispatch of Prometheus Alertmanager webhooks.
// When Enabled=true, /_internal/alert sends a WhatsApp message to ChatJID
// using the account of FromUser. MinSeverity filters out payloads below the
// level (critical < warning < info — empty = no filter). Loopback-only.
type Alerting struct {
	Enabled     bool   `json:"enabled,omitempty"`
	FromUser    string `json:"from_user,omitempty"`
	ChatJID     string `json:"chat_jid,omitempty"`
	MinSeverity string `json:"min_severity,omitempty"`
}

// AIModels routes each class of AI task to a model. An empty field
// means it inherits the process default (Opus) — byte-identical to the behaviour
// before tiering. Precedence applied in internal/aimodel: env > this config >
// tier default (only Suggest is downgraded by default, to haiku). Editable in the UI
// (mustPrimary gate) and persisted to config.json. omitempty: block absent →
// zero value → every tier inherits the default, backward compatible and without touching
// SchemaVersion.
//
//   - Suggest:     ai_suggest (alert name/description) — trivial.
//   - JiraAI:      ticket audit/refinement — standard/deep.
//
// Interactive panels are NOT tiered: they inherit the model the operator
// pre-defined in Claude Code's settings.json (Opus / full window). A legacy
// "interactive" field in an old config.json is simply ignored.
type AIModels struct {
	Suggest string `json:"suggest,omitempty"`
	JiraAI  string `json:"jira_ai,omitempty"`
	// Intake — the model of the intake pipeline (extraction + e-mail
	// classification), which talks to private-ai through the /v1/messages
	// passthrough. Resolved by aimodel.IntakeModel to a COMPLETE Anthropic id
	// (never ""): it accepts an alias (haiku/sonnet/opus) or a full id; empty
	// means the sonnet default.
	Intake string `json:"intake,omitempty"`
}

// CurrentSchemaVersion is the schema this binary writes. Boot rejects a
// config with SchemaVersion > Current (downgrade not supported). A config
// with SchemaVersion < Current is upgraded in-place by migrate.go before
// the router boots.
//
// Version map:
//
//	0 — original layout: shared data/whatsapp/, single-tenant everything,
//	    legacy top-level Username/PasswordHash. Treated as v1 for migration
//	    purposes when Username != "".
//	1 — same as 0, just explicit (never written; reserved).
//	2 — per-user isolation: data/users/<u>/..., vault keys are <u>:key,
//	    /var/lib/vpsm-whatsapp/<u>/, vpsm-whatsapp@<u>.service. Top-level
//	    Username/PasswordHash are removed; all users live in Users[].
const CurrentSchemaVersion = 2

type Config struct {
	// SchemaVersion of the on-disk config. 0 = legacy (pre-isolation);
	// migrate.go bumps to 2 the first time the daemon starts after the
	// v2 binary is deployed. See CurrentSchemaVersion for the version map.
	SchemaVersion int `json:"schema_version,omitempty"`

	// Primary is the user who inherits legacy untagged state — the only
	// account that can see sessions without a "vpsm-<user>-" prefix,
	// or any other resource that pre-dates per-user namespacing. Set by
	// migrate.go on the v1→v2 upgrade ("sam" by plan) and never changed
	// after; deleting the primary user is rejected by the user-delete
	// handler so the binding stays stable.
	Primary string `json:"primary,omitempty"`

	Listen  string `json:"listen"`
	DataDir string `json:"data_dir"`
	// JWTSecret is the secret used to sign/validate HS256 tokens.
	// Preferred setup: set JWTSecretFile (a path) and leave JWTSecret empty —
	// Load() reads the file in applyDefaults. A literal JWTSecret is kept only
	// for backward compatibility; when both exist JWTSecret wins, but
	// scripts/sync-jwt-secret.sh aborts in that case to force the migration
	// to the file-based pattern.
	JWTSecret     string `json:"jwt_secret,omitempty"`
	JWTSecretFile string `json:"jwt_secret_file,omitempty"`

	// Supabase Auth migration: configuration of the self-hosted GoTrue that
	// serves as the source of truth for passwords.
	//
	//   - SupabaseURL: base of the Kong gateway (https://supabase.<host>) with no trailing slash
	//   - SupabaseAnonKey: public key (apikey header). NOT to be confused with service_role.
	//   - AuthBackend: "local" | "supabase" | "both" (default). Overriding via
	//     the VPSM_AUTH_BACKEND env var takes precedence (a quick operational call).
	SupabaseURL     string `json:"supabase_url,omitempty"`
	SupabaseAnonKey string `json:"supabase_anon_key,omitempty"`
	AuthBackend     string `json:"auth_backend,omitempty"`
	Username        string `json:"username,omitempty"`
	PasswordHash    string `json:"password_hash,omitempty"`
	TOTPSecret      string `json:"totp_secret,omitempty"`
	// Legacy single-user RecoveryTOTPSecret. Multi-user installs use
	// User.RecoveryTOTPSecret instead. See AllUsers() for the unified view.
	RecoveryTOTPSecret string `json:"recovery_totp_secret,omitempty"`
	Users              []User `json:"users,omitempty"`
	ClaudeHome         string `json:"claude_home"`
	// AndroidPackageName is the applicationId of the native Android app
	// (br.tech.vpsmanager.app), consumed by handleAssetLinks to build the
	// "package_name" field of /.well-known/assetlinks.json. It is populated
	// when the release keystore is generated — see
	// docs/android-signing-keystore.md, section 7. Empty until then; the
	// handler serves a structurally valid manifest even without this value.
	AndroidPackageName string `json:"android_package_name,omitempty"`
	// AndroidSigningFingerprints holds the SHA-256 fingerprints (colon-
	// delimited, upper-case hex — the same format `keytool -list -v`
	// prints) of the Android app signing certificate(s), used by
	// handleAssetLinks to build "sha256_cert_fingerprints" in
	// /.well-known/assetlinks.json. More than one entry is supported on purpose:
	// a debug fingerprint can coexist with the release one during
	// development and be removed before production. Single source for this
	// value: docs/android-signing-keystore.md section 5 (it is born there and copied
	// here — never the other way round). Populated once distribution is wired up; empty until
	// then, NEVER a placeholder value.
	AndroidSigningFingerprints []string `json:"android_signing_fingerprints,omitempty"`
	// PublicHostname is the bare hostname (no scheme/port) used as the
	// WebAuthn/passkey RPID (internal/auth/webauthn.go) and therefore MUST be
	// exactly the hostname announced in /.well-known/assetlinks.json
	// (handlers_wellknown.go): this instance answers on two hostnames with no
	// registrable suffix in common (the own domain and the *.hstgr.cloud
	// fallback), and WebAuthn requires a single RPID across the deployment.
	// Empty until configured — in that case the WebAuthn Relying Party is not
	// initialised (see internal/api.NewRouter) and the passkey routes answer
	// that they are unavailable, never with an incorrect RPID.
	PublicHostname string `json:"public_hostname,omitempty"`
	// PrivateAIURL is the base of the admin API of private-ai-api (the OAuth
	// proxy → private Claude Max API) that runs on the host. The token
	// management UI (Claude AI → Tokens) proxies server-side to it; the admin
	// token comes from the private-ai-api EnvironmentFile
	// (PrivateAIAdminTokenFile) or from the secrets vault, never from here.
	// Default in applyDefaults: http://127.0.0.1:8787.
	PrivateAIURL string `json:"private_ai_url,omitempty"`
	// PrivateAIAdminTokenFile is the file (dotenv/systemd EnvironmentFile
	// format) from which VPSM reads ADMIN_TOKEN to authenticate against the
	// private-ai-api admin API. A shared source of truth — it avoids
	// duplicating the secret. Default in applyDefaults: /etc/private-ai-api/env.
	PrivateAIAdminTokenFile string `json:"private_ai_admin_token_file,omitempty"`
	// AdGuardURL is the base of the admin API of AdGuard Home (the DNS filter
	// that runs on the host, in Docker, published on loopback only). The
	// Security → AdGuard panel proxies server-side to it; the credentials
	// (adguard_user/adguard_password) come from the secrets vault, never from
	// here. Default in applyDefaults: http://127.0.0.1:3053 (3000 is Grafana's).
	AdGuardURL string `json:"adguard_url,omitempty"`
	// sing-box tunnel: the Security → Devices panel manages the devices (VLESS
	// users) by editing config.json and reloading the container, and reads the
	// live state through the Clash API (loopback). The Clash API secret comes
	// from the vault (singbox_clash_secret), never from here. Defaults in
	// applyDefaults.
	SingboxConfigPath string `json:"singbox_config_path,omitempty"` // /opt/singbox/config.json
	SingboxContainer  string `json:"singbox_container,omitempty"`   // singbox
	SingboxClashURL   string `json:"singbox_clash_url,omitempty"`   // http://127.0.0.1:9095
	// SingboxDevicePortsPath points at the device→Reality-port map (JSON), read
	// by the per-device traffic meter (conntrack). Default in applyDefaults.
	SingboxDevicePortsPath string `json:"singbox_device_ports_path,omitempty"` // /opt/singbox/.device-ports

	// Data-saver: a per-device compression proxy (mitmproxy). The
	// Security → Savings panel edits the state on the host (settings/bypass)
	// and reads the counters of bytes saved. DatasaverContainers are restarted
	// when the bypass list changes (it is read when the container starts).
	// Defaults in applyDefaults.
	DatasaverStateDir   string   `json:"datasaver_state_dir,omitempty"`  // /opt/datasaver/state
	DatasaverCAPath     string   `json:"datasaver_ca_path,omitempty"`    // /opt/datasaver/ca/mitmproxy-ca-cert.pem
	DatasaverContainers []string `json:"datasaver_containers,omitempty"` // [datasaver-vps, datasaver-casa]
	TLSEnabled          bool     `json:"tls_enabled"`
	TLSDomain           string   `json:"tls_domain,omitempty"`
	TLSEmail            string   `json:"tls_email,omitempty"`
	TLSCert             string   `json:"tls_cert"`
	TLSKey              string   `json:"tls_key"`

	// Alerting controls the dispatch of Prometheus/Alertmanager alerts over
	// WhatsApp. The POST /_internal/alert endpoint on the router consumes this
	// block. Loopback-only; editable through /api/admin/alerting in the UI.
	Alerting Alerting `json:"alerting,omitempty"`

	// AIModels routes each class of AI task to a model by
	// complexity. Empty → inherits Opus. See AIModels.
	AIModels AIModels `json:"ai_models,omitempty"`

	// GitRepos is the allowlist of the visual Git client. When empty,
	// package internal/git falls back to a built-in seed with the repos known to
	// the host; entries here are merged by Path (policy / identity
	// override) and new paths are added. omitempty: absent → nil →
	// seed only, backward compatible and does not touch SchemaVersion. This is NEVER the
	// git write surface — only the authorization boundary (which
	// repos are visible and which accept commits, under which identity).
	GitRepos []GitRepo `json:"git_repos,omitempty"`

	// GitIdentities — the author identities selectable in the visual Git
	// client. See GitIdentity. Empty → the built-in seed.
	GitIdentities []GitIdentity `json:"git_identities,omitempty"`

	// TactiqIntakeToken is the Bearer token that authenticates the
	// POST /intake/tactiq endpoint (which receives transcripts from a Google
	// Apps Script). Generated automatically in applyDefaults when empty; saved
	// to config.json.
	TactiqIntakeToken string `json:"tactiq_intake_token,omitempty"`

	// AcmeBookingURL is the base URL of Acme Booking.
	// The pusher sends meeting drafts to POST <URL>/api/vps/drafts.
	// Default: https://booking.example.com
	AcmeBookingURL string `json:"css_lee_url,omitempty"`

	// GmailDraftLive controls the A6 worker: approved notes → draft in
	// Gmail. Default false = DRY-RUN (writes to data/dryrun/gmail/ the draft that
	// WOULD be created, without touching Gmail — the same inert pattern as the fllr worker).
	// With true it creates the real draft in Jordan's mailbox (NEVER send). It only takes
	// effect live once gmailConfigured() holds (refresh token in the vault).
	GmailDraftLive bool `json:"gmail_draft_live,omitempty"`

	// Inbox poller — Flow C. Reads the inbox of the work mailbox
	// (gmail.readonly), deduplicates by thread_id and classifies via LLM into 3 buckets,
	// spooling into data/intake/email/. Default OFF: it only reads/classifies when
	// GmailPollerEnabled=true (and gmailConfigured()). Read-only — zero external writes.
	GmailPollerEnabled   bool   `json:"gmail_poller_enabled,omitempty"`
	GmailPollIntervalSec int    `json:"gmail_poll_interval_sec,omitempty"` // default 900 (15min)
	GmailPollQuery       string `json:"gmail_poll_query,omitempty"`        // default "in:inbox newer_than:2d"
	GmailPollMaxResults  int    `json:"gmail_poll_max_results,omitempty"`  // cap on threads per pass; default 25

	// EmailPusherEnabled turns on the Flow C pusher: it pushes every e-mail
	// already classified (status="classified" in data/intake/email/) to CSS via
	// POST /api/vps/emails, where it becomes an Alert + an approval task. It is the
	// WRITE piece of Flow C — the poller (GmailPollerEnabled) only reads/classifies.
	// Default OFF: it only pushes with the gate on + css_cron_secret in the vault +
	// AcmeBookingURL set. Additive and inert by default; it NEVER sends e-mail
	// (Gmail stays read-only).
	EmailPusherEnabled bool `json:"email_pusher_enabled,omitempty"`

	// EmailExtractTasks turns on extraction of TASKS from the e-mail body:
	// when on, the poller fetches the full body (format=full) and the classifier
	// also extracts actionable tasks (titulo/responsavel/data_limite/descricao/
	// prioridade), pushed in the tarefas[] field of POST /api/vps/emails — it feeds
	// the CSS "Emails to Review" page. Default OFF: off = the previous
	// behaviour (metadata + bucket only, no tasks). Additive/inert.
	EmailExtractTasks bool `json:"email_extract_tasks,omitempty"`

	// Acme roster — who counts as the INTERNAL TEAM for the purposes of the
	// `lado` (cliente|fllr) of extracted tasks (meetings + e-mails). The AI
	// classifier infers the side from the content and confuses "a task ABOUT
	// the client" with "the client is the assignee"; this roster grounds the
	// prompt AND deterministically forces lado=fllr when the assignee is
	// internal. It is DATA (config), not a literal in Go, and it mirrors the
	// source of truth in CSS (agendamento.pessoas ∪ domain). Sam is the DEV —
	// he belongs to NEITHER side (neither client nor Acme). See ehFllr() in
	// intake_processor.go.
	//   - IntakeFllrDomains: domains that define the org (default ["acme.example"]).
	//   - IntakeFllrMembers: aliases the domain does not cover (display names +
	//     alternative e-mails, e.g. Jordan's gmail). Case-insensitive.
	IntakeFllrDomains []string `json:"intake_fllr_domains,omitempty"`
	IntakeFllrMembers []string `json:"intake_fllr_members,omitempty"`

	// CLIENT roster — the EXTERNAL companies Acme serves. The
	// AI classifier extracts the `cliente` of each e-mail/meeting from the
	// CONTENT (summary/minutes); these fields ground the prompt and normalize the parse
	// deterministically. Acme is the CONSULTANCY (never a client), and vendors/
	// platforms (OneTrust, Mercatus, …) are not clients either. The source of truth for the
	// aliases is the CSS table agendamento.clientes_fllr (CSS re-normalizes on
	// intake); here the roster is enough for grounding + the exclusion list. The
	// client-name normalization lives in the intake processor.
	//   - IntakeClienteRoster: known canonical names (prompt grounding).
	//   - IntakeClienteExclusions: names/domains that are NEVER a client (Acme,
	//     vendors, generic providers). Case-insensitive.
	IntakeClienteRoster     []string `json:"intake_cliente_roster,omitempty"`
	IntakeClienteExclusions []string `json:"intake_cliente_exclusions,omitempty"`

	// SessionCollectorEnabled turns on the Claude Code session collector — Flow B
	// piece 1. It ingests RAW sessions (CLI + VS Code extension) from N of Jordan's
	// machines (they arrive via Syncthing in data/intake/sessions/sync/ or by
	// POST /intake/session), archives them versioned and INDEXES them (one record per session),
	// deduplicating by (machine,session,hash). It does NOT summarize — the timesheet is
	// built from the raw data, so an LLM summary here would be dead cost. Default OFF.
	// Read/archive only — zero external writes. The POST token lives in the vault
	// (sam:session_intake_token).
	SessionCollectorEnabled bool `json:"session_collector_enabled,omitempty"`
	// Hardening of the collector against real data. On the Syncthing path,
	// sync/ is already the durable/versioned raw copy, so the collector does NOT re-archive there;
	// it processes only *.jsonl and summarizes only what is recent. Defaults applied at the use site.
	SessionCollectorMaxAgeDays    int `json:"session_collector_max_age_days,omitempty"`   // only summarise sessions touched in the last N days (default 30; 0 = default)
	SessionCollectorSettleMinutes int `json:"session_collector_settle_minutes,omitempty"` // never summarise a file touched in the last N minutes (default 10)
	SessionCollectorMaxReadKB     int `json:"session_collector_max_read_kb,omitempty"`    // cap on bytes read for the summary; a larger file is head+tail (default 2048 KB)

	// FlowBEnabled turns on Flow B piece 2: the builder of the day's timeline
	// (B3) + submission to Gate 2 in CSS + a time entry in quadro_apontamentos (B5).
	// From the sessions already aggregated by piece 1 it builds timeline[] {hour,
	// duration, task?} and sends it to CSS; on approval at Gate 2 it writes the
	// time entry (dry-run by default — nothing written to Acme without an explicit OK).
	// Default OFF: the builder only runs on a `process_day` event from CSS and the
	// pusher only sends when true. Read/spool only — inert by default.
	FlowBEnabled bool `json:"flow_b_enabled,omitempty"`
	// FlowBIdleGapMinutes: the idle gap (minutes) that separates two blocks of
	// time within the same session (default 90; 0 = default). Past that much
	// silence, the builder splits the session into distinct blocks in the day's
	// timeline.
	FlowBIdleGapMinutes int `json:"flow_b_idle_gap_minutes,omitempty"`
	// FlowBTimezone: the timezone used to bucket the timestamps (UTC in the
	// jsonl) into the LOCAL DAY of the timesheet and to render start/end times
	// (default "America/Sao_Paulo"). Without it, work done after midnight would
	// land on the wrong UTC day. An IANA tz name; invalid or empty falls back
	// to the default.
	FlowBTimezone string `json:"flow_b_timezone,omitempty"`

	// --- Intake watchdog / health ---
	// IntakeWatchdogIntervalSec: cadence (s) of the watchdog that sweeps the intake
	// queue, builds the health snapshot and pushes it to CSS (heartbeat +
	// dead-man switch). Default 120 (0 = default).
	IntakeWatchdogIntervalSec int `json:"intake_watchdog_interval_sec,omitempty"`
	// IntakeStuckAlertMinutes: the age (minutes) beyond which a record in
	// retry/pending is considered "stuck" and becomes an alert condition in the
	// snapshot. Default 60 (0 = default).
	IntakeStuckAlertMinutes int `json:"intake_stuck_alert_minutes,omitempty"`

	// LoadedFromBackup is true when Load() had to fall back to a .bak.<ts>
	// because the live config.json failed to parse. Serializes as a transient
	// field (omitempty + json:"-" on disk) — surfacing via /api/vpsm/health.
	LoadedFromBackup bool   `json:"-"`
	LoadedBackupName string `json:"-"`
}

// GitRepo describes a git repository exposed by the visual Git client
// and its authorization policy. It lives in package config (not in
// internal/git) because Config serializes it and internal/git imports config —
// the other way round would create an import cycle.
//
//   - ID:       short, stable identifier used in the API (?repo=<id>).
//   - Path:     absolute root of the repo on the host (write-jail boundary).
//   - Name:     display label in the front-end dropdown.
//   - Policy:   "read-only" (view only) or "write" (commit/branch/etc).
//   - ExpName/ExpEmail: the EXPECTED commit identity. The backend forces
//     `-c user.name=<ExpName> -c user.email=<ExpEmail>` on the commit and aborts
//     if the live local user.email diverges (defence in depth against
//     leaking the wrong identity — e.g. never commit work with a personal
//     e-mail, or the reverse).
type GitRepo struct {
	ID       string `json:"id"`
	Path     string `json:"path"`
	Name     string `json:"name"`
	Policy   string `json:"policy"`
	ExpName  string `json:"exp_name,omitempty"`
	ExpEmail string `json:"exp_email,omitempty"`
}

// GitIdentity is an author identity selectable in the visual Git client: the
// user picks which account (name+e-mail) signs a commit. It lives in the
// config because it is serialised; when GitIdentities is empty, the
// internal/git package uses a built-in seed with the known identities (work
// and personal). The choice is per commit; the repo has an expected identity
// (GitRepo.Exp*) which becomes the suggested default plus a divergence
// warning, without blocking (the user stays in explicit control).
type GitIdentity struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

// GitIdentities is the list of selectable author identities.
// omitempty: absent → nil → only the built-in seed is used. Does not touch
// SchemaVersion.

// AllUsers returns the primary credential (Username/PasswordHash) plus any
// additional Users entries. Single-user installs keep working unchanged.
func (c *Config) AllUsers() []User {
	out := make([]User, 0, 1+len(c.Users))
	if c.Username != "" && c.PasswordHash != "" {
		out = append(out, User{
			Username:     c.Username,
			PasswordHash: c.PasswordHash,
			TOTPSecret:   c.TOTPSecret,
		})
	}
	out = append(out, c.Users...)
	return out
}

// HasUser reports whether username is present in either the legacy top-
// level slot or Users[]. Lookup is exact (no case folding) — names that
// differ only in case are different users on disk.
func (c *Config) HasUser(username string) bool {
	if username == "" {
		return false
	}
	if c.Username == username {
		return true
	}
	for _, u := range c.Users {
		if u.Username == username {
			return true
		}
	}
	return false
}

// IsAdmin reports whether username holds system-admin privileges: either the
// singular Primary account, or any user carrying the Admin flag. This is the
// single source of truth behind every admin-only RBAC gate (httpx.IsAdmin /
// IsPrimary delegate here). Exact match, no case folding.
func (c *Config) IsAdmin(username string) bool {
	if username == "" {
		return false
	}
	if c.Primary != "" && c.Primary == username {
		return true // the primary is always an admin
	}
	if c.Username == username {
		// Legacy top-level single user (pre-migration v1): it is the sole
		// account and runs everything, so it's admin when it's the primary
		// or when no primary has been recorded yet.
		return c.Primary == "" || c.Username == c.Primary
	}
	for _, u := range c.Users {
		if u.Username == username {
			return u.Admin
		}
	}
	return false
}

// SetAdmin promotes (v=true) or demotes (v=false) username's admin flag.
// Returns an error when the user is unknown or when an attempt is made to
// demote the Primary (always an admin — the binding must stay stable so
// there is never zero admins). Promoting the Primary is a no-op success.
func (c *Config) SetAdmin(username string, v bool) error {
	if username == "" {
		return fmt.Errorf("empty username")
	}
	if c.Primary != "" && c.Primary == username {
		if !v {
			return fmt.Errorf("cannot revoke admin from the primary user '%s'", username)
		}
		return nil // already implicitly admin
	}
	if c.Username == username {
		// Legacy top-level user is the lone v1 account (effectively primary);
		// it carries no separate Admin flag. Promotion is a no-op; demotion is
		// rejected to preserve the never-zero-admins invariant.
		if !v {
			return fmt.Errorf("cannot revoke admin from the legacy user '%s'", username)
		}
		return nil
	}
	for i := range c.Users {
		if c.Users[i].Username == username {
			c.Users[i].Admin = v
			return nil
		}
	}
	return fmt.Errorf("user '%s' not found", username)
}

// Admins returns the usernames that currently hold admin privileges
// (primary included), for display/CLI. Stable order: primary first.
func (c *Config) Admins() []string {
	out := []string{}
	seen := map[string]bool{}
	if c.Primary != "" {
		out = append(out, c.Primary)
		seen[c.Primary] = true
	}
	for _, u := range c.AllUsers() {
		if u.Admin && !seen[u.Username] {
			out = append(out, u.Username)
			seen[u.Username] = true
		}
	}
	return out
}

// IsAppOnly reports whether username is restricted to the native Android app
// (User.AppOnly). It is the single source of truth for that gate: every entry
// handler of the web panel checks HERE before letting the account through.
// Exact match, no case folding — same as IsAdmin/HasUser.
//
// The legacy top-level user (c.Username) is NEVER app-only: it is the sole
// account of a v1 install, and carrying that marker there would lock the
// operator out of their own panel with no way back through the UI.
func (c *Config) IsAppOnly(username string) bool {
	if username == "" {
		return false
	}
	for _, u := range c.Users {
		if u.Username == username {
			return u.AppOnly
		}
	}
	return false
}

// IsLegacyV1 reports whether the on-disk config predates the per-user
// isolation work and must be upgraded by MigrateV1ToV2 before the router
// boots. The check is "schema version below current AND a legacy
// top-level primary user is recorded" — that combination cannot exist
// on a freshly bootstrapped v2 install, where Username is always empty.
func (c *Config) IsLegacyV1() bool {
	if c.SchemaVersion >= CurrentSchemaVersion {
		return false
	}
	return c.Username != "" || c.PasswordHash != ""
}

// TOTPSecretFor returns the user's TOTP secret (or "" if 2FA is not enrolled).
func (c *Config) TOTPSecretFor(username string) (string, bool) {
	if username == "" {
		return "", false
	}
	if c.Username == username {
		return c.TOTPSecret, c.TOTPSecret != ""
	}
	for _, u := range c.Users {
		if u.Username == username {
			return u.TOTPSecret, u.TOTPSecret != ""
		}
	}
	return "", false
}

// SetTOTPSecret stores (or clears, when secret == "") the TOTP secret for the
// user. Returns true when a record was modified.
func (c *Config) SetTOTPSecret(username, secret string) bool {
	if username == "" {
		return false
	}
	if c.Username == username {
		c.TOTPSecret = secret
		return true
	}
	for i := range c.Users {
		if c.Users[i].Username == username {
			c.Users[i].TOTPSecret = secret
			return true
		}
	}
	return false
}

// RecoveryTOTPSecretFor mirrors TOTPSecretFor but for the /recovery flow.
// The recovery secret is INDEPENDENT from the primary so a session/secret
// compromise on the main app doesn't grant access to rollback/restart.
func (c *Config) RecoveryTOTPSecretFor(username string) (string, bool) {
	if username == "" {
		return "", false
	}
	if c.Username == username {
		return c.RecoveryTOTPSecret, c.RecoveryTOTPSecret != ""
	}
	for _, u := range c.Users {
		if u.Username == username {
			return u.RecoveryTOTPSecret, u.RecoveryTOTPSecret != ""
		}
	}
	return "", false
}

// SetRecoveryTOTPSecret stores (or clears) the recovery TOTP secret.
func (c *Config) SetRecoveryTOTPSecret(username, secret string) bool {
	if username == "" {
		return false
	}
	if c.Username == username {
		c.RecoveryTOTPSecret = secret
		return true
	}
	for i := range c.Users {
		if c.Users[i].Username == username {
			c.Users[i].RecoveryTOTPSecret = secret
			return true
		}
	}
	return false
}

// SetPassword updates the password hash of the user identified by username.
// Returns true when a record was modified. Works for both the legacy primary
// credential and entries in Users.
func (c *Config) SetPassword(username, hash string) bool {
	if username == "" {
		return false
	}
	if c.Username == username {
		c.PasswordHash = hash
		return true
	}
	for i := range c.Users {
		if c.Users[i].Username == username {
			c.Users[i].PasswordHash = hash
			return true
		}
	}
	return false
}

// AddUser creates a new user in Users[]. Returns an error if it already exists.
// It does not touch the primary user (the top-level Username) — that one is managed separately.
func (c *Config) AddUser(username, hash string) error {
	if username == "" {
		return fmt.Errorf("empty username")
	}
	if hash == "" {
		return fmt.Errorf("empty hash")
	}
	// Check for a conflict with the primary user
	if c.Username == username {
		return fmt.Errorf("user '%s' already exists (primary)", username)
	}
	for _, u := range c.Users {
		if u.Username == username {
			return fmt.Errorf("user '%s' already exists", username)
		}
	}
	c.Users = append(c.Users, User{Username: username, PasswordHash: hash})
	return nil
}

// RemoveUser removes the user. The primary user cannot be removed (guard).
// Returns an error when it is not found.
func (c *Config) RemoveUser(username string) error {
	if username == "" {
		return fmt.Errorf("empty username")
	}
	// The primary is protected in BOTH layouts: the legacy top-level one
	// (c.Username) and the v2 one (c.Primary points at an entry in Users[]).
	// Without the second guard, an additional admin could delete the primary
	// and break the binding of legacy state (untagged sessions, migrate).
	// See Config.Primary.
	if c.Username == username {
		return fmt.Errorf("cannot remove the primary user '%s'", username)
	}
	if c.Primary != "" && c.Primary == username {
		return fmt.Errorf("cannot remove the primary user '%s'", username)
	}
	for i, u := range c.Users {
		if u.Username == username {
			c.Users = append(c.Users[:i], c.Users[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("user '%s' not found", username)
}
