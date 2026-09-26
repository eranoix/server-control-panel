package screens

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"server-control-panel/internal/config"
	"server-control-panel/internal/deploy"
	"server-control-panel/internal/jira"
	"server-control-panel/internal/mobilebff/sdui"
	"server-control-panel/internal/queue"
)

// fakeMiscBackend is an in-memory stand-in for MiscDeps' mutating closures,
// plus spy counters — mirrors fakeDockerActionsBackend/fakeAlertsBackend so
// tests can assert a mutation NEVER happened (e.g. DestroyDeployApp/
// CancelQueueJob must not be reached when the confirmation gate, RBAC gate
// or field validation rejects the request first).
type fakeMiscBackend struct {
	mu sync.Mutex

	aiModels config.AIModels

	jiraConnected       map[string]bool
	jiraConnectCalls    int
	jiraTransitions     []jira.Transition
	jiraTransitionCalls int
	jiraCommentCalls    int

	deployAppCreateCalls   int
	deployAppRedeployCalls int
	deployAppDeleteCalls   int

	jobs               map[string]*queue.Job
	queueCancelCalls   int
	queueRerunCalls    int
	authorizedForRerun bool

	audit []miscAuditRecord
}

type miscAuditRecord struct{ user, action, target string }

func newFakeMiscBackend() *fakeMiscBackend {
	return &fakeMiscBackend{
		jiraConnected:      map[string]bool{},
		authorizedForRerun: true,
		jobs:               map[string]*queue.Job{},
	}
}

func (b *fakeMiscBackend) deps() MiscDeps {
	return MiscDeps{
		AIModelsConfig: func() (config.AIModels, map[string]string) {
			b.mu.Lock()
			defer b.mu.Unlock()
			return b.aiModels, map[string]string{}
		},
		SaveAIModels: func(m config.AIModels) error {
			b.mu.Lock()
			defer b.mu.Unlock()
			b.aiModels = m
			return nil
		},

		JiraStatus: func(user string) (bool, string) {
			b.mu.Lock()
			defer b.mu.Unlock()
			return b.jiraConnected[user], "PROJ"
		},
		JiraConnect: func(user, site, email, token, project string) error {
			b.mu.Lock()
			defer b.mu.Unlock()
			b.jiraConnectCalls++
			b.jiraConnected[user] = true
			return nil
		},
		JiraListIssues: func(_ context.Context, _ string, _ string) ([]jira.Issue, error) {
			return nil, nil
		},
		JiraGetIssue: func(_ context.Context, _ string, _ string) (*jira.IssueDetail, error) {
			return &jira.IssueDetail{}, nil
		},
		JiraTransitions: func(_ context.Context, _ string, _ string) ([]jira.Transition, error) {
			b.mu.Lock()
			defer b.mu.Unlock()
			return append([]jira.Transition(nil), b.jiraTransitions...), nil
		},
		JiraTransition: func(_ context.Context, _ string, _ string, _ string) error {
			b.mu.Lock()
			defer b.mu.Unlock()
			b.jiraTransitionCalls++
			return nil
		},
		JiraAddComment: func(_ context.Context, _ string, _ string, _ string) (*jira.Comment, error) {
			b.mu.Lock()
			defer b.mu.Unlock()
			b.jiraCommentCalls++
			return &jira.Comment{}, nil
		},

		ListDeployApps: func() ([]deploy.App, error) { return nil, nil },
		GetDeployApp:   func(string) (deploy.App, bool, error) { return deploy.App{}, false, nil },
		CreateDeployApp: func(a deploy.App) (deploy.App, error) {
			b.mu.Lock()
			defer b.mu.Unlock()
			b.deployAppCreateCalls++
			return a, nil
		},
		TriggerRedeploy: func(_ string, _ string) (string, error) {
			b.mu.Lock()
			defer b.mu.Unlock()
			b.deployAppRedeployCalls++
			return "job-1", nil
		},
		DestroyDeployApp: func(_ context.Context, _ string) error {
			b.mu.Lock()
			defer b.mu.Unlock()
			b.deployAppDeleteCalls++
			return nil
		},

		ListQueueJobs: func(string) []*queue.Job { return nil },
		GetQueueJob: func(id string) (*queue.Job, error) {
			b.mu.Lock()
			defer b.mu.Unlock()
			j, ok := b.jobs[id]
			if !ok {
				return nil, errors.New("job não encontrado")
			}
			return j, nil
		},
		CancelQueueJob: func(_ string) error {
			b.mu.Lock()
			defer b.mu.Unlock()
			b.queueCancelCalls++
			return nil
		},
		RerunQueueJob: func(id string) (*queue.Job, error) {
			b.mu.Lock()
			defer b.mu.Unlock()
			b.queueRerunCalls++
			return b.jobs[id], nil
		},
		AuthorizedForRerun: func(_ string, _ bool, _ string) bool {
			b.mu.Lock()
			defer b.mu.Unlock()
			return b.authorizedForRerun
		},

		AuditEvent: func(user, action, target string) {
			b.mu.Lock()
			defer b.mu.Unlock()
			b.audit = append(b.audit, miscAuditRecord{user: user, action: action, target: target})
		},
	}
}

