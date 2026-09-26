// Package claudeacct is the per-consumer Claude account selector.
//
// # Why this exists
//
// "Switching Claude accounts" is NOT a claude-router change. In oauth mode the
// router is a transparent passthrough — it only fixes the Host header and
// never injects Authorization (proxy.go:57-61). The Bearer that identifies
// the account is minted by Claude Code itself from its *config dir*
// (.credentials.json). So switching accounts == switching the CLAUDE_CONFIG_DIR
// a given consumer spawns with.
//
// This package owns the registry of known accounts (compiled-in allowlist)
// and the mutable map of consumer→account assignments (persisted in
// <DataDir>/claude_accounts.json). The hot path is ConfigDirFor(consumer),
// called by the jiraai runner and the PTY layer to decide which
// CLAUDE_CONFIG_DIR to export per spawn.
//
// # Sharing model (decided empirically)
//
//	.credentials.json  → per-account (the OAuth login). NOT shared.
//	.claude.json       → per-account (oauthAccount identity + MCP state).
//	                     mcpServers pre-seeded so MCPs work for both accounts.
//	settings.json      → symlink to /root/.claude (ANTHROPIC_BASE_URL=:8788). Shared.
//	CLAUDE.md          → symlink to /root/.claude (global rules). Shared.
//	prompts            → internal/aiprompts, server-side, already account-agnostic.
//
// Default = "sam" (the owner's decision): the owner of this machine is sam
// (the 20x account), so EVERY session/consumer without an explicit assignment
// uses his account (CLAUDE_CONFIG_DIR=/srv/agent-accounts/sam), not jordan
// (jordan, the global ~/.claude). jordan stays selectable when it is chosen on
// purpose. The default used to be jordan (ConfigDir="") — which ran sam's work
// on jordan's account, mixing identities.
package claudeacct

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// DefaultAccountID is the account every consumer falls back to. sam (the machine
// owner's 20x account) → CLAUDE_CONFIG_DIR=/srv/agent-accounts/sam.
const DefaultAccountID = "sam"

// Consumer IDs — the class-(A) OAuth/Max consumers the switch covers. These
// are an allowlist: Assign rejects anything else. They mirror the three spawn
// seams in the codebase (jiraai/runner.go, pty/aienv.go, pty/spawn.go).
const (
	ConsumerJobs     = "jobs"     // jiraai analysis: `claude -p` (runner.go:189/:465)
	ConsumerTerminal = "terminal" // interactive Claude panels (pty/pty.go + aienv.go)
	ConsumerFork     = "fork"     // fork / restart / jira-work sessions (pty/spawn.go)
)

// consumerOrder is the canonical display+iteration order.
var consumerOrder = []string{ConsumerJobs, ConsumerTerminal, ConsumerFork}

// Account is a Claude identity backed by its own config dir.
//
// ConfigDir == "" is special: it means "inherit the process default"
// (no CLAUDE_CONFIG_DIR export → Claude Code reads $HOME/.claude). Only the
// default account uses "". Every other account points at a provisioned dir
// under /srv/agent-accounts/<id>.
type Account struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Email     string `json:"email"`
	ConfigDir string `json:"config_dir"`
}

// accountsBaseDir is where non-default account config dirs are provisioned.
// Overridable via VPSM_CLAUDE_ACCOUNTS_DIR (portability for non-root deploys +
// hermetic tests); defaults to /srv/agent-accounts.
func accountsBaseDir() string {
	if v := strings.TrimSpace(os.Getenv("VPSM_CLAUDE_ACCOUNTS_DIR")); v != "" {
		return v
	}
	return "/srv/agent-accounts"
}

// registry is the compiled-in allowlist of known accounts. Adding an account
// is a deliberate code change (the config dir must be provisioned + logged in
// out-of-band first), which is exactly the guarantee we want: no UI action can
// conjure an account that has no real credential dir behind it.
func defaultRegistry() []Account {
	return []Account{
		{ID: "jordan", Label: "Jordan", Email: "jordan@northwind.example", ConfigDir: ""},
		{ID: "sam", Label: "Sam", Email: "sam.rivera@personal.example", ConfigDir: filepath.Join(accountsBaseDir(), "sam")},
	}
}

// Store holds the registry + the persisted consumer→account assignment map.
// All public methods are safe for concurrent use.
type Store struct {
	mu               sync.RWMutex
	path             string            // <DataDir>/claude_accounts.json
	claudeHome       string            // resolved default config dir (e.g. /root/.claude)
	accounts         []Account         // compiled-in allowlist
	assignments      map[string]string // consumer → accountID
	sessionOverrides map[string]string // session name → accountID
}

