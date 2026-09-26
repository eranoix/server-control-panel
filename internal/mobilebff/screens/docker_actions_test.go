package screens

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/docker/docker/api/types"

	"server-control-panel/internal/mobilebff/sdui"
)

// fakeDockerActionsBackend is an in-memory stand-in for DockerDeps' mutating
// closures, plus spy counters — mirrors fakeSchedulerBackend
// (scheduler_actions_test.go) so tests can assert a mutation NEVER happened
// (e.g. deps.Prune must not be reached when the confirmation gate or the
// empty-kinds validation rejects the request first).
type fakeDockerActionsBackend struct {
	mu sync.Mutex

	startCalls, stopCalls, restartCalls int
	removeContainerCalls                int
	removeImageCalls                    int
	composeUpCalls, composeDownCalls    int
	pruneCalls                          int
	lastPruneKinds                      []string

	audit []dockerAuditRecord
}

type dockerAuditRecord struct{ user, action, target string }

func newFakeDockerActionsBackend() *fakeDockerActionsBackend {
	return &fakeDockerActionsBackend{}
}

func (b *fakeDockerActionsBackend) deps() DockerDeps {
	return DockerDeps{
		ListContainers: func(context.Context) ([]types.Container, error) { return nil, nil },
		StartContainer: func(_ context.Context, id string) error {
			b.mu.Lock()
			defer b.mu.Unlock()
			b.startCalls++
			return nil
		},
		StopContainer: func(_ context.Context, id string) error {
			b.mu.Lock()
			defer b.mu.Unlock()
			b.stopCalls++
			return nil
		},
		RestartContainer: func(_ context.Context, id string) error {
			b.mu.Lock()
			defer b.mu.Unlock()
			b.restartCalls++
			return nil
		},
		RemoveContainer: func(_ context.Context, id string, force bool) error {
			b.mu.Lock()
			defer b.mu.Unlock()
			b.removeContainerCalls++
			return nil
		},
		RemoveImage: func(_ context.Context, id string, force bool) error {
			b.mu.Lock()
			defer b.mu.Unlock()
			b.removeImageCalls++
			return nil
		},
		ComposeUp: func(_ context.Context, stack string) (string, error) {
			b.mu.Lock()
			defer b.mu.Unlock()
			b.composeUpCalls++
			return "up ok", nil
		},
		ComposeDown: func(_ context.Context, stack string) (string, error) {
			b.mu.Lock()
			defer b.mu.Unlock()
			b.composeDownCalls++
			return "down ok", nil
		},
		Prune: func(_ context.Context, kinds []string) (map[string]any, error) {
			b.mu.Lock()
			defer b.mu.Unlock()
			b.pruneCalls++
			b.lastPruneKinds = append([]string(nil), kinds...)
			out := map[string]any{}
			for _, k := range kinds {
				out[k] = "ok"
			}
			return out, nil
		},
		AuditEvent: func(user, action, target string) {
			b.mu.Lock()
			defer b.mu.Unlock()
			b.audit = append(b.audit, dockerAuditRecord{user: user, action: action, target: target})
		},
	}
}

func (b *fakeDockerActionsBackend) counts() (start, stop, restart, removeC, removeI, up, down, prune int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.startCalls, b.stopCalls, b.restartCalls, b.removeContainerCalls, b.removeImageCalls, b.composeUpCalls, b.composeDownCalls, b.pruneCalls
}

func (b *fakeDockerActionsBackend) auditLog() []dockerAuditRecord {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]dockerAuditRecord, len(b.audit))
	copy(out, b.audit)
	return out
}

// --- Test 2: non-destructive round trip ---------------------------------

