package claudeacct

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestUsageAggregation(t *testing.T) {
	dir := t.TempDir()
	// Isolate sam's dir into the tempdir so the test never reads the real
	// host transcripts (/srv/agent-accounts/sam/projects).
	t.Setenv("VPSM_CLAUDE_ACCOUNTS_DIR", filepath.Join(dir, "accounts"))
	home := filepath.Join(dir, "claude-home")
	proj := filepath.Join(home, "projects", "-some-repo")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	tsToday := now.Format(time.RFC3339)
	tsOld := now.AddDate(0, 0, -30).Format(time.RFC3339)

	// Two assistant usage lines (today + 30d ago) + one non-usage line that
	// must be ignored. Opus pricing: in $5/Mtok, out $25/Mtok.
	lines := `{"type":"assistant","timestamp":"` + tsToday + `","message":{"model":"claude-opus-4-8","usage":{"input_tokens":1000000,"output_tokens":1000000,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}
{"type":"user","timestamp":"` + tsToday + `","message":{"role":"user","content":"hi"}}
{"type":"assistant","timestamp":"` + tsOld + `","message":{"model":"claude-opus-4-8","usage":{"input_tokens":2000000,"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}
`
	if err := os.WriteFile(filepath.Join(proj, "sess.jsonl"), []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := Open(dir, home)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	jordan, _ := s.AccountByID("jordan")
	u := s.Usage(jordan)

	if u.Sessions != 1 {
		t.Errorf("sessions = %d, want 1", u.Sessions)
	}
	if msgs := u.Total.Messages; msgs != 2 {
		t.Errorf("total messages = %d, want 2", msgs)
	}
	// Total input = 1M + 2M = 3M; output = 1M.
	if u.Total.InputTokens != 3_000_000 || u.Total.OutputTokens != 1_000_000 {
		t.Errorf("total tokens in/out = %d/%d, want 3M/1M", u.Total.InputTokens, u.Total.OutputTokens)
	}
	// Today excludes the 30d-old line: input 1M, output 1M.
	if u.Today.InputTokens != 1_000_000 || u.Today.OutputTokens != 1_000_000 {
		t.Errorf("today tokens in/out = %d/%d, want 1M/1M", u.Today.InputTokens, u.Today.OutputTokens)
	}
	// Today cost = 1M*$5/M (in) + 1M*$25/M (out) = $30.
	if got := u.Today.CostUSD; got < 29.99 || got > 30.01 {
		t.Errorf("today cost = %.4f, want ~30.00", got)
	}
	if len(u.ByModel) != 1 || u.ByModel[0].Model != "claude-opus-4-8" {
		t.Errorf("by_model = %+v, want single opus entry", u.ByModel)
	}
	if u.LastActivity == 0 {
		t.Error("last_activity not set")
	}

	// sam (no transcripts here) → empty rollup, no panic.
	sam, _ := s.AccountByID("sam")
	if a := s.Usage(sam); a.Sessions != 0 || a.Total.Messages != 0 {
		t.Errorf("sam usage should be empty, got %+v", a.Total)
	}
}
