package telemetry

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func lines(t *testing.T, path string) ([]string, string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}
	s := string(b)
	if s == "" {
		return nil, s
	}
	if !strings.HasSuffix(s, "\n") {
		t.Errorf("the file does not end in \\n — the one-line-per-event invariant is broken")
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n"), s
}

func freeze(s *Sink, day string) {
	tm, _ := time.Parse("2006-01-02", day)
	s.now = func() time.Time { return tm }
}

func TestSinkWriteThreeRecords(t *testing.T) {
	dir := t.TempDir()
	s, err := NewSink(dir)
	if err != nil {
		t.Fatalf("NewSink: %v", err)
	}
	defer s.Close()
	freeze(s, "2026-08-06")

	for i := 0; i < 3; i++ {
		rec, _ := json.Marshal(map[string]any{"n": i})
		if err := s.Write(rec); err != nil {
			t.Fatalf("Write %d: %v", i, err)
		}
	}
	p := filepath.Join(dir, "2026-08-06.jsonl")
	ls, raw := lines(t, p)
	if len(ls) != 3 {
		t.Fatalf("expected=3 observed=%d lines; content=%q", len(ls), raw)
	}
	for i, l := range ls {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Errorf("line %d is not valid JSON: %q", i+1, l)
		}
	}
}

func TestSinkRejectsTooLong(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewSink(dir)
	defer s.Close()
	freeze(s, "2026-08-06")

	rec := make([]byte, 4001)
	for i := range rec {
		rec[i] = 'x'
	}
	if err := s.Write(rec); !errors.Is(err, ErrTooLong) {
		t.Fatalf("expected=ErrTooLong observed=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "2026-08-06.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("a refused record created/touched the file: %v", err)
	}
	if err := s.Write(rec[:4000]); err != nil {
		t.Fatalf("4000 bytes should pass, observed=%v", err)
	}
}

func TestSinkRejectsEmbeddedNewline(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewSink(dir)
	defer s.Close()
	freeze(s, "2026-08-06")

	if err := s.Write([]byte("{\"a\":1}\n{\"b\":2}")); !errors.Is(err, ErrEmbedded) {
		t.Fatalf("expected=ErrEmbedded observed=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "2026-08-06.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("a record with an embedded \\n created the file: %v", err)
	}
	if err := s.Write([]byte("{\"a\":1}\n")); !errors.Is(err, ErrEmbedded) {
		t.Fatalf("trailing \\n: expected=ErrEmbedded observed=%v", err)
	}
}

func TestSinkRejectsEmpty(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewSink(dir)
	defer s.Close()
	freeze(s, "2026-08-06")

	if err := s.Write(nil); !errors.Is(err, ErrEmpty) {
		t.Fatalf("expected=ErrEmpty observed=%v", err)
	}
	if err := s.Write([]byte{}); !errors.Is(err, ErrEmpty) {
		t.Fatalf("expected=ErrEmpty observed=%v", err)
	}
}

func TestSinkDayRollover(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewSink(dir)
	defer s.Close()

	freeze(s, "2026-08-06")
	if err := s.Write([]byte("{\"d\":1}")); err != nil {
		t.Fatal(err)
	}
	if err := s.Write([]byte("{\"d\":2}")); err != nil {
		t.Fatal(err)
	}
	freeze(s, "2026-08-07")
	if err := s.Write([]byte("{\"d\":3}")); err != nil {
		t.Fatal(err)
	}

	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 2 {
		names := make([]string, len(ents))
		for i, e := range ents {
			names[i] = e.Name()
		}
		t.Fatalf("expected=2 files observed=%d (%v)", len(ents), names)
	}
	a, _ := lines(t, filepath.Join(dir, "2026-08-06.jsonl"))
	b, _ := lines(t, filepath.Join(dir, "2026-08-07.jsonl"))
	if len(a) != 2 || len(b) != 1 {
		t.Fatalf("wrong distribution: day1=%d day2=%d", len(a), len(b))
	}
	if len(a)+len(b) != 3 {
		t.Fatalf("event lost at the rollover: expected=3 observed=%d", len(a)+len(b))
	}
}

func TestSinkFileMode(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewSink(dir)
	defer s.Close()
	freeze(s, "2026-08-06")
	if err := s.Write([]byte("{\"a\":1}")); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(dir, "2026-08-06.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0640 {
		t.Fatalf("expected=0640 observed=%04o", got)
	}
}

func TestSinkConcurrentWrites(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewSink(dir)
	defer s.Close()
	freeze(s, "2026-08-06")

	const n = 200
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rec, _ := json.Marshal(map[string]any{
				"i":   i,
				"pad": strings.Repeat("p", 200),
			})
			errs[i] = s.Write(rec)
		}(i)
	}
	wg.Wait()
	for i, e := range errs {
		if e != nil {
			t.Fatalf("Write %d: %v", i, e)
		}
	}

	ls, _ := lines(t, filepath.Join(dir, "2026-08-06.jsonl"))
	if len(ls) != n {
		t.Fatalf("expected=%d observed=%d lines", n, len(ls))
	}
	seen := make(map[float64]bool, n)
	for j, l := range ls {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("line %d is not valid JSON (interleaving): %q", j+1, l)
		}
		idx, ok := m["i"].(float64)
		if !ok {
			t.Fatalf("line %d with no i field: %q", j+1, l)
		}
		if seen[idx] {
			t.Errorf("record %v duplicated", idx)
		}
		seen[idx] = true
	}
	if len(seen) != n {
		t.Fatalf("distinct records: expected=%d observed=%d", n, len(seen))
	}
}

