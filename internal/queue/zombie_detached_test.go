package queue

import (
	"context"
	"encoding/json"
	"testing"
)

func TestReaperInterruptsDetachedWithDeadScopeAtRuntime(t *testing.T) {
	dir := t.TempDir()
	q, err := NewQueue(Options{DataDir: dir, Workers: 1, MaxKeep: 50})
	if err != nil {
		t.Fatal(err)
	}
	defer q.Shutdown(context.Background())
	q.Register(&fakeRunner{kind: "jira_ai_analysis", mode: "ok", steps: 1})
	q.SetDetach(func(id string) (string, error) {
		return "panel-job-missing-" + id + ".scope", nil
	}, "jira_ai_analysis")

	j, err := q.Enqueue("jira_ai_analysis", json.RawMessage(`{"issue_key":"X-1","owner":"u"}`), "u", "user")
	if err != nil {
		t.Fatal(err)
	}
	if j.Status != StatusRunning {
		t.Fatalf("precondition: status = %q", j.Status)
	}

	if err := WriteDetachedStatus(q.dataDir, j.ID, DetachedStatus{
		Status: StatusRunning, Progress: 20, Step: "analyzing",
	}); err != nil {
		t.Fatal(err)
	}
	q.mu.Lock()
	q.jobs[j.ID].Started -= detachStartupGrace + 5
	q.mu.Unlock()

	q.reapDetachedOnce()

	got, _ := q.Get(j.ID)
	if got.Status == StatusRunning {
		t.Fatal("a job with a dead scope got stuck in running (stale file)")
	}
	if got.Status != StatusInterrupted {
		t.Fatalf("status = %q; want interrupted", got.Status)
	}
}
