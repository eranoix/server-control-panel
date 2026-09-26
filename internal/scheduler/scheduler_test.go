package scheduler

import (
	"encoding/json"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

type fakeEnq struct {
	mu     sync.Mutex
	calls  []call
	failOn string // kind that should fail
}

type call struct {
	kind   string
	owner  string
	source string
}

func (f *fakeEnq) Enqueue(kind string, _ json.RawMessage, owner, source string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call{kind, owner, source})
	if kind == f.failOn {
		return "", errBoom
	}
	return "q_" + strconv.Itoa(len(f.calls)), nil
}

var errBoom = &fakeErr{"boom"}

type fakeErr struct{ s string }

func (e *fakeErr) Error() string { return e.s }

func newTmp(t *testing.T, enq Enqueuer) *Scheduler {
	t.Helper()
	s, err := New(filepath.Join(t.TempDir(), "sched.json"), enq)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSaveValidatesCron(t *testing.T) {
	s := newTmp(t, &fakeEnq{})
	if _, err := s.Save(Job{Name: "x", Schedule: "garbage", Kind: "apt_upgrade", Enabled: true}); err == nil {
		t.Error("expected error for bogus cron")
	}
	if _, err := s.Save(Job{Name: "x", Schedule: "*/5 * * * *", Kind: "apt_upgrade", Enabled: true}); err != nil {
		t.Errorf("valid cron rejected: %v", err)
	}
}

func TestSaveRequiresKind(t *testing.T) {
	s := newTmp(t, &fakeEnq{})
	if _, err := s.Save(Job{Name: "x", Schedule: "* * * * *"}); err == nil {
		t.Error("missing kind should fail")
	}
}

func TestNextFires(t *testing.T) {
	s := newTmp(t, &fakeEnq{})
	out, err := s.NextFires("0 * * * *", 5)
	if err != nil || len(out) != 5 {
		t.Fatalf("want 5 fires, got %d err=%v", len(out), err)
	}
	// each one hour apart
	for i := 1; i < len(out); i++ {
		dt := out[i].Sub(out[i-1])
		if dt < 59*time.Minute || dt > 61*time.Minute {
			t.Errorf("gap %d: %s", i, dt)
		}
	}
}

func TestRunNowEnqueues(t *testing.T) {
	enq := &fakeEnq{}
	s := newTmp(t, enq)
	j, _ := s.Save(Job{Name: "x", Schedule: "0 0 * * *", Kind: "apt_upgrade", Owner: "alice", Enabled: true})
	if _, err := s.RunNow(j.ID); err != nil {
		t.Fatal(err)
	}
	if len(enq.calls) != 1 {
		t.Fatalf("expected 1 enqueue, got %d", len(enq.calls))
	}
	if enq.calls[0].source != "scheduler-manual:"+j.ID {
		t.Errorf("manual source not stamped: %s", enq.calls[0].source)
	}
}

func TestRunNowOnUnknownID(t *testing.T) {
	s := newTmp(t, &fakeEnq{})
	if _, err := s.RunNow("nope"); err != ErrNotFound {
		t.Errorf("got %v want ErrNotFound", err)
	}
}

func TestTickFiresOnceWhenDue(t *testing.T) {
	enq := &fakeEnq{}
	s := newTmp(t, enq)
	// force a NextFire in the past by saving + tweaking
	j, _ := s.Save(Job{Name: "x", Schedule: "*/1 * * * *", Kind: "apt_upgrade", Owner: "alice", Enabled: true})
	s.mu.Lock()
	s.jobs[j.ID].NextFire = time.Now().Add(-1 * time.Minute).Unix()
	s.mu.Unlock()
	s.tickOnce()
	if len(enq.calls) != 1 {
		t.Errorf("expected 1 fire, got %d", len(enq.calls))
	}
	// tick again immediately: NextFire should have rolled forward.
	enq.calls = nil
	s.tickOnce()
	if len(enq.calls) != 0 {
		t.Errorf("double-fire: got %d", len(enq.calls))
	}
}

func TestAlertOnFail(t *testing.T) {
	enq := &fakeEnq{failOn: "apt_upgrade"}
	s := newTmp(t, enq)
	var alerted struct {
		owner  string
		status string
		err    string
		called int
	}
	s.SetAlerter(func(owner string, j *Job, jobID string, status string, lastErr string) {
		alerted.owner = owner
		alerted.status = status
		alerted.err = lastErr
		alerted.called++
	})
	j, _ := s.Save(Job{Name: "x", Schedule: "* * * * *", Kind: "apt_upgrade", Owner: "alice", Enabled: true, AlertOn: AlertFail})
	_, _ = s.RunNow(j.ID)
	if alerted.called != 1 {
		t.Errorf("alerter not called; got %d", alerted.called)
	}
	if alerted.status != "failed" {
		t.Errorf("status: got %s", alerted.status)
	}
}

func TestPersistAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sched.json")
	s, _ := New(path, &fakeEnq{})
	saved, _ := s.Save(Job{Name: "weekly", Schedule: "0 3 * * 0", Kind: "backup_now", Owner: "alice", Enabled: true})

	s2, err := New(path, &fakeEnq{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s2.Get(saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Schedule != "0 3 * * 0" {
		t.Errorf("schedule lost: %q", got.Schedule)
	}
}

// The autonomous tick skips a job whose owner is not authorized for its kind:
// no enqueue, LastStatus="skipped", and (asserted by the absence of a returned
// error in tickOnce) no log spam per interval.
func TestTickSkipsUnauthorized(t *testing.T) {
	enq := &fakeEnq{}
	s := newTmp(t, enq)
	s.SetAuthorizer(func(owner, kind string) bool { return false }) // deny all
	j, _ := s.Save(Job{Name: "x", Schedule: "*/1 * * * *", Kind: "apt_upgrade", Owner: "jordan", Enabled: true})
	s.mu.Lock()
	s.jobs[j.ID].NextFire = time.Now().Add(-1 * time.Minute).Unix()
	s.mu.Unlock()
	s.tickOnce()
	if len(enq.calls) != 0 {
		t.Fatalf("unauthorized job enqueued %d times", len(enq.calls))
	}
	got, _ := s.Get(j.ID)
	if got.LastStatus != "skipped" {
		t.Errorf("LastStatus = %q, want skipped", got.LastStatus)
	}
}

// fire() returns nil (not an error) for a skipped autonomous job, so tickOnce
// does not log "fire: ..." every cron interval for a denied orphan.
func TestTickSkipReturnsNoError(t *testing.T) {
	s := newTmp(t, &fakeEnq{})
	s.SetAuthorizer(func(owner, kind string) bool { return false })
	j, _ := s.Save(Job{Name: "x", Schedule: "* * * * *", Kind: "shell", Owner: "jordan", Enabled: true})
	got, _ := s.Get(j.ID)
	if qid, err := s.fire(got, false); err != nil || qid != "" {
		t.Errorf("denied autonomous fire: qid=%q err=%v, want \"\",nil", qid, err)
	}
}

// The authorizer gates ONLY the autonomous path; manual RunNow stays authorized
// by the HTTP layer and must still enqueue even when the authorizer denies.
func TestRunNowBypassesAuthorizer(t *testing.T) {
	enq := &fakeEnq{}
	s := newTmp(t, enq)
	s.SetAuthorizer(func(owner, kind string) bool { return false })
	j, _ := s.Save(Job{Name: "x", Schedule: "0 0 * * *", Kind: "apt_upgrade", Owner: "alice", Enabled: true})
	if _, err := s.RunNow(j.ID); err != nil {
		t.Fatal(err)
	}
	if len(enq.calls) != 1 {
		t.Errorf("manual run-now was gated: got %d enqueues, want 1", len(enq.calls))
	}
}

// Chaining: OnQueueTerminal enqueues the follow-up on success, respects then_on,
// rechecks authz, and never chains a "scheduler-chain:" source (no loops).
func TestChainOnTerminal(t *testing.T) {
	enq := &fakeEnq{}
	s := newTmp(t, enq)
	j, _ := s.Save(Job{Name: "x", Schedule: "0 0 * * *", Kind: "apt_upgrade", Owner: "alice", Enabled: true, ThenKind: "cleanup"})

	// success → chain fires (1 enqueue with chain source)
	s.OnQueueTerminal("scheduler:"+j.ID, "done")
	if len(enq.calls) != 1 || enq.calls[0].kind != "cleanup" || enq.calls[0].source != "scheduler-chain:"+j.ID {
		t.Fatalf("expected chained cleanup, got %+v", enq.calls)
	}
	// a chained source must NOT chain again (loop guard)
	enq.calls = nil
	s.OnQueueTerminal("scheduler-chain:"+j.ID, "done")
	if len(enq.calls) != 0 {
		t.Fatalf("chain source should not re-chain, got %+v", enq.calls)
	}
	// failure with default then_on (success) → no chain
	enq.calls = nil
	s.OnQueueTerminal("scheduler:"+j.ID, "failed")
	if len(enq.calls) != 0 {
		t.Fatalf("failure should not chain on default then_on, got %+v", enq.calls)
	}
	// authz denies the follow-up → no chain
	enq.calls = nil
	s.SetAuthorizer(func(owner, kind string) bool { return false })
	s.OnQueueTerminal("scheduler:"+j.ID, "done")
	if len(enq.calls) != 0 {
		t.Fatalf("authz-denied chain should not enqueue, got %+v", enq.calls)
	}
}

func TestDisabledJobsDontFire(t *testing.T) {
	enq := &fakeEnq{}
	s := newTmp(t, enq)
	j, _ := s.Save(Job{Name: "off", Schedule: "* * * * *", Kind: "apt_upgrade", Owner: "alice", Enabled: false})
	s.mu.Lock()
	s.jobs[j.ID].NextFire = time.Now().Add(-10 * time.Minute).Unix()
	s.mu.Unlock()
	s.tickOnce()
	if len(enq.calls) != 0 {
		t.Errorf("disabled job fired %d times", len(enq.calls))
	}
}
