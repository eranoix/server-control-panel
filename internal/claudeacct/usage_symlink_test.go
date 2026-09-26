package claudeacct

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// escreveTranscript writes a minimal transcript with one assistant line.
func escreveTranscript(t *testing.T, dir, nome string, tokens int64) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	linha := `{"type":"assistant","timestamp":"` + time.Now().UTC().Format(time.RFC3339) +
		`","message":{"model":"claude-opus-5","usage":{"input_tokens":` +
		strconv.FormatInt(tokens, 10) + `,"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, nome), []byte(linha), 0o644); err != nil {
		t.Fatal(err)
	}
}

// escreveIdentidade writes the .claude.json with an email's oauthAccount.
//
// `dir` is the DEFAULT account's claudeHome, and for it the .claude.json lives
// one level ABOVE (pathsFor: /root/.claude/ but /root/.claude.json). Writing it
// inside leaves the identity unreadable — and an unreadable identity also yields
// mismatch=false, meaning the positive test would pass without reading anything.
func escreveIdentidade(t *testing.T, dir, email, uuid string) {
	t.Helper()
	escreveIdentidadeNoDir(t, filepath.Dir(dir), email, uuid)
}

// escreveIdentidadeNoDir writes the .claude.json exactly in the dir given (the
// non-default accounts keep the file INSIDE their own config dir).
func escreveIdentidadeNoDir(t *testing.T, dir, email, uuid string) {
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

// Each account's projects/ became a SYMLINK to a shared tree (a89bfc2, so that
// `claude --continue` would survive an account swap), and filepath.WalkDir does
// not follow a symlink, not even at the root: the sweep started visiting ONE
// entry — the link itself — and every metric went to zero, in silence, for a
// week.
//
// The test that existed built projects/ as a REAL directory, which is exactly
// the case that does not break. This one builds the case that did break.
func TestUsageAtravessaProjectsSymlinkado(t *testing.T) {
	base := t.TempDir()
	t.Setenv("VPSM_CLAUDE_ACCOUNTS_DIR", filepath.Join(base, "accounts"))

	compartilhado := filepath.Join(base, "shared", "projects")
	escreveTranscript(t, filepath.Join(compartilhado, "-repo"), "s1.jsonl", 1_000_000)

	home := filepath.Join(base, "claude-home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	// This is what the host has: projects/ is a link, not a directory.
	if err := os.Symlink(compartilhado, filepath.Join(home, "projects")); err != nil {
		t.Fatal(err)
	}
	escreveIdentidade(t, home, "jordan@northwind.example", "uuid-jordan")

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
	if u.Dir != mustEval(t, compartilhado) {
		t.Errorf("Dir=%q, wanted the resolved tree %q", u.Dir, compartilhado)
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

// An empty sweep must NOT be indistinguishable from "account with no usage": it
// was that ambiguity that kept the symlink regression invisible. A card with no
// number has to say why.
func TestUsageVaziaExplicaOMotivo(t *testing.T) {
	base := t.TempDir()
	t.Setenv("VPSM_CLAUDE_ACCOUNTS_DIR", filepath.Join(base, "accounts"))
	home := filepath.Join(base, "claude-home")
	if err := os.MkdirAll(filepath.Join(home, "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	escreveIdentidade(t, home, "jordan@northwind.example", "uuid-jordan")

	s, _ := Open(base, home)
	jordan, _ := s.AccountByID("jordan")
	if u := s.Usage(jordan); u.Error == "" {
		t.Error("an empty rollup came out with no Error — a silent 0 is the bug this test exists to prevent")
	}

	// A non-existent dir: it also explains, and does not panic.
	sam, _ := s.AccountByID("sam")
	if u := s.Usage(sam); u.Error == "" {
		t.Error("a nonexistent dir came out with no Error")
	}
}

// The credential sitting in jordan's dir belongs to sam (it was re-logged). The
// panel drew sam's quota and tokens under the label "Jordan": the same account
// twice, one of them with the wrong name. A plausible number under the wrong
// name is worse than no number at all, because nothing about it looks wrong.
func TestSlotComCredencialDeOutraContaNaoMostraNumero(t *testing.T) {
	base := t.TempDir()
	t.Setenv("VPSM_CLAUDE_ACCOUNTS_DIR", filepath.Join(base, "accounts"))

	compartilhado := filepath.Join(base, "shared", "projects")
	escreveTranscript(t, filepath.Join(compartilhado, "-repo"), "s1.jsonl", 5_000_000)

	home := filepath.Join(base, "claude-home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(compartilhado, filepath.Join(home, "projects")); err != nil {
		t.Fatal(err)
	}
	// The jordan slot declares jordan, but the credential in the dir is sam's.
	escreveIdentidade(t, home, "sam.rivera@personal.example", "uuid-sam")

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

// The identity declared in the registry is an INTENTION; the credential on disk
// is the fact. When they match nothing is blocked — the gate must not be a general brake.
func TestIdentidadeQueBateNaoBloqueia(t *testing.T) {
	base := t.TempDir()
	t.Setenv("VPSM_CLAUDE_ACCOUNTS_DIR", filepath.Join(base, "accounts"))

	compartilhado := filepath.Join(base, "shared", "projects")
	escreveTranscript(t, filepath.Join(compartilhado, "-repo"), "s1.jsonl", 7_000_000)

	home := filepath.Join(base, "claude-home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(compartilhado, filepath.Join(home, "projects")); err != nil {
		t.Fatal(err)
	}
	escreveIdentidade(t, home, "Jordan@Northwind.example ", "uuid-jordan") // case/space do not matter

	s, _ := Open(base, home)
	jordan, _ := s.AccountByID("jordan")
	ls := s.LoginStatus("jordan")
	// Vacuity guard: an UNREADABLE identity also yields mismatch=false, so
	// without this assertion the test would pass without having read anything.
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
