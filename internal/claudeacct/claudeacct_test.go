package claudeacct

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultAssignmentsAndConfigDir(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, filepath.Join(dir, "claude-home"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// Every consumer defaults to sam → ConfigDir = sam's provisioned dir.
	samDir := filepath.Join(accountsBaseDir(), "sam")
	for _, c := range []string{ConsumerJobs, ConsumerTerminal, ConsumerFork} {
		if got := s.AccountIDFor(c); got != DefaultAccountID {
			t.Errorf("AccountIDFor(%q) = %q, want %q", c, got, DefaultAccountID)
		}
		if got := s.ConfigDirFor(c); got != samDir {
			t.Errorf("ConfigDirFor(%q) = %q, want %q", c, got, samDir)
		}
	}

	// Unknown consumer falls back to default (sam).
	if got := s.ConfigDirFor("nonsense"); got != samDir {
		t.Errorf("ConfigDirFor(unknown) = %q, want %q", got, samDir)
	}

	// The seed file must have been written.
	if _, err := os.Stat(filepath.Join(dir, "claude_accounts.json")); err != nil {
		t.Errorf("seed file not written: %v", err)
	}
}

func TestAssignAndPersist(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, "")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	if err := s.Assign(ConsumerJobs, "jordan"); err != nil {
		t.Fatalf("Assign jobs→jordan: %v", err)
	}
	// Jobs → jordan (ConfigDir="" = the global ~/.claude); Terminal stays on the
	// default sam (provisioned dir). Tests explicit-assignment vs default.
	if got := s.ConfigDirFor(ConsumerJobs); got != "" {
		t.Errorf("ConfigDirFor(jobs) = %q, want \"\" (jordan)", got)
	}
	if got := s.ConfigDirFor(ConsumerTerminal); got != filepath.Join(accountsBaseDir(), "sam") {
		t.Errorf("ConfigDirFor(terminal) = %q, want sam dir (default)", got)
	}

	// Reopen → assignment persisted across process restart.
	s2, err := Open(dir, "")
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if got := s2.AccountIDFor(ConsumerJobs); got != "jordan" {
		t.Errorf("after reopen AccountIDFor(jobs) = %q, want jordan", got)
	}
}

func TestAssignRejectsInvalid(t *testing.T) {
	s, err := Open(t.TempDir(), "")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.Assign("bogus-consumer", "jordan"); err == nil {
		t.Error("Assign accepted unknown consumer")
	}
	if err := s.Assign(ConsumerJobs, "bogus-account"); err == nil {
		t.Error("Assign accepted unknown account")
	}
	// Rejected assignment must not have mutated state.
	if got := s.AccountIDFor(ConsumerJobs); got != DefaultAccountID {
		t.Errorf("state changed after rejected assign: %q", got)
	}
}

func TestGarbageAssignmentsDropped(t *testing.T) {
	dir := t.TempDir()
	// Hand-write a file with an unknown consumer and an unknown account.
	bad := `{"assignments":{"jobs":"sam","ghost":"jordan","terminal":"who"}}`
	if err := os.WriteFile(filepath.Join(dir, "claude_accounts.json"), []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir, "")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got := s.AccountIDFor(ConsumerJobs); got != "sam" {
		t.Errorf("valid pair dropped: jobs=%q", got)
	}
	// Unknown account "who" for terminal → dropped → default.
	if got := s.AccountIDFor(ConsumerTerminal); got != DefaultAccountID {
		t.Errorf("terminal should fall back to default, got %q", got)
	}
	// Unknown consumer "ghost" never appears.
	if _, ok := s.Assignments()["ghost"]; ok {
		t.Error("unknown consumer survived load")
	}
}

