package claudeacct

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func writeTranscript(t *testing.T, dir, name string, tokens int64) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"assistant","timestamp":"` + time.Now().UTC().Format(time.RFC3339) +
		`","message":{"model":"claude-opus-5","usage":{"input_tokens":` +
		strconv.FormatInt(tokens, 10) + `,"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeIdentity(t *testing.T, dir, email, uuid string) {
	t.Helper()
	writeIdentityInDir(t, filepath.Dir(dir), email, uuid)
}

func writeIdentityInDir(t *testing.T, dir, email, uuid string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	doc := map[string]any{"oauthAccount": map[string]any{
		"accountUuid": uuid, "emailAddress": email, "displayName": "X",
	}}
	b, _ := json.Marshal(doc)
	if err := os.WriteFile(filepath.Join(dir, ".claude.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestUsageFollowsSymlinkedProjects(t *testing.T) {
	base := t.TempDir()
	t.Setenv("PANEL_CLAUDE_ACCOUNTS_DIR", filepath.Join(base, "accounts"))

	shared := filepath.Join(base, "shared", "projects")
	writeTranscript(t, filepath.Join(shared, "-repo"), "s1.jsonl", 1_000_000)

	home := filepath.Join(base, "claude-home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(shared, filepath.Join(home, "projects")); err != nil {
		t.Fatal(err)
	}
	writeIdentity(t, home, "jordan@northwind.example", "uuid-jordan")

	s, err := Open(base, home)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	jordan, _ := s.AccountByID("jordan")
	u := s.Usage(jordan)

	if u.Total.InputTokens != 1_000_000 {
		t.Fatalf("the scan did not cross the symlink: input=%d, wanted 1M (error=%q)",
			u.Total.InputTokens, u.Error)
	}
	if u.Sessions != 1 {
		t.Errorf("sessions=%d, wanted 1", u.Sessions)
	}
	if u.Dir != mustEval(t, shared) {
		t.Errorf("Dir=%q, wanted the resolved tree %q", u.Dir, shared)
	}
}

func mustEval(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestEmptyUsageExplainsReason(t *testing.T) {
	base := t.TempDir()
	t.Setenv("PANEL_CLAUDE_ACCOUNTS_DIR", filepath.Join(base, "accounts"))
	home := filepath.Join(base, "claude-home")
	if err := os.MkdirAll(filepath.Join(home, "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeIdentity(t, home, "jordan@northwind.example", "uuid-jordan")

	s, _ := Open(base, home)
	jordan, _ := s.AccountByID("jordan")
	if u := s.Usage(jordan); u.Error == "" {
		t.Error("an empty rollup came out with no Error — a silent 0 is the bug this test exists to prevent")
	}

	sam, _ := s.AccountByID("sam")
	if u := s.Usage(sam); u.Error == "" {
		t.Error("a nonexistent dir came out with no Error")
	}
}

func TestSlotWithOtherAccountCredentialHidesNumber(t *testing.T) {
	base := t.TempDir()
	t.Setenv("PANEL_CLAUDE_ACCOUNTS_DIR", filepath.Join(base, "accounts"))

	shared := filepath.Join(base, "shared", "projects")
	writeTranscript(t, filepath.Join(shared, "-repo"), "s1.jsonl", 5_000_000)

	home := filepath.Join(base, "claude-home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(shared, filepath.Join(home, "projects")); err != nil {
		t.Fatal(err)
	}
	writeIdentity(t, home, "sam.rivera@personal.example", "uuid-sam")

	s, _ := Open(base, home)
	jordan, _ := s.AccountByID("jordan")

	ls := s.LoginStatus("jordan")
	if !ls.IdentityMismatch {
		t.Fatal("mismatch not detected: the slot declares jordan and carries sam's credential")
	}
	if ls.AccountUUID != "uuid-sam" {
		t.Errorf("account_uuid=%q, wanted the LIVE identity (uuid-sam)", ls.AccountUUID)
	}

	u := s.Usage(jordan)
	if u.Total.TotalTokens() != 0 {
		t.Errorf("a slot in mismatch showed %d tokens — they belong to another account", u.Total.TotalTokens())
	}
	if u.Error == "" {
		t.Error("a slot in mismatch came out with no Error explaining that it is waiting for login")
	}

	rl := s.RateLimits(jordan)
	if rl.LoggedIn {
		t.Error("a slot in mismatch reported logged_in — the quota bar would be someone else's")
	}
	if len(rl.Windows) != 0 {
		t.Errorf("a slot in mismatch brought %d quota windows belonging to someone else", len(rl.Windows))
	}
}

func TestMatchingIdentityDoesNotBlock(t *testing.T) {
	base := t.TempDir()
	t.Setenv("PANEL_CLAUDE_ACCOUNTS_DIR", filepath.Join(base, "accounts"))

	shared := filepath.Join(base, "shared", "projects")
	writeTranscript(t, filepath.Join(shared, "-repo"), "s1.jsonl", 7_000_000)

	home := filepath.Join(base, "claude-home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(shared, filepath.Join(home, "projects")); err != nil {
		t.Fatal(err)
	}
	writeIdentity(t, home, "Jordan@Northwind.example ", "uuid-jordan")

	s, _ := Open(base, home)
	jordan, _ := s.AccountByID("jordan")
	ls := s.LoginStatus("jordan")
	if ls.AccountUUID != "uuid-jordan" {
		t.Fatalf("the identity was not read (uuid=%q) — the test would pass empty", ls.AccountUUID)
	}
	if ls.IdentityMismatch {
		t.Fatal("the correct identity was marked as a mismatch (the comparison must ignore case and whitespace)")
	}
	if u := s.Usage(jordan); u.Total.InputTokens != 7_000_000 {
		t.Errorf("a legitimate account did not get its numbers: %d (error=%q)", u.Total.InputTokens, u.Error)
	}
}
