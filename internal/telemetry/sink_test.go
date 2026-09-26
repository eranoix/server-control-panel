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

// linhas returns the non-empty lines of a file, plus the raw content.
func linhas(t *testing.T, path string) ([]string, string) {
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

func congelar(s *Sink, dia string) {
	tm, _ := time.Parse("2006-01-02", dia)
	s.now = func() time.Time { return tm }
}

func TestSinkWriteThreeRecords(t *testing.T) {
	dir := t.TempDir()
	s, err := NewSink(dir)
	if err != nil {
		t.Fatalf("NewSink: %v", err)
	}
	defer s.Close()
	congelar(s, "2026-08-06")

	for i := 0; i < 3; i++ {
		rec, _ := json.Marshal(map[string]any{"n": i})
		if err := s.Write(rec); err != nil {
			t.Fatalf("Write %d: %v", i, err)
		}
	}
	p := filepath.Join(dir, "2026-08-06.jsonl")
	ls, raw := linhas(t, p)
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
	congelar(s, "2026-08-06")

	rec := make([]byte, 4001)
	for i := range rec {
		rec[i] = 'x'
	}
	if err := s.Write(rec); !errors.Is(err, ErrTooLong) {
		t.Fatalf("expected=ErrTooLong observed=%v", err)
	}
	// Nothing may have been written — not even the file may exist.
	if _, err := os.Stat(filepath.Join(dir, "2026-08-06.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("a refused record created/touched the file: %v", err)
	}
	// The limit itself: exactly 4000 passes.
	if err := s.Write(rec[:4000]); err != nil {
		t.Fatalf("4000 bytes should pass, observed=%v", err)
	}
}

func TestSinkRejectsEmbeddedNewline(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewSink(dir)
	defer s.Close()
	congelar(s, "2026-08-06")

	if err := s.Write([]byte("{\"a\":1}\n{\"b\":2}")); !errors.Is(err, ErrEmbedded) {
		t.Fatalf("expected=ErrEmbedded observed=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "2026-08-06.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("a record with an embedded \\n created the file: %v", err)
	}
	// A trailing \n is an embedded \n too: Write receives the record WITHOUT a break.
	if err := s.Write([]byte("{\"a\":1}\n")); !errors.Is(err, ErrEmbedded) {
		t.Fatalf("trailing \\n: expected=ErrEmbedded observed=%v", err)
	}
}

func TestSinkRejectsEmpty(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewSink(dir)
	defer s.Close()
	congelar(s, "2026-08-06")

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

	congelar(s, "2026-08-06")
	if err := s.Write([]byte("{\"d\":1}")); err != nil {
		t.Fatal(err)
	}
	if err := s.Write([]byte("{\"d\":2}")); err != nil {
		t.Fatal(err)
	}
	congelar(s, "2026-08-07")
	if err := s.Write([]byte("{\"d\":3}")); err != nil {
		t.Fatal(err)
	}

	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 2 {
		nomes := make([]string, len(ents))
		for i, e := range ents {
			nomes[i] = e.Name()
		}
		t.Fatalf("expected=2 files observed=%d (%v)", len(ents), nomes)
	}
	a, _ := linhas(t, filepath.Join(dir, "2026-08-06.jsonl"))
	b, _ := linhas(t, filepath.Join(dir, "2026-08-07.jsonl"))
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
	congelar(s, "2026-08-06")
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
	congelar(s, "2026-08-06")

	const n = 200
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rec, _ := json.Marshal(map[string]any{
				"i":   i,
				"pad": strings.Repeat("p", 200), // a fat record raises the chance of interleaving
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

	ls, _ := linhas(t, filepath.Join(dir, "2026-08-06.jsonl"))
	if len(ls) != n {
		t.Fatalf("expected=%d observed=%d lines", n, len(ls))
	}
	vistos := make(map[float64]bool, n)
	for j, l := range ls {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("line %d is not valid JSON (interleaving): %q", j+1, l)
		}
		idx, ok := m["i"].(float64)
		if !ok {
			t.Fatalf("line %d with no i field: %q", j+1, l)
		}
		if vistos[idx] {
			t.Errorf("record %v duplicated", idx)
		}
		vistos[idx] = true
	}
	if len(vistos) != n {
		t.Fatalf("distinct records: expected=%d observed=%d", n, len(vistos))
	}
}

func TestReadDayToleraLinhaTruncada(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "2026-08-06.jsonl")
	conteudo := "{\"screen\":\"dev.codigo\"}\n" +
		"{\"screen\":\"docker.containers.logs\"}\n" +
		"{\"screen\":\"dashboard\"}\n" +
		"{\"screen\":\"operacoes.gi" // crash mid-write: partial line, no \n
	if err := os.WriteFile(p, []byte(conteudo), 0640); err != nil {
		t.Fatal(err)
	}

	var lidos []string
	st, err := ReadDay(p, func(m map[string]any) {
		if s, ok := m["screen"].(string); ok {
			lidos = append(lidos, s)
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
	if len(lidos) != 3 {
		t.Errorf("callback called %d times, expected=3 (%v)", len(lidos), lidos)
	}
}

func TestReadDayArquivoAusente(t *testing.T) {
	st, err := ReadDay(filepath.Join(t.TempDir(), "nao-existe.jsonl"), nil)
	if err == nil {
		t.Fatalf("a missing file should return an error; observed=%+v", st)
	}
}

func TestSinkRoundTripComReadDay(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewSink(dir)
	congelar(s, "2026-08-06")
	for i := 0; i < 5; i++ {
		rec, _ := json.Marshal(map[string]any{"screen": "dev.codigo", "i": i})
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

func TestSinkCloseIdempotente(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewSink(dir)
	if err := s.Close(); err != nil {
		t.Fatalf("Close with no write: %v", err)
	}
	congelar(s, "2026-08-06")
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

func TestNewSinkCriaDiretorio(t *testing.T) {
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

// TestSinkWriteNaoMutaOChamador: Write must not write into the caller's array.
// `append(rec,'\n')` with spare capacity does exactly that — and the handler's
// json.Marshal returns a slice with spare capacity.
func TestSinkWriteNaoMutaOChamador(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewSink(dir)
	defer s.Close()
	congelar(s, "2026-08-06")

	buf := make([]byte, 0, 64)
	buf = append(buf, []byte("{\"a\":1}")...)
	sentinela := buf[:cap(buf)][len(buf)] // the byte right after the record
	if err := s.Write(buf); err != nil {
		t.Fatal(err)
	}
	if got := buf[:cap(buf)][len(buf)]; got != sentinela {
		t.Fatalf("Write mutated the caller's buffer: expected=%d observed=%d", sentinela, got)
	}
}