func (b *fakeMiscBackend) counts() (deployDelete, queueCancel int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.deployAppDeleteCalls, b.queueCancelCalls
}

func (b *fakeMiscBackend) setJob(j *queue.Job) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.jobs[j.ID] = j
}

func (b *fakeMiscBackend) setTransitions(ts []jira.Transition) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.jiraTransitions = ts
}

// registerMiscActionsForTest registers the real misc.* actions in this test
// binary's global sdui action registry EXACTLY once — RegisterAction panics
// on a duplicate ActionID, and only the tests below need the real registry
// (to exercise RunAction's confirmation/authorize steps end to end); every
// other test in this file calls the handler-producing functions directly,
// bypassing the registry. Mirrors registerDockerActionsForTest exactly.
var (
	registerMiscActionsTestOnce sync.Once
	registerMiscActionsTestDeps *fakeMiscBackend
)

func registerMiscActionsForTest() *fakeMiscBackend {
	registerMiscActionsTestOnce.Do(func() {
		registerMiscActionsTestDeps = newFakeMiscBackend()
		registerMiscActions(registerMiscActionsTestDeps.deps())
	})
	return registerMiscActionsTestDeps
}

// --- Test 1: destructive gate — zero calls without confirmation -----------

// TestMiscAction_DestructiveActionsRequireConfirmation proves
// deploy.app.delete and queue.job.cancel are unreachable without
// confirmation: an unconfirmed RunAction call returns a ConfirmationFieldKey
// FieldErrors WITHOUT ever calling deps.DestroyDeployApp/deps.CancelQueueJob
// mirrors TestDockerAction_DestructiveActionsRequireConfirmation.
func TestMiscAction_DestructiveActionsRequireConfirmation(t *testing.T) {
	backend := registerMiscActionsForTest()
	admin, _ := testMiscViewers()
	backend.setJob(&queue.Job{ID: "job-cancel-1", Kind: "app_deploy", Owner: admin.Username, Status: queue.StatusRunning})

	baseDelete, baseCancel := backend.counts()

	cases := []struct {
		actionID string
		params   map[string]string
	}{
		{miscActionDeployAppDelete, map[string]string{"id": "app1"}},
		{miscActionQueueJobCancel, map[string]string{"id": "job-cancel-1"}},
	}
	for _, c := range cases {
		_, err := sdui.RunAction(context.Background(), c.actionID, admin, c.params, nil, sdui.Confirmation{Confirmed: false})
		var fe sdui.FieldErrors
		if !errors.As(err, &fe) {
			t.Fatalf("%s without confirmation: err = %v (%T), want FieldErrors", c.actionID, err, err)
		}
		if _, ok := fe[sdui.ConfirmationFieldKey]; !ok {
			t.Errorf("%s without confirmation: FieldErrors does not have %q: %v", c.actionID, sdui.ConfirmationFieldKey, fe)
		}
	}

	deleteAfter, cancelAfter := backend.counts()
	if deleteAfter != baseDelete {
		t.Errorf("DestroyDeployApp was called without confirmation: before=%d after=%d", baseDelete, deleteAfter)
	}
	if cancelAfter != baseCancel {
		t.Errorf("CancelQueueJob was called without confirmation: before=%d after=%d", baseCancel, cancelAfter)
	}

	// Now confirm properly and prove the handlers actually run.
	if _, err := sdui.RunAction(context.Background(), miscActionDeployAppDelete, admin, map[string]string{"id": "app1"}, nil, sdui.Confirmation{Confirmed: true}); err != nil {
		t.Fatalf("deploy.app.delete confirmed: err = %v", err)
	}
	if _, err := sdui.RunAction(context.Background(), miscActionQueueJobCancel, admin, map[string]string{"id": "job-cancel-1"}, nil, sdui.Confirmation{Confirmed: true}); err != nil {
		t.Fatalf("queue.job.cancel confirmed: err = %v", err)
	}
	deleteFinal, cancelFinal := backend.counts()
	if deleteFinal != baseDelete+1 {
		t.Errorf("DestroyDeployApp called %d time(s) after confirmation, want %d", deleteFinal, baseDelete+1)
	}
	if cancelFinal != baseCancel+1 {
		t.Errorf("CancelQueueJob called %d time(s) after confirmation, want %d", cancelFinal, baseCancel+1)
	}
}

