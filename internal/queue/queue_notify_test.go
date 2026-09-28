package queue

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type notifyCounter struct {
	mu   sync.Mutex
	jobs []Job
}

func (n *notifyCounter) fn() func(*Job) {
	return func(j *Job) {
		n.mu.Lock()
		n.jobs = append(n.jobs, *j)
		n.mu.Unlock()
	}
}
func (n *notifyCounter) count() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.jobs)
}
func (n *notifyCounter) forID(id string) []Job {
	n.mu.Lock()
	defer n.mu.Unlock()
	var out []Job
	for _, j := range n.jobs {
		if j.ID == id {
			out = append(out, j)
		}
	}
	return out
}

func waitCount(t *testing.T, n *notifyCounter, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if n.count() >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("notify count never reached %d (got %d)", want, n.count())
}

func newQueueWorkers(t *testing.T, workers int) *Queue {
	t.Helper()
	q, err := NewQueue(Options{DataDir: t.TempDir(), Workers: workers, MaxKeep: 50})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { q.Shutdown(context.Background()) })
	return q
}

func TestNotifyOnSuccess(t *testing.T) {
	q := newTmpQueue(t)
	nc := &notifyCounter{}
	q.SetNotifier(nc.fn())
	q.Register(&fakeRunner{kind: "ok", mode: "ok", steps: 2})

	j, _ := q.Enqueue("ok", nil, "alice", "user")
	waitFor(t, q, j.ID, StatusDone)
	waitCount(t, nc, 1)
	if got := nc.forID(j.ID); len(got) != 1 || got[0].Status != StatusDone {
		t.Fatalf("want 1 Done notify, got %+v", got)
	}
}

func TestNotifyOnFail(t *testing.T) {
	q := newTmpQueue(t)
	nc := &notifyCounter{}
	q.SetNotifier(nc.fn())
	q.Register(&fakeRunner{kind: "boom", mode: "fail"})

	j, _ := q.Enqueue("boom", nil, "a", "user")
	waitFor(t, q, j.ID, StatusFailed)
	waitCount(t, nc, 1)
	if got := nc.forID(j.ID); len(got) != 1 || got[0].Status != StatusFailed {
		t.Fatalf("want 1 Failed notify, got %+v", got)
	}
}

func TestNotifyOnCancelRunning(t *testing.T) {
	q := newTmpQueue(t)
	nc := &notifyCounter{}
	q.SetNotifier(nc.fn())
	q.Register(&fakeRunner{kind: "block", mode: "block"})

	j, _ := q.Enqueue("block", nil, "a", "user")
	waitFor(t, q, j.ID, StatusRunning)
	if err := q.Cancel(j.ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, q, j.ID, StatusCancelled)
	waitCount(t, nc, 1)
	if got := nc.forID(j.ID); len(got) != 1 || got[0].Status != StatusCancelled {
		t.Fatalf("want 1 Cancelled notify, got %+v", got)
	}
}

func TestNotifyOnCancelQueued(t *testing.T) {
	q := newQueueWorkers(t, 1)
	nc := &notifyCounter{}
	q.SetNotifier(nc.fn())
	q.Register(&fakeRunner{kind: "block", mode: "block"})

	busy, _ := q.Enqueue("block", nil, "a", "user")
	waitFor(t, q, busy.ID, StatusRunning)

	queued, _ := q.Enqueue("block", nil, "a", "user")
	if jj, _ := q.Get(queued.ID); jj.Status != StatusQueued {
		t.Fatalf("second job should be queued, got %s", jj.Status)
	}
	if err := q.Cancel(queued.ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, q, queued.ID, StatusCancelled)
	if got := nc.forID(queued.ID); len(got) != 1 || got[0].Status != StatusCancelled {
		t.Fatalf("want 1 Cancelled notify for queued job, got %+v", got)
	}
	if got := nc.forID(busy.ID); len(got) != 0 {
		t.Fatalf("busy job should not have notified yet, got %+v", got)
	}
}

