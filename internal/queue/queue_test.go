package queue

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// fakeRunner just emits N progress steps then either succeeds, fails, or
// blocks until ctx is cancelled.
type fakeRunner struct {
	kind     string
	mode     string // "ok" | "fail" | "block"
	steps    int
	primary  bool // true → primary-only
	called   int
	mu       sync.Mutex
	authzReq bool
}

func (f *fakeRunner) Kind() string { return f.kind }
func (f *fakeRunner) AuthorizedFor(_ string, isPrimary bool) bool {
	if f.primary {
		return isPrimary
	}
	return true
}
func (f *fakeRunner) Run(ctx context.Context, _ json.RawMessage, w io.Writer, progress func(int), step func(string)) error {
	f.mu.Lock()
	f.called++
	f.mu.Unlock()
	io.WriteString(w, "starting "+f.kind+"\n")
	switch f.mode {
	case "fail":
		return errors.New("boom")
	case "block":
		<-ctx.Done()
		return ctx.Err()
	}
	for i := 1; i <= f.steps; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
			progress(i * 100 / f.steps)
			io.WriteString(w, "step\n")
		}
	}
	return nil
}

func newTmpQueue(t *testing.T) *Queue {
	t.Helper()
	q, err := NewQueue(Options{DataDir: t.TempDir(), Workers: 2, MaxKeep: 50})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { q.Shutdown(context.Background()) })
	return q
}