// TestDockerAction_ContainerLifecycle_ReturnsPatchOrInvalidate proves
// start/stop/restart call the right closure exactly once and return a valid
// ActionResult (Patch when the container is still listable, Invalidate
// otherwise — deps.ListContainers here returns an empty list, so every
// lifecycle call falls back to Invalidate, which is itself a correct,
// deliberately exercised branch of handleDockerContainerLifecycle).
func TestDockerAction_ContainerLifecycle_ReturnsPatchOrInvalidate(t *testing.T) {
	backend := newFakeDockerActionsBackend()
	deps := backend.deps()
	admin, _ := testDockerViewers()

	cases := []struct {
		name    string
		handler sdui.ActionHandler
		action  string
	}{
		{"start", handleDockerContainerLifecycle(deps, deps.StartContainer, dockerActionContainerStart), dockerActionContainerStart},
		{"stop", handleDockerContainerLifecycle(deps, deps.StopContainer, dockerActionContainerStop), dockerActionContainerStop},
		{"restart", handleDockerContainerLifecycle(deps, deps.RestartContainer, dockerActionContainerRestart), dockerActionContainerRestart},
	}
	for _, c := range cases {
		result, err := c.handler(context.Background(), admin, map[string]string{"id": "c1"}, nil)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if valErr := result.Validate(); valErr != nil {
			t.Errorf("%s: ActionResult.Validate: %v", c.name, valErr)
		}
	}

	start, stop, restart, _, _, _, _, _ := backend.counts()
	if start != 1 || stop != 1 || restart != 1 {
		t.Errorf("calls = start:%d stop:%d restart:%d, want 1 each", start, stop, restart)
	}

	log := backend.auditLog()
	if len(log) != 3 {
		t.Fatalf("audit log has %d entry(ies), want 3: %v", len(log), log)
	}
}

// TestDockerAction_ContainerLifecycle_EmptyIDIsNotFound proves an empty id
// never reaches the domain closure.
func TestDockerAction_ContainerLifecycle_EmptyIDIsNotFound(t *testing.T) {
	backend := newFakeDockerActionsBackend()
	deps := backend.deps()
	admin, _ := testDockerViewers()

	handler := handleDockerContainerLifecycle(deps, deps.StartContainer, dockerActionContainerStart)
	_, err := handler(context.Background(), admin, map[string]string{}, nil)
	if !errors.Is(err, sdui.ErrActionNotFound) {
		t.Errorf("err = %v, want sdui.ErrActionNotFound", err)
	}
	start, _, _, _, _, _, _, _ := backend.counts()
	if start != 0 {
		t.Errorf("StartContainer was called %d time(s), want 0", start)
	}
}

// TestDockerAction_ComposeUp_ReadsIDAsStackName proves compose.up reads the
// clicked row's "id" as the stack name and returns Invalidate.
func TestDockerAction_ComposeUp_ReadsIDAsStackName(t *testing.T) {
	backend := newFakeDockerActionsBackend()
	deps := backend.deps()
	admin, _ := testDockerViewers()

	handler := handleDockerComposeUp(deps)
	result, err := handler(context.Background(), admin, map[string]string{"id": "myapp"}, nil)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if len(result.Invalidate) == 0 || result.Patch != nil {
		t.Errorf("result = %+v, want only Invalidate", result)
	}
	if valErr := result.Validate(); valErr != nil {
		t.Errorf("ActionResult.Validate: %v", valErr)
	}
	_, _, _, _, _, up, _, _ := backend.counts()
	if up != 1 {
		t.Errorf("ComposeUp was called %d time(s), want 1", up)
	}
}

// --- Test 4: prune body validation ---------------------------------------

// TestDockerAction_PruneRun_AllFalseKindsIsFieldError proves an all-false
// body never reaches deps.Prune.
func TestDockerAction_PruneRun_AllFalseKindsIsFieldError(t *testing.T) {
	backend := newFakeDockerActionsBackend()
	deps := backend.deps()
	admin, _ := testDockerViewers()

	handler := handleDockerPruneRun(deps)
	body, _ := json.Marshal(dockerPruneInput{})
	_, err := handler(context.Background(), admin, nil, body)

	var fe sdui.FieldErrors
	if !errors.As(err, &fe) {
		t.Fatalf("err = %v (%T), want sdui.FieldErrors", err, err)
	}
	if _, ok := fe["containers"]; !ok {
		t.Errorf("FieldErrors does not have the \"containers\" key: %v", fe)
	}
	_, _, _, _, _, _, _, prune := backend.counts()
	if prune != 0 {
		t.Errorf("Prune was called %d time(s) with kinds empty, want 0", prune)
	}
}

