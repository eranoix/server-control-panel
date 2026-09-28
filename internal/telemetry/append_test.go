package telemetry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

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
	clock := func() time.Time { return tm }
	a.now, b.now = clock, clock

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

	ls, _ := lines(t, filepath.Join(dir, "2026-08-06.jsonl"))
	if len(ls) != 2*n {
		t.Fatalf("O_APPEND is not holding independent descriptors: expected=%d observed=%d lines", 2*n, len(ls))
	}
	countA, countB := 0, 0
	for j, l := range ls {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("line %d corrupted by an overwrite: %q", j+1, l)
		}
		switch m["h"] {
		case "a":
			countA++
		case "b":
			countB++
		default:
			t.Fatalf("line %d with no recognizable handle: %q", j+1, l)
		}
	}
	if countA != n || countB != n {
		t.Fatalf("events lost: handle a=%d handle b=%d (expected %d each)", countA, countB, n)
	}
}

func TestSinkAppendAcrossHandlesConcurrent(t *testing.T) {
	dir := t.TempDir()
	const n = 100
	tm, _ := time.Parse("2006-01-02", "2026-08-06")
	clock := func() time.Time { return tm }

	sinks := make([]*Sink, 2)
	for i := range sinks {
		s, err := NewSink(dir)
		if err != nil {
			t.Fatal(err)
		}
		s.now = clock
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