type persisted struct {
	Assignments      map[string]string `json:"assignments"`
	SessionOverrides map[string]string `json:"session_overrides,omitempty"`
}

// Open loads (or seeds) the assignment store. claudeHome is the default
// account's config dir (cfg.ClaudeHome, normally /root/.claude); it anchors
// LoginStatus path resolution for the default account. A missing file is not
// an error — it is seeded with every consumer pointing at DefaultAccountID.
func Open(dataDir, claudeHome string) (*Store, error) {
	if claudeHome == "" {
		claudeHome = "/root/.claude"
	}
	s := &Store{
		path:             filepath.Join(dataDir, "claude_accounts.json"),
		claudeHome:       claudeHome,
		accounts:         defaultRegistry(),
		assignments:      map[string]string{},
		sessionOverrides: map[string]string{},
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) load() error {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		// Seed: every consumer → default. Persist so the file exists for
		// later hand-edits and so the shape is discoverable.
		s.assignments = s.seedDefaults()
		return s.saveLocked()
	}
	if err != nil {
		return err
	}
	var p persisted
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	out := s.seedDefaults()
	// Overlay persisted values, but only for known consumer→account pairs.
	// Unknown/garbage entries are dropped (forward/backward-compat + safety).
	for c, a := range p.Assignments {
		if isConsumer(c) && s.hasAccount(a) {
			out[c] = a
		}
	}
	s.assignments = out
	if p.SessionOverrides != nil {
		s.sessionOverrides = make(map[string]string, len(p.SessionOverrides))
		for sess, aid := range p.SessionOverrides {
			if s.hasAccountLocked(aid) {
				s.sessionOverrides[sess] = aid
			}
		}
	} else {
		s.sessionOverrides = make(map[string]string)
	}
	return nil
}

func (s *Store) seedDefaults() map[string]string {
	m := make(map[string]string, len(consumerOrder))
	for _, c := range consumerOrder {
		m[c] = DefaultAccountID
	}
	return m
}

// ConfigDirFor returns the CLAUDE_CONFIG_DIR to export for the given consumer,
// or "" to inherit the process default ($HOME/.claude). This is the hot path
// — callers do `if dir := store.ConfigDirFor(c); dir != "" { env += dir }`.
// An unknown consumer falls back to the default account (→ "").
func (s *Store) ConfigDirFor(consumer string) string {
	id := s.AccountIDFor(consumer)
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, a := range s.accounts {
		if a.ID == id {
			return a.ConfigDir
		}
	}
	return ""
}

// AccountIDFor returns the account ID assigned to a consumer, defaulting to
// DefaultAccountID for unknown consumers or unset assignments.
func (s *Store) AccountIDFor(consumer string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if id, ok := s.assignments[consumer]; ok && s.hasAccountLocked(id) {
		return id
	}
	return DefaultAccountID
}

// Assign points a consumer at an account. Both are validated against the
// allowlists; an invalid pair is rejected without touching disk.
func (s *Store) Assign(consumer, accountID string) error {
	if !isConsumer(consumer) {
		return errors.New("unknown consumer: " + consumer)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.hasAccountLocked(accountID) {
		return errors.New("unknown account: " + accountID)
	}
	s.assignments[consumer] = accountID
	return s.saveLocked()
}

// SetSessionAccount associates a specific session with an account, enabling
// per-session hotswap without touching the global consumer assignment. accountID=""
// removes the override, falling back to the consumer-level assignment.
func (s *Store) SetSessionAccount(session, accountID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if accountID != "" && !s.hasAccountLocked(accountID) {
		return errors.New("unknown account: " + accountID)
	}
	if accountID == "" {
		delete(s.sessionOverrides, session)
	} else {
		s.sessionOverrides[session] = accountID
	}
	return s.saveLocked()
}

// ConfigDirForSession returns the CLAUDE_CONFIG_DIR for a specific session.
// Session override takes priority; falls back to the consumer-level assignment.
// Three sequential (never nested) lock acquisitions.
func (s *Store) ConfigDirForSession(session, consumer string) string {
	s.mu.RLock()
	id, hasOverride := s.sessionOverrides[session]
	overrideValid := hasOverride && s.hasAccountLocked(id)
	s.mu.RUnlock()

	if !overrideValid {
		id = s.AccountIDFor(consumer) // acquires its own lock — sequential, not nested
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, a := range s.accounts {
		if a.ID == id {
			return a.ConfigDir
		}
	}
	return ""
}

// SessionAccountID returns the effective accountID for a session. Inline to
// avoid nested RLock (does not call AccountIDFor while holding the lock).
func (s *Store) SessionAccountID(session, consumer string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if id, ok := s.sessionOverrides[session]; ok && s.hasAccountLocked(id) {
		return id
	}
	if id, ok := s.assignments[consumer]; ok && s.hasAccountLocked(id) {
		return id
	}
	return DefaultAccountID
}

// Accounts returns a copy of the registry.
func (s *Store) Accounts() []Account {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Account, len(s.accounts))
	copy(out, s.accounts)
	return out
}

// Consumers returns the canonical ordered list of class-(A) consumer IDs.
func (s *Store) Consumers() []string {
	out := make([]string, len(consumerOrder))
	copy(out, consumerOrder)
	return out
}

// Assignments returns a copy of the consumer→accountID map.
func (s *Store) Assignments() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]string, len(s.assignments))
	for k, v := range s.assignments {
		out[k] = v
	}
	return out
}

