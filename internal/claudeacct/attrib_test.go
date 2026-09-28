package claudeacct

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func setupTwoAccounts(t *testing.T) (*Store, string) {
	t.Helper()
	base := t.TempDir()
	accounts := filepath.Join(base, "accounts")
	t.Setenv("PANEL_CLAUDE_ACCOUNTS_DIR", accounts)

	shared := filepath.Join(base, "shared", "projects")
	if err := os.MkdirAll(filepath.Join(shared, "-repo"), 0o755); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(base, "claude-home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(shared, filepath.Join(home, "projects")); err != nil {
		t.Fatal(err)
	}
	writeIdentity(t, home, "jordan@northwind.example", "uuid-jordan")

	dirSam := filepath.Join(accounts, "sam")
	if err := os.MkdirAll(dirSam, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(shared, filepath.Join(dirSam, "projects")); err != nil {
		t.Fatal(err)
	}
	writeIdentityInDir(t, dirSam, "sam.rivera@personal.example", "uuid-sam")

	s, err := Open(base, home)
	if err != nil {
		t.Fatal(err)
	}
	return s, filepath.Join(shared, "-repo")
}

func writeMessages(t *testing.T, dir, session string, when []time.Time, tokens int64) {
	t.Helper()
	var buf []byte
	for _, ts := range when {
		buf = append(buf, []byte(`{"type":"assistant","sessionId":"`+session+
			`","timestamp":"`+ts.UTC().Format(time.RFC3339)+
			`","message":{"model":"claude-opus-5","usage":{"input_tokens":`+
			strconv.FormatInt(tokens, 10)+
			`,"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}`+"\n")...)
	}
	if err := os.WriteFile(filepath.Join(dir, session+".jsonl"), buf, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMidSessionSwapSplitsTokens(t *testing.T) {
	s, repo := setupTwoAccounts(t)

	now := time.Now()
	before := now.Add(-40 * time.Minute)
	after := now.Add(-10 * time.Minute)
	writeMessages(t, repo, "sess-swap", []time.Time{before, after}, 1_000_000)

	if err := s.RecordAttrib(AttribEntry{
		Ts: before.Add(-time.Minute).Unix(), SessionID: "sess-swap", AccountID: "jordan", Source: "startup",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordAttrib(AttribEntry{
		Ts: now.Add(-20 * time.Minute).Unix(), SessionID: "sess-swap", AccountID: "sam", Source: "resume",
	}); err != nil {
		t.Fatal(err)
	}

	rep := s.UsageAll()
	byAccount := map[string]int64{}
	for _, u := range rep.Accounts {
		byAccount[u.AccountID] = u.Total.InputTokens
	}
	if byAccount["jordan"] != 1_000_000 {
		t.Errorf("jordan ended up with %d, wanted the 1M from BEFORE the switch", byAccount["jordan"])
	}
	if byAccount["sam"] != 1_000_000 {
		t.Errorf("sam ended up with %d, wanted the 1M from AFTER the switch", byAccount["sam"])
	}
	if rep.Unattributed.Total.InputTokens != 0 {
		t.Errorf("%d left unattributed in a session fully covered by the ledger",
			rep.Unattributed.Total.InputTokens)
	}
}

func TestNothingLostOrDoubled(t *testing.T) {
	s, repo := setupTwoAccounts(t)
	now := time.Now()

	writeMessages(t, repo, "sess-a", []time.Time{now.Add(-time.Hour)}, 3_000_000)
	writeMessages(t, repo, "sess-b", []time.Time{now.Add(-2 * time.Hour)}, 5_000_000)
	writeMessages(t, repo, "sess-orphan", []time.Time{now.Add(-3 * time.Hour)}, 7_000_000)

	_ = s.RecordAttrib(AttribEntry{Ts: now.Add(-90 * time.Minute).Unix(), SessionID: "sess-a", AccountID: "sam"})
	_ = s.RecordAttrib(AttribEntry{Ts: now.Add(-3 * time.Hour).Unix(), SessionID: "sess-b", AccountID: "jordan"})

	rep := s.UsageAll()
	var sum int64
	for _, u := range rep.Accounts {
		sum += u.Total.InputTokens
	}
	sum += rep.Unattributed.Total.InputTokens

	const raw = 3_000_000 + 5_000_000 + 7_000_000
	if sum != raw {
		t.Errorf("sum(accounts)+unattributed = %d, wanted the raw %d", sum, raw)
	}
	if rep.Unattributed.Total.InputTokens != 7_000_000 {
		t.Errorf("the orphan landed somewhere: unattributed = %d, wanted 7M",
			rep.Unattributed.Total.InputTokens)
	}
	if !rep.SharedTree {
		t.Error("the shared tree was not flagged")
	}
}

func TestFirstEntryBackdatesButLaterOnesDoNot(t *testing.T) {
	s, repo := setupTwoAccounts(t)
	now := time.Now()
	first := now.Add(-30 * time.Minute)

	writeMessages(t, repo, "sess-c", []time.Time{first}, 2_000_000)
	_ = s.RecordAttrib(AttribEntry{Ts: first.Add(2 * time.Second).Unix(), SessionID: "sess-c", AccountID: "sam"})

	rep := s.UsageAll()
	for _, u := range rep.Accounts {
		if u.AccountID == "sam" && u.Total.InputTokens != 2_000_000 {
			t.Errorf("sam = %d: the first entry has to reach back to the start of the session", u.Total.InputTokens)
		}
	}
	if rep.Unattributed.Total.InputTokens != 0 {
		t.Errorf("the message before the milestone fell into the bucket (%d) instead of reaching back",
			rep.Unattributed.Total.InputTokens)
	}
}

func TestLedgerRejectsInvalidEntry(t *testing.T) {
	s, _ := setupTwoAccounts(t)
	_ = s.RecordAttrib(AttribEntry{Ts: 1, SessionID: "", AccountID: "sam"})
	_ = s.RecordAttrib(AttribEntry{Ts: 1, SessionID: "x", AccountID: ""})
	_ = s.RecordAttrib(AttribEntry{Ts: 1, SessionID: "x", AccountID: "missing-account"})
	if n := len(s.loadLedger().bySession); n != 0 {
		t.Errorf("the ledger accepted %d invalid entries", n)
	}
}

func TestConfigDirMapsToAccount(t *testing.T) {
	s, _ := setupTwoAccounts(t)
	if id := s.AccountIDForConfigDir(""); id != "jordan" {
		t.Errorf("empty dir → %q, wanted the default account jordan", id)
	}
	sam, _ := s.AccountByID("sam")
	if id := s.AccountIDForConfigDir(sam.ConfigDir + "/"); id != "sam" {
		t.Errorf("sam's dir (with a trailing slash) → %q", id)
	}
	if id := s.AccountIDForConfigDir("/dir/made-up"); id != "" {
		t.Errorf("unknown dir → %q, wanted empty", id)
	}
}

func TestBlockedAccountKeepsTokens(t *testing.T) {
	base := t.TempDir()
	accounts := filepath.Join(base, "accounts")
	t.Setenv("PANEL_CLAUDE_ACCOUNTS_DIR", accounts)

	shared := filepath.Join(base, "shared", "projects")
	repo := filepath.Join(shared, "-repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(base, "claude-home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(shared, filepath.Join(home, "projects")); err != nil {
		t.Fatal(err)
	}
	writeIdentity(t, home, "sam.rivera@personal.example", "uuid-sam")

	dirSam := filepath.Join(accounts, "sam")
	if err := os.MkdirAll(dirSam, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(shared, filepath.Join(dirSam, "projects")); err != nil {
		t.Fatal(err)
	}
	writeIdentityInDir(t, dirSam, "sam.rivera@personal.example", "uuid-sam")

	s, err := Open(base, home)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	writeMessages(t, repo, "sess-of-jordan", []time.Time{now.Add(-time.Hour)}, 9_000_000)
	_ = s.RecordAttrib(AttribEntry{Ts: now.Add(-2 * time.Hour).Unix(), SessionID: "sess-of-jordan", AccountID: "jordan"})

	rep := s.UsageAll()
	var sum int64
	for _, u := range rep.Accounts {
		sum += u.Total.InputTokens
	}
	sum += rep.Unattributed.Total.InputTokens
	if sum != 9_000_000 {
		t.Errorf("sum = %d, wanted 9M: the blocked account's token evaporated", sum)
	}
	if rep.Unattributed.Total.InputTokens != 9_000_000 {
		t.Errorf("unattributed = %d, wanted the 9M from the blocked account",
			rep.Unattributed.Total.InputTokens)
	}
}