// --- Test 2: validation FieldErrors -----------------------------------------

// TestMiscAction_ValidationReturnsFieldErrors proves ai.settings.save,
// jira.connect and deploy.app.create each return field-keyed FieldErrors on
// bad input, never a generic error, and never reach their domain closure.
func TestMiscAction_ValidationReturnsFieldErrors(t *testing.T) {
	backend := newFakeMiscBackend()
	deps := backend.deps()
	admin, _ := testMiscViewers()

	t.Run("ai.settings.save invalid model", func(t *testing.T) {
		handler := handleAISettingsSave(deps)
		body, _ := json.Marshal(aiSettingsSaveInput{Suggest: "not-a-real-model", JiraAI: "haiku"})
		_, err := handler(context.Background(), admin, nil, body)
		var fe sdui.FieldErrors
		if !errors.As(err, &fe) {
			t.Fatalf("err = %v (%T), want FieldErrors", err, err)
		}
		if _, ok := fe["suggest"]; !ok {
			t.Errorf("FieldErrors does not have the \"suggest\" key: %v", fe)
		}
	})

	t.Run("jira.connect required fields missing", func(t *testing.T) {
		handler := handleJiraConnect(deps)
		body, _ := json.Marshal(jiraConnectInput{})
		_, err := handler(context.Background(), admin, nil, body)
		var fe sdui.FieldErrors
		if !errors.As(err, &fe) {
			t.Fatalf("err = %v (%T), want FieldErrors", err, err)
		}
		for _, key := range []string{"site", "email", "token"} {
			if _, ok := fe[key]; !ok {
				t.Errorf("FieldErrors does not have the %q key: %v", key, fe)
			}
		}
		if backend.jiraConnectCalls != 0 {
			t.Errorf("JiraConnect was called %d time(s) with an invalid body, want 0", backend.jiraConnectCalls)
		}
	})

	t.Run("deploy.app.create name missing", func(t *testing.T) {
		handler := handleDeployAppCreate(deps)
		body, _ := json.Marshal(deployAppCreateInput{Domain: "x.example.com"})
		_, err := handler(context.Background(), admin, nil, body)
		var fe sdui.FieldErrors
		if !errors.As(err, &fe) {
			t.Fatalf("err = %v (%T), want FieldErrors", err, err)
		}
		if _, ok := fe["name"]; !ok {
			t.Errorf("FieldErrors does not have the \"name\" key: %v", fe)
		}
		if backend.deployAppCreateCalls != 0 {
			t.Errorf("CreateDeployApp was called %d time(s) with an invalid body, want 0", backend.deployAppCreateCalls)
		}
	})
}

// --- Test 3: RBAC parity — unauthorized admin-only mutation -----------------