// TestDockerAction_PruneRun_SelectedKindsOnly proves deps.Prune receives
// exactly the true-valued kinds, in the documented order, and the result is
// returned as Patch (the prune screen has no table to Invalidate).
func TestDockerAction_PruneRun_SelectedKindsOnly(t *testing.T) {
	backend := newFakeDockerActionsBackend()
	deps := backend.deps()
	admin, _ := testDockerViewers()

	handler := handleDockerPruneRun(deps)
	body, _ := json.Marshal(dockerPruneInput{Images: true, Volumes: true})
	result, err := handler(context.Background(), admin, nil, body)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if result.Patch == nil || len(result.Invalidate) != 0 {
		t.Errorf("result = %+v, want only Patch", result)
	}
	if valErr := result.Validate(); valErr != nil {
		t.Errorf("ActionResult.Validate: %v", valErr)
	}

	backend.mu.Lock()
	kinds := append([]string(nil), backend.lastPruneKinds...)
	backend.mu.Unlock()
	want := []string{"images", "volumes"}
	if len(kinds) != len(want) {
		t.Fatalf("kinds = %v, want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Errorf("kinds[%d] = %q, want %q", i, kinds[i], want[i])
		}
	}

	log := backend.auditLog()
	if len(log) != 1 || log[0].action != "docker.prune.run" || log[0].target != "images,volumes" {
		t.Errorf("audit log = %v, want 1 docker.prune.run entry target=images,volumes", log)
	}
}

// --- Test 1 (destructive gate) + Test 3 (RBAC parity) --------------------

// registerDockerActionsForTest registers the real docker.* actions in this
// test binary's global sdui action registry EXACTLY once — RegisterAction
// panics on a duplicate ActionID, and only the tests below need the real
// registry (to exercise RunAction's confirmation/typed-confirmation gates
// and the authorize step end to end); every other test in this file calls
// the handler-producing functions directly, bypassing the registry.
var (
	registerDockerActionsTestOnce sync.Once
	registerDockerActionsTestDeps *fakeDockerActionsBackend
)

func registerDockerActionsForTest() *fakeDockerActionsBackend {
	registerDockerActionsTestOnce.Do(func() {
		registerDockerActionsTestDeps = newFakeDockerActionsBackend()
		registerDockerActions(registerDockerActionsTestDeps.deps())
	})
	return registerDockerActionsTestDeps
}

// TestDockerAction_DestructiveActionsRequireConfirmation proves
// container.remove/image.remove/compose.down/prune.run are unreachable
// without confirmation: an unconfirmed RunAction call returns a
// ConfirmationFieldKey FieldErrors WITHOUT ever calling the domain closure.
// prune.run additionally requires the exact typed string "PRUNE" even after
// Confirmed:true.
func TestDockerAction_DestructiveActionsRequireConfirmation(t *testing.T) {
	backend := registerDockerActionsForTest()
	admin, _ := testDockerViewers()

	cases := []struct {
		actionID string
		params   map[string]string
		input    json.RawMessage
	}{
		{dockerActionContainerRemove, map[string]string{"id": "c1"}, nil},
		{dockerActionImageRemove, map[string]string{"id": "img1"}, nil},
		{dockerActionComposeDown, map[string]string{"id": "myapp"}, nil},
		{dockerActionPruneRun, nil, mustJSONBytes(t, dockerPruneInput{Containers: true})},
	}
	for _, c := range cases {
		_, err := sdui.RunAction(context.Background(), c.actionID, admin, c.params, c.input, sdui.Confirmation{Confirmed: false})
		var fe sdui.FieldErrors
		if !errors.As(err, &fe) {
			t.Fatalf("%s without confirmation: err = %v (%T), want FieldErrors", c.actionID, err, err)
		}
		if _, ok := fe[sdui.ConfirmationFieldKey]; !ok {
			t.Errorf("%s without confirmation: FieldErrors does not have %q: %v", c.actionID, sdui.ConfirmationFieldKey, fe)
		}
	}

	start, stop, restart, removeC, removeI, up, down, prune := backend.counts()
	if start+stop+restart != 0 {
		t.Errorf("lifecycle should not have been called without confirmation: start=%d stop=%d restart=%d", start, stop, restart)
	}
	if removeC != 0 || removeI != 0 || down != 0 || prune != 0 {
		t.Errorf("destructive mutation was reached without confirmation: removeContainer=%d removeImage=%d composeDown=%d prune=%d", removeC, removeI, down, prune)
	}
	_ = up

	// prune.run: confirmed=true but wrong/missing typed confirmation is still
	// rejected, with no call to deps.Prune.
	_, err := sdui.RunAction(context.Background(), dockerActionPruneRun, admin, nil,
		mustJSONBytes(t, dockerPruneInput{Containers: true}), sdui.Confirmation{Confirmed: true, Typed: "not-prune"})
	var fe sdui.FieldErrors
	if !errors.As(err, &fe) {
		t.Fatalf("prune.run with the wrong typed text: err = %v, want FieldErrors", err)
	}
	if _, _, _, _, _, _, _, pruneAfter := backend.counts(); pruneAfter != 0 {
		t.Errorf("Prune was called with the wrong text confirmation, want 0 calls")
	}

	// Now confirm properly and prove the handler actually runs.
	result, err := sdui.RunAction(context.Background(), dockerActionPruneRun, admin, nil,
		mustJSONBytes(t, dockerPruneInput{Containers: true}), sdui.Confirmation{Confirmed: true, Typed: "PRUNE"})
	if err != nil {
		t.Fatalf("prune.run confirmed correctly: err = %v", err)
	}
	if result.Patch == nil {
		t.Errorf("result = %+v, want Patch filled in", result)
	}
	if _, _, _, _, _, _, _, pruneAfter := backend.counts(); pruneAfter != 1 {
		t.Errorf("Prune was called %d time(s) after a correct confirmation, want 1", pruneAfter)
	}
}

