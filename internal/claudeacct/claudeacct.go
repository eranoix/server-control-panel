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

const DefaultAccountID = "sam"

const (
	ConsumerJobs     = "jobs"
	ConsumerTerminal = "terminal"
	ConsumerFork     = "fork"
)

var consumerOrder = []string{ConsumerJobs, ConsumerTerminal, ConsumerFork}

type Account struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Email     string `json:"email"`
	ConfigDir string `json:"config_dir"`
}

func accountsBaseDir() string {
	if v := strings.TrimSpace(os.Getenv("PANEL_CLAUDE_ACCOUNTS_DIR")); v != "" {
		return v
	}
	return "/srv/agent-accounts"
}

func defaultRegistry() []Account {
	return []Account{
		{ID: "jordan", Label: "Jordan", Email: "jordan@northwind.example", ConfigDir: ""},
		{ID: "sam", Label: "Sam", Email: "sam.rivera@personal.example", ConfigDir: filepath.Join(accountsBaseDir(), "sam")},
	}
}

type Store struct {
	mu               sync.RWMutex
	path             string
	claudeHome       string
	accounts         []Account
	assignments      map[string]string
	sessionOverrides map[string]string
}

type persisted struct {
	Assignments      map[string]string `json:"assignments"`
	SessionOverrides map[string]string `json:"session_overrides,omitempty"`
}

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

func (s *Store) AccountIDFor(consumer string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if id, ok := s.assignments[consumer]; ok && s.hasAccountLocked(id) {
		return id
	}
	return DefaultAccountID
}

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

func (s *Store) ConfigDirForSession(session, consumer string) string {
	s.mu.RLock()
	id, hasOverride := s.sessionOverrides[session]
	overrideValid := hasOverride && s.hasAccountLocked(id)
	s.mu.RUnlock()

	if !overrideValid {
		id = s.AccountIDFor(consumer)
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

func (s *Store) Accounts() []Account {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Account, len(s.accounts))
	copy(out, s.accounts)
	return out
}

func (s *Store) Consumers() []string {
	out := make([]string, len(consumerOrder))
	copy(out, consumerOrder)
	return out
}

func (s *Store) Assignments() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]string, len(s.assignments))
	for k, v := range s.assignments {
		out[k] = v
	}
	return out
}

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

type LoginStatus struct {
	AccountID        string `json:"account_id"`
	LoggedIn         bool   `json:"logged_in"`
	Email            string `json:"email,omitempty"`
	SubscriptionType string `json:"subscription_type,omitempty"`
	ExpiresAt        int64  `json:"expires_at,omitempty"`
	ExpiresInDays    int    `json:"expires_in_days,omitempty"`
	Expired          bool   `json:"expired,omitempty"`
	CanRefresh       bool   `json:"can_refresh,omitempty"`
	AccountUUID      string `json:"account_uuid,omitempty"`
	IdentityMismatch bool   `json:"identity_mismatch,omitempty"`
}

func (s *Store) LoginStatus(accountID string) LoginStatus {
	acct, ok := s.AccountByID(accountID)
	if !ok {
		return LoginStatus{AccountID: accountID}
	}
	credPath, jsonPath := s.pathsFor(acct)
	st := LoginStatus{AccountID: accountID, Email: acct.Email}

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
	if id := readOauthIdentity(jsonPath); id.Email != "" {
		st.Email = id.Email
		st.AccountUUID = id.AccountUUID
		st.IdentityMismatch = !sameIdentity(acct.Email, id.Email)
	}
	return st
}

func sameIdentity(declared, live string) bool {
	d := strings.ToLower(strings.TrimSpace(declared))
	v := strings.ToLower(strings.TrimSpace(live))
	return d == "" || d == v
}

func asOtherAccount(liveEmail string) string {
	if strings.TrimSpace(liveEmail) == "" {
		return ""
	}
	return " (the credential in this dir belongs to " + liveEmail + ")"
}

type Identity struct {
	AccountUUID string `json:"account_uuid,omitempty"`
	Email       string `json:"email,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
}

func (s *Store) AccountIdentity(a Account) Identity {
	_, jsonPath := s.pathsFor(a)
	return readOauthIdentity(jsonPath)
}

func (s *Store) pathsFor(a Account) (credPath, jsonPath string) {
	if a.ConfigDir == "" {
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