func waitFor(t *testing.T, q *Queue, id string, target Status) *Job {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		j, err := q.Get(id)
		if err == nil && j.Status == target {
			return j
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("job %s never reached %s", id, target)
	return nil
}

func TestEnqueueAndRun(t *testing.T) {
	q := newTmpQueue(t)
	q.Register(&fakeRunner{kind: "test_ok", mode: "ok", steps: 3})

	j, err := q.Enqueue("test_ok", nil, "alice", "user")
	if err != nil {
		t.Fatal(err)
	}
	// ACCEPTED, and not "still in the queue".
	//
	// Enqueue calls dispatch BEFORE taking the snapshot it returns (see
	// queue.go): when the machine is busy, the worker has already picked the job
	// up and the snapshot comes back as `running`. Demanding `queued` here was
	// demanding of the API a promise it never made — and the test only failed
	// under load, which is exactly when nobody wants to be chasing a flaky test.
	//
	// What actually matters to prove is that the job was ACCEPTED: neither
	// refused nor already failed. The final outcome stays covered by the waitFor
	// just below.
	if j.Status != StatusQueued && j.Status != StatusRunning {
		t.Errorf("initial status: got %s want queued or running", j.Status)
	}

	final := waitFor(t, q, j.ID, StatusDone)
	if final.Progress != 100 {
		t.Errorf("final progress: got %d want 100", final.Progress)
	}
	log, err := q.ReadLog(j.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(log) == 0 {
		t.Error("log empty")
	}
}

func TestUnknownKind(t *testing.T) {
	q := newTmpQueue(t)
	if _, err := q.Enqueue("nope", nil, "a", "user"); err != ErrUnknownKind {
		t.Errorf("got %v want ErrUnknownKind", err)
	}
}

func TestFailRecorded(t *testing.T) {
	q := newTmpQueue(t)
	q.Register(&fakeRunner{kind: "boom", mode: "fail"})
	j, _ := q.Enqueue("boom", nil, "a", "user")
	final := waitFor(t, q, j.ID, StatusFailed)
	if final.Error == "" {
		t.Error("Error should be set on failure")
	}
}

func TestCancelBlocking(t *testing.T) {
	q := newTmpQueue(t)
	q.Register(&fakeRunner{kind: "block", mode: "block"})
	j, _ := q.Enqueue("block", nil, "a", "user")
	time.Sleep(30 * time.Millisecond) // let it transition to running
	if err := q.Cancel(j.ID); err != nil {
		t.Fatal(err)
	}
	final := waitFor(t, q, j.ID, StatusCancelled)
	if final.Status != StatusCancelled {
		t.Errorf("status: got %s", final.Status)
	}
}

func TestSubscribeReceivesProgress(t *testing.T) {
	q := newTmpQueue(t)
	q.Register(&fakeRunner{kind: "progress", mode: "ok", steps: 5})
	j, _ := q.Enqueue("progress", nil, "a", "user")
	ch, cancel := q.Subscribe(j.ID)
	defer cancel()

	sawProgress := false
	sawDone := false
	timeout := time.After(2 * time.Second)
loop:
	for {
		select {
		case ev := <-ch:
			if ev.Type == "progress" {
				sawProgress = true
			}
			if ev.Type == "status" && ev.Status == StatusDone {
				sawDone = true
				break loop
			}
		case <-timeout:
			t.Fatal("timeout waiting for events")
		}
	}
	if !sawProgress {
		t.Error("expected at least one progress event")
	}
	if !sawDone {
		t.Error("expected a done event")
	}
}

func TestPersistAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	q, err := NewQueue(Options{DataDir: dir, Workers: 1, MaxKeep: 50})
	if err != nil {
		t.Fatal(err)
	}
	q.Register(&fakeRunner{kind: "x", mode: "ok", steps: 2})
	j, _ := q.Enqueue("x", nil, "a", "user")
	_ = waitFor(t, q, j.ID, StatusDone)
	q.Shutdown(context.Background())

	// re-open; the finished job should still be there
	q2, err := NewQueue(Options{DataDir: dir, Workers: 1, MaxKeep: 50})
	if err != nil {
		t.Fatal(err)
	}
	q2.Register(&fakeRunner{kind: "x", mode: "ok", steps: 2})
	defer q2.Shutdown(context.Background())
	got, err := q2.Get(j.ID)
	if err != nil {
		t.Fatalf("get after restart: %v", err)
	}
	if got.Status != StatusDone {
		t.Errorf("status after restart: got %s want done", got.Status)
	}
}

func TestRunningInterruptedOnReload(t *testing.T) {
	dir := t.TempDir()
	// Hand-craft state.json with a 'running' job — simulates a crash. Kind
	// "x" is unknown → not SafeToResume → it becomes the HONEST terminal
	// "interrupted" (not the old red "failed"), recoverable via Rerun.
	state := `{"jobs":[{"id":"j_x","kind":"x","status":"running","queued":1,"log_path":""}],"order":["j_x"]}`
	if err := os.MkdirAll(filepath.Join(dir, "queue", "runs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "queue", "state.json"), []byte(state), 0o600); err != nil {
		t.Fatal(err)
	}
	q, err := NewQueue(Options{DataDir: dir, Workers: 1, MaxKeep: 50})
	if err != nil {
		t.Fatal(err)
	}
	defer q.Shutdown(context.Background())
	got, err := q.Get("j_x")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusInterrupted {
		t.Errorf("interrupted job: got %s want interrupted", got.Status)
	}
}

func TestDelete(t *testing.T) {
	q := newTmpQueue(t)
	q.Register(&fakeRunner{kind: "test_ok", mode: "ok", steps: 1})

	// Finished job: deletable, and its log file is removed from disk.
	j, _ := q.Enqueue("test_ok", nil, "alice", "user")
	final := waitFor(t, q, j.ID, StatusDone)
	if final.LogPath == "" {
		t.Fatal("expected a log path on finished job")
	}
	if _, err := os.Stat(final.LogPath); err != nil {
		t.Fatalf("log file should exist before delete: %v", err)
	}
	if err := q.Delete(j.ID); err != nil {
		t.Fatalf("delete finished job: %v", err)
	}
	if _, err := q.Get(j.ID); err != ErrNotFound {
		t.Errorf("after delete Get: got %v want ErrNotFound", err)
	}
	if _, err := os.Stat(final.LogPath); !os.IsNotExist(err) {
		t.Errorf("log file should be gone after delete: %v", err)
	}
	for _, id := range q.order {
		if id == j.ID {
			t.Error("deleted id still present in q.order")
		}
	}

	// Unknown id → ErrNotFound.
	if err := q.Delete("nope"); err != ErrNotFound {
		t.Errorf("delete unknown: got %v want ErrNotFound", err)
	}

	// Active (running) job → ErrConflict; must cancel first.
	q.Register(&fakeRunner{kind: "block", mode: "block"})
	bj, _ := q.Enqueue("block", nil, "alice", "user")
	waitFor(t, q, bj.ID, StatusRunning)
	if err := q.Delete(bj.ID); err != ErrConflict {
		t.Errorf("delete running job: got %v want ErrConflict", err)
	}
	if err := q.Cancel(bj.ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, q, bj.ID, StatusCancelled)
	if err := q.Delete(bj.ID); err != nil {
		t.Errorf("delete cancelled job: got %v want nil", err)
	}
}

func TestRerun(t *testing.T) {
	q := newTmpQueue(t)
	q.Register(&fakeRunner{kind: "test_ok", mode: "ok", steps: 1})

	j, _ := q.Enqueue("test_ok", nil, "alice", "scheduler:s_1")
	waitFor(t, q, j.ID, StatusDone)

	// Rerun keeps the SAME id and source, and resets run state.
	nj, err := q.Rerun(j.ID)
	if err != nil {
		t.Fatalf("rerun: %v", err)
	}
	if nj.ID != j.ID {
		t.Errorf("rerun changed id: got %s want %s", nj.ID, j.ID)
	}
	if nj.Source != "scheduler:s_1" {
		t.Errorf("rerun changed source: got %q want scheduler:s_1", nj.Source)
	}
	if nj.Finished != 0 || nj.Error != "" {
		t.Errorf("rerun did not reset run state: finished=%d err=%q", nj.Finished, nj.Error)
	}

	// It actually runs again and reaches done.
	final := waitFor(t, q, j.ID, StatusDone)
	if final.Progress != 100 {
		t.Errorf("rerun final progress: got %d want 100", final.Progress)
	}

	// Unknown id → ErrNotFound.
	if _, err := q.Rerun("nope"); err != ErrNotFound {
		t.Errorf("rerun unknown: got %v want ErrNotFound", err)
	}

	// Active job → ErrConflict.
	q.Register(&fakeRunner{kind: "block", mode: "block"})
	bj, _ := q.Enqueue("block", nil, "alice", "user")
	waitFor(t, q, bj.ID, StatusRunning)
	if _, err := q.Rerun(bj.ID); err != ErrConflict {
		t.Errorf("rerun running job: got %v want ErrConflict", err)
	}
}