func TestLoginStatusMetadataOnly(t *testing.T) {
	dir := t.TempDir()
	// Isolate sam's config dir into the tempdir so the test never reads the
	// real host credential (/srv/agent-accounts/sam).
	t.Setenv("VPSM_CLAUDE_ACCOUNTS_DIR", filepath.Join(dir, "accounts"))
	home := filepath.Join(dir, "claude-home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	// Default account (jordan): credential one dir, .claude.json its sibling.
	exp := time.Now().Add(72 * time.Hour).UnixMilli()
	cred := `{"claudeAiOauth":{"accessToken":"SECRET-DO-NOT-LEAK","refreshToken":"ALSO-SECRET","expiresAt":` +
		itoa(exp) + `,"subscriptionType":"max"}}`
	if err := os.WriteFile(filepath.Join(home, ".credentials.json"), []byte(cred), 0o600); err != nil {
		t.Fatal(err)
	}
	cj := `{"oauthAccount":{"emailAddress":"jordan@northwind.example"}}`
	if err := os.WriteFile(filepath.Join(dir, ".claude.json"), []byte(cj), 0o600); err != nil {
		t.Fatal(err)
	}

	s, err := Open(dir, home)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	st := s.LoginStatus("jordan")
	if !st.LoggedIn {
		t.Error("expected LoggedIn=true")
	}
	if st.Email != "jordan@northwind.example" {
		t.Errorf("email = %q", st.Email)
	}
	if st.SubscriptionType != "max" {
		t.Errorf("subscriptionType = %q", st.SubscriptionType)
	}
	if st.ExpiresInDays < 2 || st.ExpiresInDays > 3 {
		t.Errorf("ExpiresInDays = %d, want ~3", st.ExpiresInDays)
	}
	if st.Expired {
		t.Error("should not be expired")
	}
	if !st.CanRefresh {
		t.Error("expected CanRefresh=true (fixture has a refreshToken)")
	}

	// sam not provisioned in this tempdir → LoggedIn=false, declared email.
	a := s.LoginStatus("sam")
	if a.LoggedIn {
		t.Error("sam should not be logged in in this tempdir")
	}
	if a.Email != "sam.rivera@personal.example" {
		t.Errorf("sam declared email = %q", a.Email)
	}
}

// TestLoginStatusExpiredRefreshable covers the label fix: an expired access
// token is BENIGN when a refresh token is present (CanRefresh=true → UI shows
// "renews on its own"), and only a genuine relogin case when it is absent
// (CanRefresh=false → UI shows "needs re-login").
func TestLoginStatusExpiredRefreshable(t *testing.T) {
	expired := time.Now().Add(-2 * time.Hour).UnixMilli()

	cases := []struct {
		name           string
		cred           string
		wantExpired    bool
		wantCanRefresh bool
	}{
		{
			name:           "expired but has refresh token (benign)",
			cred:           `{"claudeAiOauth":{"accessToken":"X","refreshToken":"R","expiresAt":` + itoa(expired) + `,"subscriptionType":"max"}}`,
			wantExpired:    true,
			wantCanRefresh: true,
		},
		{
			name:           "expired and no refresh token (needs relogin)",
			cred:           `{"claudeAiOauth":{"accessToken":"X","expiresAt":` + itoa(expired) + `,"subscriptionType":"max"}}`,
			wantExpired:    true,
			wantCanRefresh: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("VPSM_CLAUDE_ACCOUNTS_DIR", filepath.Join(dir, "accounts"))
			home := filepath.Join(dir, "claude-home")
			if err := os.MkdirAll(home, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(home, ".credentials.json"), []byte(tc.cred), 0o600); err != nil {
				t.Fatal(err)
			}
			s, err := Open(dir, home)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			st := s.LoginStatus("jordan")
			if !st.LoggedIn {
				t.Fatal("expected LoggedIn=true")
			}
			if st.Expired != tc.wantExpired {
				t.Errorf("Expired = %v, want %v", st.Expired, tc.wantExpired)
			}
			if st.CanRefresh != tc.wantCanRefresh {
				t.Errorf("CanRefresh = %v, want %v", st.CanRefresh, tc.wantCanRefresh)
			}
		})
	}
}

// itoa avoids importing strconv just for the test fixture.
func TestSetSessionAccount(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("VPSM_CLAUDE_ACCOUNTS_DIR", dir)
	s, err := Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}

	if err := s.SetSessionAccount("main", "sam"); err != nil {
		t.Fatalf("SetSessionAccount: %v", err)
	}
	if got := s.SessionAccountID("main", ConsumerTerminal); got != "sam" {
		t.Errorf("got %q, want sam", got)
	}

	if err := s.SetSessionAccount("main", ""); err != nil {
		t.Fatalf("remove override: %v", err)
	}
	if got := s.SessionAccountID("main", ConsumerTerminal); got != DefaultAccountID {
		t.Errorf("after remove: got %q, want %q", got, DefaultAccountID)
	}

	if err := s.SetSessionAccount("main", "unknown"); err == nil {
		t.Error("expected error for unknown account")
	}
}

func TestSessionOverridesPersistence(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("VPSM_CLAUDE_ACCOUNTS_DIR", dir)
	s, err := Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetSessionAccount("work", "sam"); err != nil {
		t.Fatal(err)
	}

	s2, err := Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := s2.SessionAccountID("work", ConsumerTerminal); got != "sam" {
		t.Errorf("after reopen: got %q, want sam", got)
	}
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