// TestMiscAction_NonAdminMutatingActionsNotFound proves a non-admin invoking
// any admin-only mutating action directly — even fully confirmed — gets
// sdui.ErrActionNotFound at RunAction's authorize step, never reaching the
// handler. Covers ai.settings.save and all three deploy.app.*
// actions (miscAdminViewer-gated); queue.job.* is deliberately excluded here
// since it is NOT admin-gated (ownership-gated instead, see Test 4-adjacent
// TestMiscAction_QueueJob_OwnershipGate below).
func TestMiscAction_NonAdminMutatingActionsNotFound(t *testing.T) {
	backend := registerMiscActionsForTest()
	_, nonAdmin := testMiscViewers()

	baseDelete, _ := backend.counts()
	baseCreate := backend.deployAppCreateCalls
	baseRedeploy := backend.deployAppRedeployCalls

	cases := []struct {
		actionID string
		params   map[string]string
		input    json.RawMessage
	}{
		{miscActionAISettingsSave, nil, mustJSONBytes(t, aiSettingsSaveInput{Suggest: "haiku", JiraAI: "haiku"})},
		{miscActionDeployAppCreate, nil, mustJSONBytes(t, deployAppCreateInput{Name: "app2"})},
		{miscActionDeployAppRedeploy, map[string]string{"id": "app1"}, nil},
		{miscActionDeployAppDelete, map[string]string{"id": "app1"}, nil},
	}
	for _, c := range cases {
		_, err := sdui.RunAction(context.Background(), c.actionID, nonAdmin, c.params, c.input, sdui.Confirmation{Confirmed: true})
		if !errors.Is(err, sdui.ErrActionNotFound) {
			t.Errorf("%s non-admin: err = %v, want sdui.ErrActionNotFound", c.actionID, err)
		}
	}

	deleteAfter, _ := backend.counts()
	if deleteAfter != baseDelete {
		t.Errorf("DestroyDeployApp was reached by a non-admin: before=%d after=%d", baseDelete, deleteAfter)
	}
	if backend.deployAppCreateCalls != baseCreate {
		t.Errorf("CreateDeployApp was reached by a non-admin: before=%d after=%d", baseCreate, backend.deployAppCreateCalls)
	}
	if backend.deployAppRedeployCalls != baseRedeploy {
		t.Errorf("TriggerRedeploy was reached by a non-admin: before=%d after=%d", baseRedeploy, backend.deployAppRedeployCalls)
	}
}

// TestMiscAction_QueueJob_OwnershipGate proves queue.job.retry/cancel are
// reachable by a non-admin for THEIR OWN job, but return ErrActionNotFound
// (never calling RerunQueueJob/CancelQueueJob) for a job owned by someone
// else — the resource-level ownership check that lives inside the handler
// itself, since RunAction's binary authorize gate cannot express per-resource
// ownership (see registerMiscActions' doc comment on these two actions).
func TestMiscAction_QueueJob_OwnershipGate(t *testing.T) {
	backend := newFakeMiscBackend()
	deps := backend.deps()
	_, nonAdmin := testMiscViewers()

	backend.setJob(&queue.Job{ID: "mine", Kind: "app_deploy", Owner: nonAdmin.Username, Status: queue.StatusRunning})
	backend.setJob(&queue.Job{ID: "not-mine", Kind: "app_deploy", Owner: "someone-else", Status: queue.StatusRunning})

	rerun := handleQueueJobRerun(deps)
	if _, err := rerun(context.Background(), nonAdmin, map[string]string{"id": "mine"}, nil); err != nil {
		t.Errorf("retry of one's own job: err = %v, want nil", err)
	}
	if backend.queueRerunCalls != 1 {
		t.Errorf("RerunQueueJob called %d time(s) for one's own job, want 1", backend.queueRerunCalls)
	}

	if _, err := rerun(context.Background(), nonAdmin, map[string]string{"id": "not-mine"}, nil); !errors.Is(err, sdui.ErrActionNotFound) {
		t.Errorf("retry of someone else's job: err = %v, want sdui.ErrActionNotFound", err)
	}
	if backend.queueRerunCalls != 1 {
		t.Errorf("RerunQueueJob was reached for someone else's job: total=%d, want to stay at 1", backend.queueRerunCalls)
	}

	cancel := handleQueueJobCancel(deps)
	if _, err := cancel(context.Background(), nonAdmin, map[string]string{"id": "mine"}, nil); err != nil {
		t.Errorf("cancel of one's own job: err = %v, want nil", err)
	}
	if backend.queueCancelCalls != 1 {
		t.Errorf("CancelQueueJob called %d time(s) for one's own job, want 1", backend.queueCancelCalls)
	}

	if _, err := cancel(context.Background(), nonAdmin, map[string]string{"id": "not-mine"}, nil); !errors.Is(err, sdui.ErrActionNotFound) {
		t.Errorf("cancel of someone else's job: err = %v, want sdui.ErrActionNotFound", err)
	}
	if backend.queueCancelCalls != 1 {
		t.Errorf("CancelQueueJob was reached for someone else's job: total=%d, want to stay at 1", backend.queueCancelCalls)
	}
}