// AccountByID looks up an account in the registry.
func (s *Store) AccountByID(id string) (Account, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, a := range s.accounts {
		if a.ID == id {
			return a, true
		}
	}
	return Account{}, false
}

func (s *Store) hasAccount(id string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.hasAccountLocked(id)
}

func (s *Store) hasAccountLocked(id string) bool {
	for _, a := range s.accounts {
		if a.ID == id {
			return true
		}
	}
	return false
}

func isConsumer(c string) bool {
	for _, x := range consumerOrder {
		if x == c {
			return true
		}
	}
	return false
}

// saveLocked atomically persists the assignment map. Caller holds s.mu.
// Mirrors the tmp→fsync→rename pattern used across the codebase (todos.go).
func (s *Store) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(persisted{
		Assignments:      s.assignments,
		SessionOverrides: s.sessionOverrides,
	}, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, s.path)
}

// ─── Login status (metadata only — NEVER exposes the token) ───────────────

// LoginStatus is the safe-to-serialize view of an account's credential. It is
// built by parsing ONLY metadata fields out of .credentials.json/.claude.json;
// the access/refresh tokens are never unmarshaled into memory, let alone
// returned. The frontend renders "logged in as <email>, expires in Nd".
type LoginStatus struct {
	AccountID        string `json:"account_id"`
	LoggedIn         bool   `json:"logged_in"`
	Email            string `json:"email,omitempty"`
	SubscriptionType string `json:"subscription_type,omitempty"`
	ExpiresAt        int64  `json:"expires_at,omitempty"` // ms epoch
	ExpiresInDays    int    `json:"expires_in_days,omitempty"`
	Expired          bool   `json:"expired,omitempty"`
	// CanRefresh is true when a refresh token is present. An expired access
	// token with CanRefresh=true is BENIGN: the CLI silently mints a new one
	// on the next spawn. Only Expired && !CanRefresh means the user must
	// actually re-login. This is what lets the UI avoid the alarming
	// "expired token" for accounts that simply went idle overnight.
	CanRefresh bool `json:"can_refresh,omitempty"`
	// AccountUUID is the Anthropic account the credential ACTUALLY belongs to,
	// read from oauthAccount in .claude.json. It is the only trustworthy
	// identity: Account.Label/Email are compiled-in declarations that a
	// re-login can silently invalidate.
	AccountUUID string `json:"account_uuid,omitempty"`
	// IdentityMismatch is true when the credential sitting in this slot's
	// config dir belongs to a DIFFERENT identity than the slot declares.
	// The jordan slot hit exactly this: it was re-logged as sam,
	// so the panel rendered sam's quota and tokens under the label "Jordan" —
	// the same account drawn twice, one of them with the wrong name.
	//
	// A mismatched slot is treated as NOT logged in for display purposes: it
	// shows "awaiting login" and the login button, never the other
	// account's numbers. Showing a plausible wrong number is worse than
	// showing none, because nothing about it looks wrong.
	IdentityMismatch bool `json:"identity_mismatch,omitempty"`
}

// LoginStatus reads the credential metadata for an account without ever
// touching the token. Unknown account → zero value with LoggedIn=false.
func (s *Store) LoginStatus(accountID string) LoginStatus {
	acct, ok := s.AccountByID(accountID)
	if !ok {
		return LoginStatus{AccountID: accountID}
	}
	credPath, jsonPath := s.pathsFor(acct)
	st := LoginStatus{AccountID: accountID, Email: acct.Email}

	// claudeAiOauth lives in .credentials.json — its presence == logged in.
	if cred := readCredMeta(credPath); cred != nil {
		st.LoggedIn = true
		st.SubscriptionType = cred.SubscriptionType
		st.ExpiresAt = cred.ExpiresAt
		st.CanRefresh = cred.HasRefresh
		if cred.ExpiresAt > 0 {
			delta := time.Until(time.UnixMilli(cred.ExpiresAt))
			st.ExpiresInDays = int(delta.Hours() / 24)
			st.Expired = delta <= 0
		}
	}
	// Prefer the live identity from oauthAccount (.claude.json) over the
	// declared registry value when the account is actually logged in. The
	// declared value is an intention; the credential on disk is the fact.
	if id := readOauthIdentity(jsonPath); id.Email != "" {
		st.Email = id.Email
		st.AccountUUID = id.AccountUUID
		st.IdentityMismatch = !sameIdentity(acct.Email, id.Email)
	}
	return st
}

