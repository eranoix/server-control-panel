package screens

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"server-control-panel/internal/mobilebff/sdui"
	"server-control-panel/internal/scheduler"
)

type fakeSchedulerBackend struct {
	mu   sync.Mutex
	jobs map[string]*scheduler.Job

	saveCalls   int
	deleteCalls int
	runNowCalls int
	audit       []auditRecord
}

func newFakeSchedulerBackend() *fakeSchedulerBackend {
	return &fakeSchedulerBackend{
		jobs: map[string]*scheduler.Job{
			"job-admin-root": {
				ID: "job-admin-root", Name: "Backup root", Schedule: "0 3 * * *",
				Kind: "docker_prune", Owner: "sched-admin", RunAsRoot: true, Enabled: true, Created: 1000,
			},
			"job-user-owned": {
				ID: "job-user-owned", Name: "User's job", Schedule: "*/15 * * * *",
				Kind: "docker_prune", Owner: "sched-user", Enabled: true, Created: 1000,
			},
			"job-other-owned": {
				ID: "job-other-owned", Name: "Another user's job", Schedule: "*/30 * * * *",
				Kind: "docker_prune", Owner: "sched-other", Enabled: true, Created: 1000,
			},
			"job-orphaned-kind": {
				ID: "job-orphaned-kind", Name: "Orphaned job", Schedule: "*/5 * * * *",
				Kind: "system_reboot", Owner: "sched-user", Enabled: true, Created: 1000,
			},
		},
	}
}

func (b *fakeSchedulerBackend) deps() SchedulerDeps {
	return SchedulerDeps{
		ListJobs: func(owner string) []*scheduler.Job {
			b.mu.Lock()
			defer b.mu.Unlock()
			var out []*scheduler.Job
			for _, j := range b.jobs {
				if owner == "" || j.Owner == owner {
					out = append(out, j)
				}
			}
			return out
		},
		GetJob: func(id string) (*scheduler.Job, error) {
			b.mu.Lock()
			defer b.mu.Unlock()
			j, ok := b.jobs[id]
			if !ok {
				return nil, scheduler.ErrNotFound
			}
			cp := *j
			return &cp, nil
		},
		SaveJob: func(j scheduler.Job) (*scheduler.Job, error) {
			b.mu.Lock()
			defer b.mu.Unlock()
			b.saveCalls++
			if j.ID == "" {
				j.ID = "job-new"
			}
			cp := j
			b.jobs[j.ID] = &cp
			return &cp, nil
		},
		DeleteJob: func(id string) error {
			b.mu.Lock()
			defer b.mu.Unlock()
			b.deleteCalls++
			if _, ok := b.jobs[id]; !ok {
				return scheduler.ErrNotFound
			}
			delete(b.jobs, id)
			return nil
		},
		RunNow: func(id string) (string, error) {
			b.mu.Lock()
			defer b.mu.Unlock()
			b.runNowCalls++
			j, ok := b.jobs[id]
			if !ok {
				return "", scheduler.ErrNotFound
			}
			j.LastStatus = "ok"
			j.LastFire = time.Now().Unix()
			return "queue-job-1", nil
		},
		NextFires: func(expr string, n int) ([]time.Time, error) {
			if expr == "" || expr == "not a cron" {
				return nil, errors.New("invalid cron expression")
			}
			return []time.Time{time.Now()}, nil
		},
		AuthorizedKinds: func(_ string, isAdmin bool) []KindOption {
			shared := KindOption{Value: "docker_prune", Label: "Prune Docker"}
			if isAdmin {
				return []KindOption{shared, {Value: "system_reboot", Label: "Reboot system"}}
			}
			return []KindOption{shared}
		},
		AuditEvent: func(user, action, target string) {
			b.mu.Lock()
			defer b.mu.Unlock()
			b.audit = append(b.audit, auditRecord{user: user, action: action, target: target})
		},
	}
}

type auditRecord struct{ user, action, target string }

func (b *fakeSchedulerBackend) auditLog() []auditRecord {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]auditRecord, len(b.audit))
	copy(out, b.audit)
	return out
}

