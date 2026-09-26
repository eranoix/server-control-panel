package claudeacct

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// setupTwoAccounts builds the host's real scenario: both accounts pointing at the
// SAME transcript tree (which is what `claude --continue` requires in order to
// survive the swap), both logged in with the identity they declare.
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
	// sam is not the default account: its .claude.json goes INSIDE the config dir.
	writeIdentityInDir(t, dirSam, "sam.rivera@personal.example", "uuid-sam")

	s, err := Open(base, home)
	if err != nil {
		t.Fatal(err)
	}
	return s, filepath.Join(shared, "-repo")
}

// writeMessages writes a transcript with one assistant line per instant.
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

// The live swap is the case that makes the ledger work by INTERVAL and not by
// session: the same transcript goes on being written after the account switch
// (that is what `--continue` does), so the tokens from before and from after
// have different owners INSIDE the same file.
func TestMidSessionSwapSplitsTokens(t *testing.T) {
	s, repo := setupTwoAccounts(t)

	now := time.Now()
	before := now.Add(-40 * time.Minute)
	after := now.Add(-10 * time.Minute)
	writeMessages(t, repo, "sess-swap", []time.Time{before, after}, 1_000_000)

	// Started on jordan; 20 min later switched to sam.
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

// Conservation: no token may vanish or be counted twice. With the shared tree,
// sweeping per account instead of per directory would double the total — and the
// result would look perfectly plausible.
func TestNothingLostOrDoubled(t *testing.T) {
	s, repo := setupTwoAccounts(t)
	now := time.Now()

	writeMessages(t, repo, "sess-a", []time.Time{now.Add(-time.Hour)}, 3_000_000)
	writeMessages(t, repo, "sess-b", []time.Time{now.Add(-2 * time.Hour)}, 5_000_000)
	writeMessages(t, repo, "sess-orphan", []time.Time{now.Add(-3 * time.Hour)}, 7_000_000)

	_ = s.RecordAttrib(AttribEntry{Ts: now.Add(-90 * time.Minute).Unix(), SessionID: "sess-a", AccountID: "sam"})
	_ = s.RecordAttrib(AttribEntry{Ts: now.Add(-3 * time.Hour).Unix(), SessionID: "sess-b", AccountID: "jordan"})
	// sess-orphan is deliberately left OUT of the ledger.

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

// A session's first entry applies RETROACTIVELY: the SessionStart hook fires a
// few ms AFTER claude opens the transcript, so requiring ts >= entry would
// discard the first messages of every session — hence the common case of a short
// one. The entries that FOLLOW cannot be back-dated: they mark real switches,
// and back-dating would steal tokens from the previous account.
func TestFirstEntryBackdatesButLaterOnesDoNot(t *testing.T) {
	s, repo := setupTwoAccounts(t)
	now := time.Now()
	first := now.Add(-30 * time.Minute)

	writeMessages(t, repo, "sess-c", []time.Time{first}, 2_000_000)
	// Hook recorded AFTER the first message, as it actually happens.
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

// An invalid entry must not poison the ledger nor be silently accepted as though
// it attributed something.
func TestLedgerRejectsInvalidEntry(t *testing.T) {
	s, _ := setupTwoAccounts(t)
	_ = s.RecordAttrib(AttribEntry{Ts: 1, SessionID: "", AccountID: "sam"})
	_ = s.RecordAttrib(AttribEntry{Ts: 1, SessionID: "x", AccountID: ""})
	_ = s.RecordAttrib(AttribEntry{Ts: 1, SessionID: "x", AccountID: "missing-account"})
	if n := len(s.loadLedger().bySession); n != 0 {
		t.Errorf("the ledger accepted %d invalid entries", n)
	}
}

// The dir → account mapping is what connects the hook (which knows only the
// CLAUDE_CONFIG_DIR) to the registry. An empty Dir is the default account, not "unknown".
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

// An account BLOCKED by the identity gate must not make a token evaporate. The
// token existed; what is missing is knowing which slot to credit it to — and
// that is exactly the definition of the bucket. Dropping it silently would break
// the one property that makes the panel auditable: sum(accounts) + unattributed
// == the tree's gross total.
//
// This test exists because the first version did drop them: a probe against the
// real data showed 790 million tokens vanishing between one total and another.
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
	// jordan carries sam's credential → blocked.
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
