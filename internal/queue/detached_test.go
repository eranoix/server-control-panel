package queue

import (
	"context"
	"encoding/json"
	"testing"
)

func TestDetachedEnqueueAndReap(t *testing.T) {
	dir := t.TempDir()
	q, err := NewQueue(Options{DataDir: dir, Workers: 1, MaxKeep: 50})
	if err != nil {
		t.Fatal(err)
	}
	defer q.Shutdown(context.Background())
	q.Register(&fakeRunner{kind: "jira_ai_analysis", mode: "ok", steps: 1})

	q.SetDetach(func(id string) (string, error) {
		return "panel-job-" + id + ".scope", nil
	}, "jira_ai_analysis")

	j, err := q.Enqueue("jira_ai_analysis", json.RawMessage(`{"issue_key":"X-1","owner":"u"}`), "u", "user")
	if err != nil {
		t.Fatal(err)
	}
	if j.Status != StatusRunning {
		t.Fatalf("status = %q; want running", j.Status)
	}
	if j.Scope == "" {
		t.Fatal("detached job must have a Scope")
	}

	if err := WriteDetachedStatus(q.dataDir, j.ID, DetachedStatus{Status: StatusRunning, Progress: 50, Step: "analyzing"}); err != nil {
		t.Fatal(err)
	}
	q.reapDetachedOnce()
	got, _ := q.Get(j.ID)
	if got.Progress != 50 || got.Step != "analyzing" || got.Status != StatusRunning {
		t.Fatalf("after progress: status=%q progress=%d step=%q", got.Status, got.Progress, got.Step)
	}

	if err := WriteDetachedStatus(q.dataDir, j.ID, DetachedStatus{Status: StatusDone, Progress: 100, Finished: 123}); err != nil {
		t.Fatal(err)
	}
	q.reapDetachedOnce()
	got, _ = q.Get(j.ID)
	if got.Status != StatusDone {
		t.Fatalf("after done: status = %q; want done", got.Status)
	}
	if got.Finished != 123 {
		t.Fatalf("finished = %d; want 123", got.Finished)
	}
}

func TestReconcileDetachedAdoptsTerminal(t *testing.T) {
	jobs := []*Job{{ID: "j_d", Kind: "jira_ai_analysis", Status: StatusRunning, Started: 1, Scope: "panel-job-j_d.scope"}}
	dir := seedState(t, jobs)
	queueRoot := dir + "/queue"
	if err := WriteDetachedStatus(queueRoot, "j_d", DetachedStatus{Status: StatusDone, Progress: 100, Finished: 999}); err != nil {
		t.Fatal(err)
	}
	q, err := NewQueue(Options{DataDir: dir, Workers: 1, MaxKeep: 50})
	if err != nil {
		t.Fatal(err)
	}
	defer q.Shutdown(context.Background())

	got, _ := q.Get("j_d")
	if got.Status != StatusDone {
		t.Fatalf("status = %q; want done (adopted from detached file)", got.Status)
	}
	if got.Finished != 999 {
		t.Fatalf("finished = %d; want 999", got.Finished)
	}
}

func TestReconcileDetachedInterruptedWhenNoFile(t *testing.T) {
	jobs := []*Job{{ID: "j_c", Kind: "jira_ai_analysis", Status: StatusRunning, Started: 1, Scope: "panel-job-j_c.scope"}}
	dir := seedState(t, jobs)
	q, err := NewQueue(Options{DataDir: dir, Workers: 1, MaxKeep: 50})
	if err != nil {
		t.Fatal(err)
	}
	defer q.Shutdown(context.Background())

	got, _ := q.Get("j_c")
	if got.Status != StatusInterrupted {
		t.Fatalf("status = %q; want interrupted", got.Status)
	}
}
