package queue

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func seedState(t *testing.T, jobs []*Job) string {
	t.Helper()
	dir := t.TempDir()
	root := filepath.Join(dir, "queue")
	if err := os.MkdirAll(filepath.Join(root, "runs"), 0o700); err != nil {
		t.Fatal(err)
	}
	order := make([]string, len(jobs))
	for i, j := range jobs {
		order[i] = j.ID
	}
	stored := struct {
		Jobs  []*Job   `json:"jobs"`
		Order []string `json:"order"`
	}{Jobs: jobs, Order: order}
	data, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "state.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

type stubbornRunner struct{ kind string }

func (s stubbornRunner) Kind() string                        { return s.kind }
func (s stubbornRunner) AuthorizedFor(_ string, _ bool) bool { return true }
func (s stubbornRunner) Run(_ context.Context, _ json.RawMessage, w io.Writer, _ func(int), _ func(string)) error {
	io.WriteString(w, "stubborn start\n")
	time.Sleep(10 * time.Second)
	return nil
}

func TestReconcileSafeKindResumes(t *testing.T) {
	if !SafeToResume("apt_upgrade") {
		t.Fatal("apt_upgrade should be SafeToResume")
	}
	jobs := []*Job{{ID: "j_safe", Kind: "apt_upgrade", Status: StatusRunning, Started: 1, Progress: 42}}
	dir := seedState(t, jobs)
	q, err := NewQueue(Options{DataDir: dir, Workers: 2, MaxKeep: 50})
	if err != nil {
		t.Fatal(err)
	}
	defer q.Shutdown(context.Background())

	j, err := q.Get("j_safe")
	if err != nil {
		t.Fatal(err)
	}
	if j.Status == StatusInterrupted {
		t.Fatalf("safe kind became interrupted; want resumed")
	}
	if j.Error == "interrupted: server-control-panel restart" {
		t.Fatalf("safe kind got interrupt error; want resume branch")
	}
}

func TestReconcileJiraAIInterrupted(t *testing.T) {
	if SafeToResume("jira_ai_analysis") {
		t.Fatal("jira_ai_analysis must NOT be SafeToResume")
	}
	jobs := []*Job{{ID: "j_ai", Kind: "jira_ai_analysis", Status: StatusRunning, Started: 1}}
	dir := seedState(t, jobs)
	q, err := NewQueue(Options{DataDir: dir, Workers: 2, MaxKeep: 50})
	if err != nil {
		t.Fatal(err)
	}
	defer q.Shutdown(context.Background())

	j, err := q.Get("j_ai")
	if err != nil {
		t.Fatal(err)
	}
	if j.Status != StatusInterrupted {
		t.Fatalf("status = %q; want interrupted", j.Status)
	}
	if j.Finished == 0 {
		t.Fatalf("interrupted job must have Finished set")
	}
}

func TestReconcileShellInterrupted(t *testing.T) {
	jobs := []*Job{{ID: "j_sh", Kind: "shell", Status: StatusRunning, Started: 1}}
	dir := seedState(t, jobs)
	q, err := NewQueue(Options{DataDir: dir, Workers: 2, MaxKeep: 50})
	if err != nil {
		t.Fatal(err)
	}
	defer q.Shutdown(context.Background())

	j, _ := q.Get("j_sh")
	if j.Status != StatusInterrupted {
		t.Fatalf("status = %q; want interrupted", j.Status)
	}
}

func TestRerunInterrupted(t *testing.T) {
	jobs := []*Job{{ID: "j_int", Kind: "shell", Status: StatusInterrupted, Finished: 1, Error: "x"}}
	dir := seedState(t, jobs)
	q, err := NewQueue(Options{DataDir: dir, Workers: 1, MaxKeep: 50})
	if err != nil {
		t.Fatal(err)
	}
	q.Register(&fakeRunner{kind: "shell", mode: "ok", steps: 1})
	defer q.Shutdown(context.Background())

	before := time.Now().Unix()
	cp, err := q.Rerun("j_int")
	if err != nil {
		t.Fatalf("Rerun(interrupted) err = %v; want nil", err)
	}
	if cp == nil {
		t.Fatal("Rerun returned nil job")
	}

	if cp.Status == StatusInterrupted {
		t.Fatalf("reran job status = %q; the job did not leave the terminal state", cp.Status)
	}
	if cp.Queued < before {
		t.Fatalf("Queued = %d, earlier than %d — the job was not re-queued", cp.Queued, before)
	}
}

func TestDeleteInterrupted(t *testing.T) {
	jobs := []*Job{{ID: "j_int", Kind: "shell", Status: StatusInterrupted, Finished: 1}}
	dir := seedState(t, jobs)
	q, err := NewQueue(Options{DataDir: dir, Workers: 1, MaxKeep: 50})
	if err != nil {
		t.Fatal(err)
	}
	defer q.Shutdown(context.Background())

	if err := q.Delete("j_int"); err != nil {
		t.Fatalf("Delete(interrupted) err = %v; want nil", err)
	}
	if _, err := q.Get("j_int"); err == nil {
		t.Fatal("job still present after Delete")
	}
}

func TestShutdownCapsAndInterrupts(t *testing.T) {
	dir := t.TempDir()
	q, err := NewQueue(Options{DataDir: dir, Workers: 1, MaxKeep: 50})
	if err != nil {
		t.Fatal(err)
	}
	q.Register(stubbornRunner{kind: "stubborn"})
	j, err := q.Enqueue("stubborn", json.RawMessage(`{}`), "u", "user")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, q, j.ID, StatusRunning)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	q.Shutdown(ctx)
	elapsed := time.Since(start)
	if elapsed > 2*time.Second {
		t.Fatalf("Shutdown took %v; want ≤ cap", elapsed)
	}

	got, err := q.Get(j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusInterrupted {
		t.Fatalf("survivor status = %q; want interrupted", got.Status)
	}
	q2, err := NewQueue(Options{DataDir: dir, Workers: 1, MaxKeep: 50})
	if err != nil {
		t.Fatal(err)
	}
	defer q2.Shutdown(context.Background())
	j2, _ := q2.Get(j.ID)
	if j2.Status != StatusInterrupted {
		t.Fatalf("persisted status = %q; want interrupted", j2.Status)
	}
}

func TestCounts(t *testing.T) {
	dir := t.TempDir()
	q, err := NewQueue(Options{DataDir: dir, Workers: 1, MaxKeep: 50})
	if err != nil {
		t.Fatal(err)
	}
	q.Register(stubbornRunner{kind: "stubborn"})
	defer q.Shutdown(context.Background())

	j1, _ := q.Enqueue("stubborn", json.RawMessage(`{}`), "u", "user")
	waitFor(t, q, j1.ID, StatusRunning)
	j2, _ := q.Enqueue("stubborn", json.RawMessage(`{}`), "u", "user")
	_ = j2
	time.Sleep(50 * time.Millisecond)

	running, queued := q.Counts()
	if running != 1 {
		t.Fatalf("running = %d; want 1", running)
	}
	if queued != 1 {
		t.Fatalf("queued = %d; want 1", queued)
	}
}