func (b *fakeSchedulerBackend) job(id string) *scheduler.Job {
	b.mu.Lock()
	defer b.mu.Unlock()
	j, ok := b.jobs[id]
	if !ok {
		return nil
	}
	cp := *j
	return &cp
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func TestSchedulerAction_Save_BadCronKeysSchedule(t *testing.T) {
	backend := newFakeSchedulerBackend()
	handle := handleSchedulerJobSave(backend.deps())
	admin, _ := testSchedulerViewers()

	input := mustJSON(t, saveJobInput{Name: "x", Schedule: "not a cron", Kind: "docker_prune", Enabled: true})
	_, err := handle(context.Background(), admin, nil, input)

	var fe sdui.FieldErrors
	if !errors.As(err, &fe) {
		t.Fatalf("err = %v (%T), want sdui.FieldErrors", err, err)
	}
	if len(fe) != 1 {
		t.Fatalf("FieldErrors has %d key(s), want 1: %v", len(fe), fe)
	}
	if _, ok := fe["schedule"]; !ok {
		t.Fatalf("FieldErrors does not have the \"schedule\" key: %v", fe)
	}
	if !errors.Is(err, sdui.ErrValidation) {
		t.Errorf("errors.Is(err, sdui.ErrValidation) = false")
	}

	env, buildErr := buildSchedulerJobsScreen(backend.deps(), admin)
	if buildErr != nil {
		t.Fatalf("build screen: %v", buildErr)
	}
	form := findComponent(t, env, "job-form").(sdui.FormComponent)
	if matchErr := fe.MatchesForm(&form); matchErr != nil {
		t.Errorf("MatchesForm: %v", matchErr)
	}
}

func TestSchedulerAction_Save_EmptyNameAndUnknownKind(t *testing.T) {
	backend := newFakeSchedulerBackend()
	handle := handleSchedulerJobSave(backend.deps())
	admin, _ := testSchedulerViewers()

	_, err := handle(context.Background(), admin, nil, mustJSON(t, saveJobInput{
		Name: "", Schedule: "*/5 * * * *", Kind: "docker_prune",
	}))
	var fe sdui.FieldErrors
	if !errors.As(err, &fe) {
		t.Fatalf("empty name: err = %v, want FieldErrors", err)
	}
	if _, ok := fe["name"]; !ok {
		t.Errorf("empty name: FieldErrors does not have \"name\": %v", fe)
	}

	_, err = handle(context.Background(), admin, nil, mustJSON(t, saveJobInput{
		Name: "x", Schedule: "*/5 * * * *", Kind: "kind_that_does_not_exist",
	}))
	fe = nil
	if !errors.As(err, &fe) {
		t.Fatalf("unknown kind: err = %v, want FieldErrors", err)
	}
	if _, ok := fe["kind"]; !ok {
		t.Errorf("unknown kind: FieldErrors does not have \"kind\": %v", fe)
	}
}

func TestSchedulerAction_Save_NonAdminRunAsRootDenied(t *testing.T) {
	backend := newFakeSchedulerBackend()
	handle := handleSchedulerJobSave(backend.deps())
	_, nonAdmin := testSchedulerViewers()

	runAsRoot := true
	_, err := handle(context.Background(), nonAdmin, nil, mustJSON(t, saveJobInput{
		Name: "x", Schedule: "*/5 * * * *", Kind: "docker_prune", RunAsRoot: &runAsRoot,
	}))

	var fe sdui.FieldErrors
	if !errors.As(err, &fe) {
		t.Fatalf("err = %v, want FieldErrors", err)
	}
	if _, ok := fe["run_as_root"]; !ok {
		t.Errorf("FieldErrors does not have \"run_as_root\": %v", fe)
	}
	if backend.saveCalls != 0 {
		t.Errorf("SaveJob was called %d time(s), want 0", backend.saveCalls)
	}
	log := backend.auditLog()
	if len(log) != 1 || log[0].action != "scheduler.run_as_root_denied" {
		t.Errorf("audit log = %v, want 1 scheduler.run_as_root_denied entry", log)
	}
}

func TestSchedulerAction_NonAdminOnAnotherUsersJob_NotFound(t *testing.T) {
	backend := newFakeSchedulerBackend()
	_, nonAdmin := testSchedulerViewers()

	saveHandle := handleSchedulerJobSave(backend.deps())
	_, err := saveHandle(context.Background(), nonAdmin, nil, mustJSON(t, saveJobInput{
		ID: "job-other-owned", Name: "x", Schedule: "*/5 * * * *", Kind: "docker_prune",
	}))
	if !errors.Is(err, sdui.ErrActionNotFound) {
		t.Errorf("save on another user's job: err = %v, want sdui.ErrActionNotFound", err)
	}

	deleteHandle := handleSchedulerJobDelete(backend.deps())
	_, err = deleteHandle(context.Background(), nonAdmin, map[string]string{"id": "job-other-owned"}, nil)
	if !errors.Is(err, sdui.ErrActionNotFound) {
		t.Errorf("delete on another user's job: err = %v, want sdui.ErrActionNotFound", err)
	}
	if backend.deleteCalls != 0 {
		t.Errorf("DeleteJob was called %d time(s), want 0", backend.deleteCalls)
	}
}

func TestSchedulerAction_ResponseModes(t *testing.T) {
	backend := newFakeSchedulerBackend()
	admin, _ := testSchedulerViewers()

	saveHandle := handleSchedulerJobSave(backend.deps())
	saveResult, err := saveHandle(context.Background(), admin, nil, mustJSON(t, saveJobInput{
		Name: "New job", Schedule: "*/5 * * * *", Kind: "docker_prune", Enabled: true,
	}))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if len(saveResult.Invalidate) == 0 || saveResult.Patch != nil {
		t.Errorf("save: result = %+v, want only Invalidate filled in", saveResult)
	}
	if valErr := saveResult.Validate(); valErr != nil {
		t.Errorf("save: ActionResult.Validate: %v", valErr)
	}

	runNowHandle := handleSchedulerJobRunNow(backend.deps())
	runResult, err := runNowHandle(context.Background(), admin, map[string]string{"id": "job-admin-root"}, nil)
	if err != nil {
		t.Fatalf("run_now: %v", err)
	}
	if runResult.Patch == nil || len(runResult.Invalidate) != 0 {
		t.Errorf("run_now: result = %+v, want only Patch filled in", runResult)
	}
	if valErr := runResult.Validate(); valErr != nil {
		t.Errorf("run_now: ActionResult.Validate: %v", valErr)
	}
}

var (
	registerSchedulerActionsTestOnce sync.Once
	registerSchedulerActionsTestDeps *fakeSchedulerBackend
)

func registerSchedulerActionsForTest() *fakeSchedulerBackend {
	registerSchedulerActionsTestOnce.Do(func() {
		registerSchedulerActionsTestDeps = newFakeSchedulerBackend()
		registerSchedulerActions(registerSchedulerActionsTestDeps.deps())
	})
	return registerSchedulerActionsTestDeps
}

func TestSchedulerAction_DeleteRequiresConfirmation(t *testing.T) {
	backend := registerSchedulerActionsForTest()
	admin, _ := testSchedulerViewers()

	before := backend.job("job-admin-root")
	if before == nil {
		t.Fatal("fixture job-admin-root missing before the test")
	}

	_, err := sdui.RunAction(context.Background(), schedulerActionDelete, admin,
		map[string]string{"id": "job-admin-root"}, nil, sdui.Confirmation{Confirmed: false})

	var fe sdui.FieldErrors
	if !errors.As(err, &fe) {
		t.Fatalf("without confirmation: err = %v, want FieldErrors", err)
	}
	if _, ok := fe[sdui.ConfirmationFieldKey]; !ok {
		t.Errorf("without confirmation: FieldErrors does not have %q: %v", sdui.ConfirmationFieldKey, fe)
	}
	if backend.deleteCalls != 0 {
		t.Errorf("DeleteJob was called %d time(s) without confirmation, want 0", backend.deleteCalls)
	}
	if backend.job("job-admin-root") == nil {
		t.Fatal("job was removed even without confirmation")
	}

	result, err := sdui.RunAction(context.Background(), schedulerActionDelete, admin,
		map[string]string{"id": "job-admin-root"}, nil, sdui.Confirmation{Confirmed: true})
	if err != nil {
		t.Fatalf("with confirmation: err = %v", err)
	}
	if len(result.Invalidate) == 0 {
		t.Errorf("with confirmation: result = %+v, want Invalidate filled in", result)
	}
	if backend.deleteCalls != 1 {
		t.Errorf("DeleteJob was called %d time(s), want 1", backend.deleteCalls)
	}
	if backend.job("job-admin-root") != nil {
		t.Error("job still present after confirmation")
	}
}

func TestSchedulerAction_RunNow_ReauthorizesKind(t *testing.T) {
	backend := newFakeSchedulerBackend()
	_, nonAdmin := testSchedulerViewers()

	runNowHandle := handleSchedulerJobRunNow(backend.deps())
	_, err := runNowHandle(context.Background(), nonAdmin, map[string]string{"id": "job-orphaned-kind"}, nil)
	if !errors.Is(err, sdui.ErrActionNotFound) {
		t.Errorf("err = %v, want sdui.ErrActionNotFound", err)
	}
	if backend.runNowCalls != 0 {
		t.Errorf("RunNow was called %d time(s), want 0", backend.runNowCalls)
	}
}
