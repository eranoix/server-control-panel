package queue

import (
	"context"
	"encoding/json"
	"testing"
)

// Zombie at RUNTIME (not at boot): the job is already running detached when the
// external process dies abruptly (SIGKILL, OOM, an external systemctl stop),
// leaving behind a status file that says "running".
//
// The boot reconcile already covered this case; the reaper did not. It trusted
// the file, re-applied "running" forever and never reached the dead-scope
// branch — a job running for good, with no process behind it at all.
func TestReaperInterrompeDetachedComScopeMortoEmRuntime(t *testing.T) {
	dir := t.TempDir()
	q, err := NewQueue(Options{DataDir: dir, Workers: 1, MaxKeep: 50})
	if err != nil {
		t.Fatal(err)
	}
	defer q.Shutdown(context.Background())
	q.Register(&fakeRunner{kind: "jira_ai_analysis", mode: "ok", steps: 1})
	// The scope it hands back does not really exist -> scopeAlive() is false, which
	// is exactly the "the process died" state we want to simulate.
	q.SetDetach(func(id string) (string, error) {
		return "vpsm-job-inexistente-" + id + ".scope", nil
	}, "jira_ai_analysis")

	j, err := q.Enqueue("jira_ai_analysis", json.RawMessage(`{"issue_key":"X-1","owner":"u"}`), "u", "user")
	if err != nil {
		t.Fatal(err)
	}
	if j.Status != StatusRunning {
		t.Fatalf("precondition: status = %q", j.Status)
	}

	// The last status the process managed to write before dying.
	if err := WriteDetachedStatus(q.dataDir, j.ID, DetachedStatus{
		Status: StatusRunning, Progress: 20, Step: "analisando",
	}); err != nil {
		t.Fatal(err)
	}
	// Push the start time into the past to get out of the startup grace (which
	// exists so we don't raise a false positive while systemd-run is still
	// bringing the scope up).
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