func TestNotifyUnknownKindResume(t *testing.T) {
	q := newTmpQueue(t)
	nc := &notifyCounter{}
	q.SetNotifier(nc.fn())

	const jid = "ghost"
	q.mu.Lock()
	q.jobs[jid] = &Job{ID: jid, Kind: "vanished_kind", Owner: "a",
		Status: StatusQueued, Queued: time.Now().Unix()}
	q.order = append(q.order, jid)
	q.mu.Unlock()

	q.runOne(jid)
	got := nc.forID(jid)
	if len(got) != 1 || got[0].Status != StatusFailed || !strings.Contains(got[0].Error, "unknown kind") {
		t.Fatalf("want 1 unknown-kind Failed notify, got %+v", got)
	}
}

func TestNotifyDetachedReapOnce(t *testing.T) {
	q := newTmpQueue(t)
	nc := &notifyCounter{}
	q.SetNotifier(nc.fn())

	const jid = "det1"
	if err := WriteDetachedStatus(q.dataDir, jid, DetachedStatus{
		Status: StatusDone, Progress: 100, Finished: time.Now().Unix(),
	}); err != nil {
		t.Fatal(err)
	}
	q.mu.Lock()
	q.jobs[jid] = &Job{ID: jid, Kind: "jira_ai_analysis", Owner: "a",
		Status: StatusRunning, Scope: "fake.scope", Started: time.Now().Unix() - 100}
	q.order = append(q.order, jid)
	q.mu.Unlock()

	q.reapDetachedOnce()
	waitCount(t, nc, 1)
	if got := nc.forID(jid); len(got) != 1 || got[0].Status != StatusDone {
		t.Fatalf("want 1 Done notify from reaper, got %+v", got)
	}

	q.reapDetachedOnce()
	time.Sleep(30 * time.Millisecond)
	if got := nc.forID(jid); len(got) != 1 {
		t.Fatalf("re-reap must not re-notify, got %d", len(got))
	}
}

func TestNotifyDetachedTerminalAfterRestart(t *testing.T) {
	dir := t.TempDir()
	q1, err := NewQueue(Options{DataDir: dir, Workers: 1, MaxKeep: 50})
	if err != nil {
		t.Fatal(err)
	}
	const jid = "doneDet"
	q1.mu.Lock()
	q1.jobs[jid] = &Job{ID: jid, Kind: "jira_ai_analysis", Owner: "a",
		Status: StatusDone, Scope: "fake.scope", Progress: 100, Finished: time.Now().Unix()}
	q1.order = append(q1.order, jid)
	q1.persistLocked()
	q1.mu.Unlock()
	q1.Shutdown(context.Background())

	q2, err := NewQueue(Options{DataDir: dir, Workers: 1, MaxKeep: 50})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { q2.Shutdown(context.Background()) })
	nc := &notifyCounter{}
	q2.SetNotifier(nc.fn())

	q2.reapDetachedOnce()
	time.Sleep(80 * time.Millisecond)
	if nc.count() != 0 {
		t.Fatalf("terminal-at-boot job re-notified after restart: %+v", nc.jobs)
	}
	if jj, err := q2.Get(jid); err != nil || jj.Status != StatusDone {
		t.Fatalf("job not preserved as Done: %v %+v", err, jj)
	}
}

func TestNotifyOnLogCreateFailure(t *testing.T) {
	q := newTmpQueue(t)
	nc := &notifyCounter{}
	q.SetNotifier(nc.fn())
	q.Register(&fakeRunner{kind: "ok", mode: "ok", steps: 1})

	runs := filepath.Join(q.dataDir, "runs")
	_ = os.RemoveAll(runs)
	if err := os.WriteFile(runs, []byte("not a dir"), 0o600); err != nil {
		t.Fatal(err)
	}

	j, _ := q.Enqueue("ok", nil, "a", "user")
	waitFor(t, q, j.ID, StatusFailed)
	if got := nc.forID(j.ID); len(got) != 1 || got[0].Status != StatusFailed {
		t.Fatalf("want 1 Failed notify from fail(), got %+v", got)
	}
}
