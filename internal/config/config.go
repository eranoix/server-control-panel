package config

import (
	"fmt"
)

type User struct {
	Username           string `json:"username"`
	PasswordHash       string `json:"password_hash"`
	TOTPSecret         string `json:"totp_secret,omitempty"`
	RecoveryTOTPSecret string `json:"recovery_totp_secret,omitempty"`
	SupabaseMFAEnabled bool   `json:"supabase_mfa_enabled,omitempty"`

	Admin bool `json:"admin,omitempty"`

	AppOnly bool `json:"app_only,omitempty"`
}

type Alerting struct {
	Enabled     bool   `json:"enabled,omitempty"`
	FromUser    string `json:"from_user,omitempty"`
	ChatJID     string `json:"chat_jid,omitempty"`
	MinSeverity string `json:"min_severity,omitempty"`
}

type AIModels struct {
	Suggest string `json:"suggest,omitempty"`
	JiraAI  string `json:"jira_ai,omitempty"`
	Intake  string `json:"intake,omitempty"`
}

const CurrentSchemaVersion = 2

type Config struct {
	SchemaVersion int `json:"schema_version,omitempty"`

	Primary string `json:"primary,omitempty"`

	Listen        string `json:"listen"`
	DataDir       string `json:"data_dir"`
	JWTSecret     string `json:"jwt_secret,omitempty"`
	JWTSecretFile string `json:"jwt_secret_file,omitempty"`

	SupabaseURL                string   `json:"supabase_url,omitempty"`
	SupabaseAnonKey            string   `json:"supabase_anon_key,omitempty"`
	AuthBackend                string   `json:"auth_backend,omitempty"`
	Username                   string   `json:"username,omitempty"`
	PasswordHash               string   `json:"password_hash,omitempty"`
	TOTPSecret                 string   `json:"totp_secret,omitempty"`
	RecoveryTOTPSecret         string   `json:"recovery_totp_secret,omitempty"`
	Users                      []User   `json:"users,omitempty"`
	ClaudeHome                 string   `json:"claude_home"`
	AndroidPackageName         string   `json:"android_package_name,omitempty"`
	AndroidSigningFingerprints []string `json:"android_signing_fingerprints,omitempty"`
	PublicHostname             string   `json:"public_hostname,omitempty"`
	PrivateAIURL               string   `json:"private_ai_url,omitempty"`
	PrivateAIAdminTokenFile    string   `json:"private_ai_admin_token_file,omitempty"`
	AdGuardURL                 string   `json:"adguard_url,omitempty"`
	SingboxConfigPath          string   `json:"singbox_config_path,omitempty"`
	SingboxContainer           string   `json:"singbox_container,omitempty"`
	SingboxClashURL            string   `json:"singbox_clash_url,omitempty"`
	SingboxDevicePortsPath     string   `json:"singbox_device_ports_path,omitempty"`

	DatasaverStateDir   string   `json:"datasaver_state_dir,omitempty"`
	DatasaverCAPath     string   `json:"datasaver_ca_path,omitempty"`
	DatasaverContainers []string `json:"datasaver_containers,omitempty"`
	TLSEnabled          bool     `json:"tls_enabled"`
	TLSDomain           string   `json:"tls_domain,omitempty"`
	TLSEmail            string   `json:"tls_email,omitempty"`
	TLSCert             string   `json:"tls_cert"`
	TLSKey              string   `json:"tls_key"`

	Alerting Alerting `json:"alerting,omitempty"`

	AIModels AIModels `json:"ai_models,omitempty"`

	GitRepos []GitRepo `json:"git_repos,omitempty"`

	GitIdentities []GitIdentity `json:"git_identities,omitempty"`

	TactiqIntakeToken string `json:"tactiq_intake_token,omitempty"`

	AcmeBookingURL string `json:"css_lee_url,omitempty"`

	GmailDraftLive bool `json:"gmail_draft_live,omitempty"`

	GmailPollerEnabled   bool   `json:"gmail_poller_enabled,omitempty"`
	GmailPollIntervalSec int    `json:"gmail_poll_interval_sec,omitempty"`
	GmailPollQuery       string `json:"gmail_poll_query,omitempty"`
	GmailPollMaxResults  int    `json:"gmail_poll_max_results,omitempty"`

	EmailPusherEnabled bool `json:"email_pusher_enabled,omitempty"`

	EmailExtractTasks bool `json:"email_extract_tasks,omitempty"`

	IntakeFllrDomains []string `json:"intake_fllr_domains,omitempty"`
	IntakeFllrMembers []string `json:"intake_fllr_members,omitempty"`

	IntakeClientRoster     []string `json:"intake_client_roster,omitempty"`
	IntakeClientExclusions []string `json:"intake_client_exclusions,omitempty"`

	SessionCollectorEnabled       bool `json:"session_collector_enabled,omitempty"`
	SessionCollectorMaxAgeDays    int  `json:"session_collector_max_age_days,omitempty"`
	SessionCollectorSettleMinutes int  `json:"session_collector_settle_minutes,omitempty"`
	SessionCollectorMaxReadKB     int  `json:"session_collector_max_read_kb,omitempty"`

	FlowBEnabled        bool   `json:"flow_b_enabled,omitempty"`
	FlowBIdleGapMinutes int    `json:"flow_b_idle_gap_minutes,omitempty"`
	FlowBTimezone       string `json:"flow_b_timezone,omitempty"`

	IntakeWatchdogIntervalSec int `json:"intake_watchdog_interval_sec,omitempty"`
	IntakeStuckAlertMinutes   int `json:"intake_stuck_alert_minutes,omitempty"`

	LoadedFromBackup bool   `json:"-"`
	LoadedBackupName string `json:"-"`
}

type GitRepo struct {
	ID       string `json:"id"`
	Path     string `json:"path"`
	Name     string `json:"name"`
	Policy   string `json:"policy"`
	ExpName  string `json:"exp_name,omitempty"`
	ExpEmail string `json:"exp_email,omitempty"`
}

type GitIdentity struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

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

func (c *Config) IsAdmin(username string) bool {
	if username == "" {
		return false
	}
	if c.Primary != "" && c.Primary == username {
		return true
	}
	if c.Username == username {
		return c.Primary == "" || c.Username == c.Primary
	}
	for _, u := range c.Users {
		if u.Username == username {
			return u.Admin
		}
	}
	return false
}

func (c *Config) SetAdmin(username string, v bool) error {
	if username == "" {
		return fmt.Errorf("empty username")
	}
	if c.Primary != "" && c.Primary == username {
		if !v {
			return fmt.Errorf("cannot revoke admin from the primary user '%s'", username)
		}
		return nil
	}
	if c.Username == username {
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

func (c *Config) IsLegacyV1() bool {
	if c.SchemaVersion >= CurrentSchemaVersion {
		return false
	}
	return c.Username != "" || c.PasswordHash != ""
}

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

func (c *Config) AddUser(username, hash string) error {
	if username == "" {
		return fmt.Errorf("empty username")
	}
	if hash == "" {
		return fmt.Errorf("empty hash")
	}
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

func (c *Config) RemoveUser(username string) error {
	if username == "" {
		return fmt.Errorf("empty username")
	}
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
