package claudeacct

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// montaDuasContas builds the host's real scenario: both accounts pointing at the
// SAME transcript tree (which is what `claude --continue` requires in order to
// survive the swap), both logged in with the identity they declare.
func montaDuasContas(t *testing.T) (*Store, string) {
	t.Helper()
	base := t.TempDir()
	contas := filepath.Join(base, "accounts")
	t.Setenv("VPSM_CLAUDE_ACCOUNTS_DIR", contas)

	compartilhado := filepath.Join(base, "shared", "projects")
	if err := os.MkdirAll(filepath.Join(compartilhado, "-repo"), 0o755); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(base, "claude-home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(compartilhado, filepath.Join(home, "projects")); err != nil {
		t.Fatal(err)
	}
	escreveIdentidade(t, home, "jordan@northwind.example", "uuid-jordan")

	dirSam := filepath.Join(contas, "sam")
	if err := os.MkdirAll(dirSam, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(compartilhado, filepath.Join(dirSam, "projects")); err != nil {
		t.Fatal(err)
	}
	// sam is not the default account: its .claude.json goes INSIDE the config dir.
	escreveIdentidadeNoDir(t, dirSam, "sam.rivera@personal.example", "uuid-sam")

	s, err := Open(base, home)
	if err != nil {
		t.Fatal(err)
	}
	return s, filepath.Join(compartilhado, "-repo")
}

// escreveMensagens writes a transcript with one assistant line per instant.
func escreveMensagens(t *testing.T, dir, sessao string, quando []time.Time, tokens int64) {
	t.Helper()
	var buf []byte
	for _, ts := range quando {
		buf = append(buf, []byte(`{"type":"assistant","sessionId":"`+sessao+
			`","timestamp":"`+ts.UTC().Format(time.RFC3339)+
			`","message":{"model":"claude-opus-5","usage":{"input_tokens":`+
			strconv.FormatInt(tokens, 10)+
			`,"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}`+"\n")...)
	}
	if err := os.WriteFile(filepath.Join(dir, sessao+".jsonl"), buf, 0o644); err != nil {
		t.Fatal(err)
	}
}

// The live swap is the case that makes the ledger work by INTERVAL and not by
// session: the same transcript goes on being written after the account switch
// (that is what `--continue` does), so the tokens from before and from after
// have different owners INSIDE the same file.
func TestSwapNoMeioDaSessaoParteOsTokens(t *testing.T) {
	s, repo := montaDuasContas(t)

	agora := time.Now()
	antes := agora.Add(-40 * time.Minute)
	depois := agora.Add(-10 * time.Minute)
	escreveMensagens(t, repo, "sess-swap", []time.Time{antes, depois}, 1_000_000)

	// Started on jordan; 20 min later switched to sam.
	if err := s.RecordAttrib(AttribEntry{
		Ts: antes.Add(-time.Minute).Unix(), SessionID: "sess-swap", AccountID: "jordan", Source: "startup",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordAttrib(AttribEntry{
		Ts: agora.Add(-20 * time.Minute).Unix(), SessionID: "sess-swap", AccountID: "sam", Source: "resume",
	}); err != nil {
		t.Fatal(err)
	}

	rep := s.UsageAll()
	porConta := map[string]int64{}
	for _, u := range rep.Accounts {
		porConta[u.AccountID] = u.Total.InputTokens
	}
	if porConta["jordan"] != 1_000_000 {
		t.Errorf("jordan ended up with %d, wanted the 1M from BEFORE the switch", porConta["jordan"])
	}
	if porConta["sam"] != 1_000_000 {
		t.Errorf("sam ended up with %d, wanted the 1M from AFTER the switch", porConta["sam"])
	}
	if rep.Unattributed.Total.InputTokens != 0 {
		t.Errorf("%d left unattributed in a session fully covered by the ledger",
			rep.Unattributed.Total.InputTokens)
	}
}

// Conservation: no token may vanish or be counted twice. With the shared tree,
// sweeping per account instead of per directory would double the total — and the
// result would look perfectly plausible.
func TestNadaSomeNemDobra(t *testing.T) {
	s, repo := montaDuasContas(t)
	agora := time.Now()

	escreveMensagens(t, repo, "sess-a", []time.Time{agora.Add(-time.Hour)}, 3_000_000)
	escreveMensagens(t, repo, "sess-b", []time.Time{agora.Add(-2 * time.Hour)}, 5_000_000)
	escreveMensagens(t, repo, "sess-orfa", []time.Time{agora.Add(-3 * time.Hour)}, 7_000_000)

	_ = s.RecordAttrib(AttribEntry{Ts: agora.Add(-90 * time.Minute).Unix(), SessionID: "sess-a", AccountID: "sam"})
	_ = s.RecordAttrib(AttribEntry{Ts: agora.Add(-3 * time.Hour).Unix(), SessionID: "sess-b", AccountID: "jordan"})
	// sess-orfa is deliberately left OUT of the ledger.

	rep := s.UsageAll()
	var soma int64
	for _, u := range rep.Accounts {
		soma += u.Total.InputTokens
	}
	soma += rep.Unattributed.Total.InputTokens

	const bruto = 3_000_000 + 5_000_000 + 7_000_000
	if soma != bruto {
		t.Errorf("sum(accounts)+unattributed = %d, wanted the raw %d", soma, bruto)
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
func TestPrimeiraEntradaRetroageMasAsSeguintesNao(t *testing.T) {
	s, repo := montaDuasContas(t)
	agora := time.Now()
	primeira := agora.Add(-30 * time.Minute)

	escreveMensagens(t, repo, "sess-c", []time.Time{primeira}, 2_000_000)
	// Hook recorded AFTER the first message, as it actually happens.
	_ = s.RecordAttrib(AttribEntry{Ts: primeira.Add(2 * time.Second).Unix(), SessionID: "sess-c", AccountID: "sam"})

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
func TestLedgerRejeitaEntradaInvalida(t *testing.T) {
	s, _ := montaDuasContas(t)
	_ = s.RecordAttrib(AttribEntry{Ts: 1, SessionID: "", AccountID: "sam"})
	_ = s.RecordAttrib(AttribEntry{Ts: 1, SessionID: "x", AccountID: ""})
	_ = s.RecordAttrib(AttribEntry{Ts: 1, SessionID: "x", AccountID: "conta-que-nao-existe"})
	if n := len(s.carregaLedger().porSessao); n != 0 {
		t.Errorf("the ledger accepted %d invalid entries", n)
	}
}

// The dir → account mapping is what connects the hook (which knows only the
// CLAUDE_CONFIG_DIR) to the registry. An empty Dir is the default account, not "unknown".
func TestConfigDirMapeiaParaConta(t *testing.T) {
	s, _ := montaDuasContas(t)
	if id := s.AccountIDForConfigDir(""); id != "jordan" {
		t.Errorf("empty dir → %q, wanted the default account jordan", id)
	}
	sam, _ := s.AccountByID("sam")
	if id := s.AccountIDForConfigDir(sam.ConfigDir + "/"); id != "sam" {
		t.Errorf("sam's dir (with a trailing slash) → %q", id)
	}
	if id := s.AccountIDForConfigDir("/dir/inventado"); id != "" {
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
func TestContaBloqueadaNaoEvaporaToken(t *testing.T) {
	base := t.TempDir()
	contas := filepath.Join(base, "accounts")
	t.Setenv("VPSM_CLAUDE_ACCOUNTS_DIR", contas)

	compartilhado := filepath.Join(base, "shared", "projects")
	repo := filepath.Join(compartilhado, "-repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(base, "claude-home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(compartilhado, filepath.Join(home, "projects")); err != nil {
		t.Fatal(err)
	}
	// jordan carries sam's credential → blocked.
	escreveIdentidade(t, home, "sam.rivera@personal.example", "uuid-sam")

	dirSam := filepath.Join(contas, "sam")
	if err := os.MkdirAll(dirSam, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(compartilhado, filepath.Join(dirSam, "projects")); err != nil {
		t.Fatal(err)
	}
	escreveIdentidadeNoDir(t, dirSam, "sam.rivera@personal.example", "uuid-sam")

	s, err := Open(base, home)
	if err != nil {
		t.Fatal(err)
	}
	agora := time.Now()
	escreveMensagens(t, repo, "sess-da-jordan", []time.Time{agora.Add(-time.Hour)}, 9_000_000)
	_ = s.RecordAttrib(AttribEntry{Ts: agora.Add(-2 * time.Hour).Unix(), SessionID: "sess-da-jordan", AccountID: "jordan"})

	rep := s.UsageAll()
	var soma int64
	for _, u := range rep.Accounts {
		soma += u.Total.InputTokens
	}
	soma += rep.Unattributed.Total.InputTokens
	if soma != 9_000_000 {
		t.Errorf("sum = %d, wanted 9M: the blocked account's token evaporated", soma)
	}
	if rep.Unattributed.Total.InputTokens != 9_000_000 {
		t.Errorf("unattributed = %d, wanted the 9M from the blocked account",
			rep.Unattributed.Total.InputTokens)
	}
}