// sameIdentity compares a declared email against the live one. Empty declared
// means the slot makes no claim, so nothing can contradict it.
func sameIdentity(declarado, vivo string) bool {
	d := strings.ToLower(strings.TrimSpace(declarado))
	v := strings.ToLower(strings.TrimSpace(vivo))
	return d == "" || d == v
}

// comoOutraConta explains, without hedging, WHY the slot is awaiting login:
// there is a credential there, it just is not this account's. Without that
// sentence the operator sees "awaiting login" on a slot the panel calls logged in.
func comoOutraConta(emailVivo string) string {
	if strings.TrimSpace(emailVivo) == "" {
		return ""
	}
	return " (the credential in this dir belongs to " + emailVivo + ")"
}

// Identity is the live identity behind a config dir: who the credential
// REALLY belongs to, as opposed to who the registry says it should.
type Identity struct {
	AccountUUID string `json:"account_uuid,omitempty"`
	Email       string `json:"email,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
}

// AccountIdentity resolves an account's live identity from its config dir.
// Offline by design: accountUuid is already in .claude.json, so the panel
// never has to spend a request on the very rate limit it is trying to report.
func (s *Store) AccountIdentity(a Account) Identity {
	_, jsonPath := s.pathsFor(a)
	return readOauthIdentity(jsonPath)
}

// pathsFor resolves the credential + state file paths for an account, handling
// the two layouts: default ($HOME mode → .claude.json sits one level ABOVE
// .claude/) vs CLAUDE_CONFIG_DIR mode (.claude.json sits INSIDE the dir).
func (s *Store) pathsFor(a Account) (credPath, jsonPath string) {
	if a.ConfigDir == "" {
		// Default account: <claudeHome>/.credentials.json and the sibling
		// <parent>/.claude.json (e.g. /root/.claude/.credentials.json +
		// /root/.claude.json).
		return filepath.Join(s.claudeHome, ".credentials.json"),
			filepath.Join(filepath.Dir(s.claudeHome), ".claude.json")
	}
	return filepath.Join(a.ConfigDir, ".credentials.json"),
		filepath.Join(a.ConfigDir, ".claude.json")
}

type credMeta struct {
	ExpiresAt        int64
	SubscriptionType string
	HasRefresh       bool
}

// readCredMeta parses ONLY expiresAt + subscriptionType + the PRESENCE of a
// refresh token from claudeAiOauth. The accessToken value is never decoded;
// the refreshToken is decoded transiently only to compute a presence bool and
// is never returned — credMeta carries HasRefresh, not the token itself, so no
// secret escapes this function.
func readCredMeta(path string) *credMeta {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var doc struct {
		ClaudeAiOauth *struct {
			ExpiresAt        int64  `json:"expiresAt"`
			SubscriptionType string `json:"subscriptionType"`
			RefreshToken     string `json:"refreshToken"`
		} `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal(data, &doc); err != nil || doc.ClaudeAiOauth == nil {
		return nil
	}
	return &credMeta{
		ExpiresAt:        doc.ClaudeAiOauth.ExpiresAt,
		SubscriptionType: doc.ClaudeAiOauth.SubscriptionType,
		HasRefresh:       doc.ClaudeAiOauth.RefreshToken != "",
	}
}

// readOauthIdentity extracts the identity fields of oauthAccount from a
// .claude.json (zero value if absent/unreadable). No credential material is
// read — accountUuid/emailAddress/displayName are profile metadata, and the
// tokens live in .credentials.json, a different file this never opens.
func readOauthIdentity(path string) Identity {
	data, err := os.ReadFile(path)
	if err != nil {
		return Identity{}
	}
	var doc struct {
		OauthAccount *struct {
			AccountUUID  string `json:"accountUuid"`
			EmailAddress string `json:"emailAddress"`
			DisplayName  string `json:"displayName"`
		} `json:"oauthAccount"`
	}
	if err := json.Unmarshal(data, &doc); err != nil || doc.OauthAccount == nil {
		return Identity{}
	}
	return Identity{
		AccountUUID: doc.OauthAccount.AccountUUID,
		Email:       doc.OauthAccount.EmailAddress,
		DisplayName: doc.OauthAccount.DisplayName,
	}
}