// TestDockerAction_NoVolumeOrNetworkRemoveIsRegistered proves this package
// never registers a single-item delete for volumes/networks anywhere —
// invoking a hypothetical "docker.volume.remove"/"docker.network.remove" id
// gets the same ErrActionNotFound as any other unknown action id, because no
// such id was ever passed to RegisterAction.
func TestDockerAction_NoVolumeOrNetworkRemoveIsRegistered(t *testing.T) {
	registerDockerActionsForTest()
	admin, _ := testDockerViewers()

	for _, id := range []string{"docker.volume.remove", "docker.network.remove"} {
		_, err := sdui.RunAction(context.Background(), id, admin, map[string]string{"id": "vol1"}, nil, sdui.Confirmation{Confirmed: true})
		if !errors.Is(err, sdui.ErrActionNotFound) {
			t.Errorf("%s: err = %v, want sdui.ErrActionNotFound (action should not exist)", id, err)
		}
	}
}

// TestDockerAction_NonAdminDestructiveActionsNotFound proves a non-admin
// invoking any destructive Docker action directly — even with a fully
// confirmed request — gets sdui.ErrActionNotFound at RunAction's authorize
// step, never reaching the confirmation gate or the handler.
func TestDockerAction_NonAdminDestructiveActionsNotFound(t *testing.T) {
	backend := registerDockerActionsForTest()
	_, nonAdmin := testDockerViewers()

	// backend is shared (sync.Once) across every test in this file that calls
	// registerDockerActionsForTest, so counts are compared as a delta against
	// this baseline, never an absolute value.
	_, _, _, baseRemoveC, baseRemoveI, _, baseDown, basePrune := backend.counts()

	cases := []struct {
		actionID string
		params   map[string]string
		input    json.RawMessage
		typed    string
	}{
		{dockerActionContainerRemove, map[string]string{"id": "c1"}, nil, ""},
		{dockerActionImageRemove, map[string]string{"id": "img1"}, nil, ""},
		{dockerActionComposeDown, map[string]string{"id": "myapp"}, nil, ""},
		{dockerActionPruneRun, nil, mustJSONBytes(t, dockerPruneInput{Containers: true}), "PRUNE"},
	}
	for _, c := range cases {
		_, err := sdui.RunAction(context.Background(), c.actionID, nonAdmin, c.params, c.input,
			sdui.Confirmation{Confirmed: true, Typed: c.typed})
		if !errors.Is(err, sdui.ErrActionNotFound) {
			t.Errorf("%s non-admin: err = %v, want sdui.ErrActionNotFound", c.actionID, err)
		}
	}

	_, _, _, removeC, removeI, _, down, prune := backend.counts()
	if removeC != baseRemoveC || removeI != baseRemoveI || down != baseDown || prune != basePrune {
		t.Errorf("destructive mutation was reached by a non-admin: removeContainer=%d(base %d) removeImage=%d(base %d) composeDown=%d(base %d) prune=%d(base %d)",
			removeC, baseRemoveC, removeI, baseRemoveI, down, baseDown, prune, basePrune)
	}
}

func mustJSONBytes(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}