// --- Test 4: Jira transition validity — never a raw passthrough ------------

// TestMiscAction_JiraTransition_InvalidTransitionIsFieldError proves an
// out-of-list transition_id never reaches deps.JiraTransition — it comes
// back as a form-level FieldErrors instead, never a raw Jira API error
// passthrough. Conversely, a transition_id that IS in the fresh list from
// deps.JiraTransitions succeeds and calls deps.JiraTransition exactly once.
func TestMiscAction_JiraTransition_InvalidTransitionIsFieldError(t *testing.T) {
	backend := newFakeMiscBackend()
	deps := backend.deps()
	_, nonAdmin := testMiscViewers()

	backend.setTransitions([]jira.Transition{{ID: "31", Name: "Em progresso"}, {ID: "41", Name: "Concluído"}})
	setJiraSelectedIssue(nonAdmin.Username, "PROJ-1")

	handler := handleJiraIssueTransition(deps)

	body, _ := json.Marshal(jiraIssueTransitionInput{TransitionID: "999-nao-existe"})
	_, err := handler(context.Background(), nonAdmin, nil, body)
	var fe sdui.FieldErrors
	if !errors.As(err, &fe) {
		t.Fatalf("invalid transition: err = %v (%T), want FieldErrors", err, err)
	}
	if _, ok := fe["transition_id"]; !ok {
		t.Errorf("FieldErrors does not have the \"transition_id\" key: %v", fe)
	}
	if backend.jiraTransitionCalls != 0 {
		t.Errorf("JiraTransition was called %d time(s) with an invalid transition_id, want 0", backend.jiraTransitionCalls)
	}

	body2, _ := json.Marshal(jiraIssueTransitionInput{TransitionID: "31"})
	if _, err := handler(context.Background(), nonAdmin, nil, body2); err != nil {
		t.Fatalf("valid transition: err = %v, want nil", err)
	}
	if backend.jiraTransitionCalls != 1 {
		t.Errorf("JiraTransition called %d time(s) with a valid transition_id, want 1", backend.jiraTransitionCalls)
	}
}

// TestMiscAction_JiraTransition_NoSelectedIssueIsFieldError proves the
// transition/comment actions refuse to run (as a FieldErrors, not a panic or
// a call with an empty key) when the caller never selected an issue via
// jira.issue.select first — this uses a UNIQUE username never touched by any
// other test in this package, since jiraSelectedIssueByUser (misc.go) is
// shared package-level state.
func TestMiscAction_JiraTransition_NoSelectedIssueIsFieldError(t *testing.T) {
	backend := newFakeMiscBackend()
	deps := backend.deps()
	cfg := testMiscCfg()
	v := sdui.ViewerFrom(cfg, "misc-user-no-issue-selected")

	handler := handleJiraIssueTransition(deps)
	body, _ := json.Marshal(jiraIssueTransitionInput{TransitionID: "31"})
	_, err := handler(context.Background(), v, nil, body)
	var fe sdui.FieldErrors
	if !errors.As(err, &fe) {
		t.Fatalf("err = %v (%T), want FieldErrors", err, err)
	}
	if backend.jiraTransitionCalls != 0 {
		t.Errorf("JiraTransition was called %d time(s) with no issue selected, want 0", backend.jiraTransitionCalls)
	}
}
