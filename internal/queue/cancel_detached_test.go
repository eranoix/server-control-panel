package queue

import (
	"context"
	"encoding/json"
	"testing"
)

func TestCancelDetachedJob(t *testing.T) {
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

	nc := &notifyCounter{}
	q.SetNotifier(nc.fn())

	j, err := q.Enqueue("jira_ai_analysis", json.RawMessage(`{"issue_key":"X-1","owner":"u"}`), "u", "user")
	if err != nil {
		t.Fatal(err)
	}
	if j.Status != StatusRunning || j.Scope == "" {
		t.Fatalf("precondition: status=%q scope=%q", j.Status, j.Scope)
	}

	if err := WriteDetachedStatus(q.dataDir, j.ID, DetachedStatus{
		Status: StatusRunning, Progress: 20, Step: "analyzing",
	}); err != nil {
		t.Fatal(err)
	}
	q.reapDetachedOnce()

	if err := q.Cancel(j.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := q.Get(j.ID)
	if got.Status != StatusCancelled {
		t.Fatalf("after Cancel: status = %q; want cancelled", got.Status)
	}
	if got.Finished == 0 {
		t.Error("a cancelled job should have Finished")
	}

	q.reapDetachedOnce()
	got, _ = q.Get(j.ID)
	if got.Status != StatusCancelled {
		t.Fatalf("the reaper resurrected the cancelled job: status = %q", got.Status)
	}

	waitCount(t, nc, 1)
}
