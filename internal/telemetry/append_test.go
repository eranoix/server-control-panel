package telemetry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestSinkAppendAcrossHandles is the test that actually exercises O_APPEND.
//
// Why it exists: swapping `os.O_APPEND` for `os.O_TRUNC` does NOT fail the
// goroutine-concurrency test. An `*os.File` has a single offset, and the mutex
// already serializes the writes — O_APPEND is not what makes that test pass.
// Measured, not assumed.
//
// What O_APPEND buys is atomicity of (seek to end + write) across INDEPENDENT
// DESCRIPTORS — two processes, or the same process after a deploy with the old
// binary still alive, or a rotation that reopened the file. Without it, the
// second descriptor opens at offset 0 and overwrites what the first one wrote:
// the panel would report success and the file would hold fewer events.
//
// This test opens two Sinks over the same directory and alternates the writes.
// With O_APPEND: 2*n lines. Without it: far fewer.
func TestSinkAppendAcrossHandles(t *testing.T) {
	dir := t.TempDir()
	const n = 100

	a, err := NewSink(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := NewSink(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	tm, _ := time.Parse("2006-01-02", "2026-08-06")
	relogio := func() time.Time { return tm }
	a.now, b.now = relogio, relogio

	for i := 0; i < n; i++ {
		recA, _ := json.Marshal(map[string]any{"h": "a", "i": i})
		if err := a.Write(recA); err != nil {
			t.Fatalf("handle a, write %d: %v", i, err)
		}
		recB, _ := json.Marshal(map[string]any{"h": "b", "i": i})
		if err := b.Write(recB); err != nil {
			t.Fatalf("handle b, write %d: %v", i, err)
		}
	}

	ls, _ := linhas(t, filepath.Join(dir, "2026-08-06.jsonl"))
	if len(ls) != 2*n {
		t.Fatalf("O_APPEND is not holding independent descriptors: expected=%d observed=%d lines", 2*n, len(ls))
	}
	contaA, contaB := 0, 0
	for j, l := range ls {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("line %d corrupted by an overwrite: %q", j+1, l)
		}
		switch m["h"] {
		case "a":
			contaA++
		case "b":
			contaB++
		default:
			t.Fatalf("line %d with no recognizable handle: %q", j+1, l)
		}
	}
	if contaA != n || contaB != n {
		t.Fatalf("events lost: handle a=%d handle b=%d (expected %d each)", contaA, contaB, n)
	}
}

// TestSinkAppendAcrossHandlesConcurrent: the same invariant, now with both
// descriptors writing in parallel, so -race has something to look at.
func TestSinkAppendAcrossHandlesConcurrent(t *testing.T) {
	dir := t.TempDir()
	const n = 100
	tm, _ := time.Parse("2006-01-02", "2026-08-06")
	relogio := func() time.Time { return tm }

	sinks := make([]*Sink, 2)
	for i := range sinks {
		s, err := NewSink(dir)
		if err != nil {
			t.Fatal(err)
		}
		s.now = relogio
		defer s.Close()
		sinks[i] = s
	}

	var wg sync.WaitGroup
	for h, s := range sinks {
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(s *Sink, h, i int) {
				defer wg.Done()
				rec, _ := json.Marshal(map[string]any{"h": h, "i": i})
				if err := s.Write(rec); err != nil {
					t.Error(err)
				}
			}(s, h, i)
		}
	}
	wg.Wait()

	b, err := os.ReadFile(filepath.Join(dir, "2026-08-06.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	st, err := ReadDay(filepath.Join(dir, "2026-08-06.jsonl"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if st.Valid != 2*n || st.Invalid != 0 {
		t.Fatalf("expected={Valid:%d Invalid:0} observed=%+v (%d bytes)", 2*n, st, len(b))
	}
}
