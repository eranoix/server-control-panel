package todos

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newTmpStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	return NewStore(dir)
}

func TestCreateListDelete(t *testing.T) {
	s := newTmpStore(t)

	got, err := s.Create(Todo{Title: "renew tls", Category: CatSSLRenewal})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if got.ID == "" {
		t.Error("ID not set")
	}
	if got.Status != StatusPending {
		t.Errorf("default status: got %q want pending", got.Status)
	}

	list, err := s.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 todo, got %d", len(list))
	}

	if err := s.Delete(got.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := s.Delete(got.ID); err != ErrNotFound {
		t.Errorf("double delete: got %v want ErrNotFound", err)
	}
}

func TestCreateRejectsEmptyTitle(t *testing.T) {
	s := newTmpStore(t)
	if _, err := s.Create(Todo{Title: "   "}); err == nil {
		t.Error("expected error for empty title")
	}
}

func TestRecurringRoll(t *testing.T) {
	s := newTmpStore(t)
	got, err := s.Create(Todo{Title: "weekly thing", Category: CatBackupCheck, IntervalDays: 7})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	originalDue := got.Due
	// MarkDone bumps Due by IntervalDays from time.Now() — sleep ≥1s to
	// guarantee the next second tick so the comparison is meaningful.
	time.Sleep(1100 * time.Millisecond)

	done, err := s.MarkDone(got.ID)
	if err != nil {
		t.Fatalf("mark done: %v", err)
	}
	if done.Status != StatusPending {
		t.Errorf("recurring should reset to pending; got %q", done.Status)
	}
	if done.Due <= originalDue {
		t.Errorf("recurring should bump due forward: original=%d new=%d", originalDue, done.Due)
	}
	if done.DoneCount != 1 {
		t.Errorf("done_count: got %d want 1", done.DoneCount)
	}
}

func TestOneShotMarksDone(t *testing.T) {
	s := newTmpStore(t)
	got, _ := s.Create(Todo{Title: "one shot"})
	done, err := s.MarkDone(got.ID)
	if err != nil {
		t.Fatalf("mark done: %v", err)
	}
	if done.Status != StatusDone {
		t.Errorf("one-shot should stay done; got %q", done.Status)
	}
}

func TestSnoozePastFails(t *testing.T) {
	s := newTmpStore(t)
	got, _ := s.Create(Todo{Title: "x"})
	_, err := s.Snooze(got.ID, time.Now().Add(-1*time.Hour).Unix())
	if err == nil {
		t.Error("snooze in past should fail")
	}
}

func TestBucketOf(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name string
		t    Todo
		want Bucket
	}{
		{"done", Todo{Status: StatusDone}, BucketDone},
		{"no due", Todo{Status: StatusPending, Due: 0}, BucketFuture},
		{"overdue", Todo{Status: StatusPending, Due: now.Add(-1 * time.Hour).Unix()}, BucketOverdue},
		{"week", Todo{Status: StatusPending, Due: now.Add(3 * 24 * time.Hour).Unix()}, BucketWeek},
		{"month", Todo{Status: StatusPending, Due: now.Add(20 * 24 * time.Hour).Unix()}, BucketMonth},
		{"future", Todo{Status: StatusPending, Due: now.Add(60 * 24 * time.Hour).Unix()}, BucketFuture},
		{"snoozed in future", Todo{Status: StatusSnoozed, SnoozeUntil: now.Add(1 * time.Hour).Unix(), Due: now.Add(-1 * time.Hour).Unix()}, BucketFuture},
	}
	for _, c := range cases {
		if got := BucketOf(c.t, now); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

func TestAtomicWriteLeavesBak(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	if _, err := s.Create(Todo{Title: "v1"}); err != nil {
		t.Fatalf("create v1: %v", err)
	}
	if _, err := s.Create(Todo{Title: "v2"}); err != nil {
		t.Fatalf("create v2: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "todos.json.bak")); err != nil {
		t.Errorf(".bak should exist after second write: %v", err)
	}
}