func TestReadDayToleratesTruncatedLine(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "2026-08-06.jsonl")
	content := "{\"screen\":\"dev.code\"}\n" +
		"{\"screen\":\"docker.containers.logs\"}\n" +
		"{\"screen\":\"dashboard\"}\n" +
		"{\"screen\":\"operations.gi"
	if err := os.WriteFile(p, []byte(content), 0640); err != nil {
		t.Fatal(err)
	}

	var readLines []string
	st, err := ReadDay(p, func(m map[string]any) {
		if s, ok := m["screen"].(string); ok {
			readLines = append(readLines, s)
		}
	})
	if err != nil {
		t.Fatalf("ReadDay returned an error for a partial line (it should tolerate it): %v", err)
	}
	if st.Valid != 3 {
		t.Errorf("Valid: expected=3 observed=%d", st.Valid)
	}
	if st.Invalid != 1 {
		t.Errorf("Invalid: expected=1 observed=%d — a partial line ignored in silence is a lie", st.Invalid)
	}
	if len(readLines) != 3 {
		t.Errorf("callback called %d times, expected=3 (%v)", len(readLines), readLines)
	}
}

func TestReadDayMissingFile(t *testing.T) {
	st, err := ReadDay(filepath.Join(t.TempDir(), "missing.jsonl"), nil)
	if err == nil {
		t.Fatalf("a missing file should return an error; observed=%+v", st)
	}
}

func TestSinkRoundTripWithReadDay(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewSink(dir)
	freeze(s, "2026-08-06")
	for i := 0; i < 5; i++ {
		rec, _ := json.Marshal(map[string]any{"screen": "dev.code", "i": i})
		if err := s.Write(rec); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	st, err := ReadDay(filepath.Join(dir, "2026-08-06.jsonl"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if st.Valid != 5 || st.Invalid != 0 {
		t.Fatalf("expected={5 0} observed=%+v", st)
	}
}

func TestSinkCloseIdempotent(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewSink(dir)
	if err := s.Close(); err != nil {
		t.Fatalf("Close with no write: %v", err)
	}
	freeze(s, "2026-08-06")
	if err := s.Write([]byte("{\"a\":1}")); err != nil {
		t.Fatalf("a Write after Close must reopen: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("a double Close should be a no-op: %v", err)
	}
}

func TestNewSinkCreatesDir(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "data", "telemetry")
	s, err := NewSink(dir)
	if err != nil {
		t.Fatalf("NewSink in a nonexistent directory: %v", err)
	}
	defer s.Close()
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("the directory was not created: %v", err)
	}
	if got := fi.Mode().Perm(); got != 0750 {
		t.Errorf("directory mode: expected=0750 observed=%04o", got)
	}
}

func TestSinkWriteDoesNotMutateCaller(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewSink(dir)
	defer s.Close()
	freeze(s, "2026-08-06")

	buf := make([]byte, 0, 64)
	buf = append(buf, []byte("{\"a\":1}")...)
	sentinel := buf[:cap(buf)][len(buf)]
	if err := s.Write(buf); err != nil {
		t.Fatal(err)
	}
	if got := buf[:cap(buf)][len(buf)]; got != sentinel {
		t.Fatalf("Write mutated the caller's buffer: expected=%d observed=%d", sentinel, got)
	}
}
