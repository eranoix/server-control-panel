package screens

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"server-control-panel/internal/metrics"
	"server-control-panel/internal/mobilebff/sdui"
	"server-control-panel/internal/procs"
	"server-control-panel/internal/sysextra"
)

// fakeSystemActionsBackend is an in-memory stand-in for SystemDeps' mutating
// closures, plus spy counters — mirrors fakeDockerActionsBackend
// (docker_actions_test.go).
type fakeSystemActionsBackend struct {
	mu sync.Mutex

	killCalls       int
	lastKilledPID   int32
	unitActionCalls map[string]int
	lastUnit        string

	audit []systemAuditRecord
}

type systemAuditRecord struct{ user, action, target string }

func newFakeSystemActionsBackend() *fakeSystemActionsBackend {
	return &fakeSystemActionsBackend{unitActionCalls: map[string]int{}}
}

func (b *fakeSystemActionsBackend) deps() SystemDeps {
	return SystemDeps{
		ListHistory: func() []metrics.Point { return nil },
		ListProcesses: func(context.Context) ([]procs.Info, error) {
			return nil, nil
		},
		KillProcess: func(_ context.Context, pid int32) error {
			b.mu.Lock()
			defer b.mu.Unlock()
			b.killCalls++
			b.lastKilledPID = pid
			return nil
		},
		ListPorts: func() ([]sysextra.Port, error) { return nil, nil },
		ListUnits: func() ([]sysextra.Unit, error) { return nil, nil },
		UnitAction: func(_ context.Context, unit, action string) (string, error) {
			b.mu.Lock()
			defer b.mu.Unlock()
			b.unitActionCalls[action]++
			b.lastUnit = unit
			return "ok", nil
		},
		AuditEvent: func(user, action, target string) {
			b.mu.Lock()
			defer b.mu.Unlock()
			b.audit = append(b.audit, systemAuditRecord{user: user, action: action, target: target})
		},
	}
}

func (b *fakeSystemActionsBackend) killCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.killCalls
}

func (b *fakeSystemActionsBackend) unitActionCount(action string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.unitActionCalls[action]
}

func (b *fakeSystemActionsBackend) auditLog() []systemAuditRecord {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]systemAuditRecord, len(b.audit))
	copy(out, b.audit)
	return out
}

// --- Test 2: non-destructive round trip ---------------------------------

// TestSystemAction_UnitActions_CallCorrectVerbAndInvalidate proves each of
// the five unit actions calls deps.UnitAction with the matching action
// string, reads params["id"] as the unit name, and returns Invalidate on the
// systemd table.
func TestSystemAction_UnitActions_CallCorrectVerbAndInvalidate(t *testing.T) {
	backend := newFakeSystemActionsBackend()
	deps := backend.deps()
	admin, _ := testSystemViewers()

	cases := []string{"start", "stop", "restart", "enable", "disable"}
	for _, action := range cases {
		handler := handleSystemUnitAction(deps, action)
		result, err := handler(context.Background(), admin, map[string]string{"id": "nginx.service"}, nil)
		if err != nil {
			t.Fatalf("%s: %v", action, err)
		}
		if valErr := result.Validate(); valErr != nil {
			t.Errorf("%s: ActionResult.Validate: %v", action, valErr)
		}
		if len(result.Invalidate) == 0 || result.Invalidate[0] != "systemd-table" {
			t.Errorf("%s: Invalidate = %v, want [\"systemd-table\"]", action, result.Invalidate)
		}
		if got := backend.unitActionCount(action); got != 1 {
			t.Errorf("%s: UnitAction called %d time(s), want 1", action, got)
		}
	}

	log := backend.auditLog()
	if len(log) != len(cases) {
		t.Fatalf("audit log has %d entry(ies), want %d: %v", len(log), len(cases), log)
	}
	for i, action := range cases {
		want := "system.unit." + action
		if log[i].action != want {
			t.Errorf("audit[%d].action = %q, want %q", i, log[i].action, want)
		}
	}
}

// TestSystemAction_UnitAction_EmptyIDIsNotFound proves an empty unit name
// never reaches deps.UnitAction.
func TestSystemAction_UnitAction_EmptyIDIsNotFound(t *testing.T) {
	backend := newFakeSystemActionsBackend()
	deps := backend.deps()
	admin, _ := testSystemViewers()

	handler := handleSystemUnitAction(deps, "restart")
	_, err := handler(context.Background(), admin, map[string]string{}, nil)
	if !errors.Is(err, sdui.ErrActionNotFound) {
		t.Errorf("err = %v, want sdui.ErrActionNotFound", err)
	}
	if got := backend.unitActionCount("restart"); got != 0 {
		t.Errorf("UnitAction was called %d time(s), want 0", got)
	}
}

// TestSystemAction_MetricsWindow_ValidWindowSetsPreferenceAndInvalidatesCharts
// proves a valid window is written to the per-username preference store and
// the response invalidates all three chart components. Uses a unique
// username (never "golden-admin"/"golden-user"/"sys-admin"/"sys-user") per
// systemMetricsWindowMu's doc comment, so this test's write can never leak
// into another test's or the golden corpus' assertions.
func TestSystemAction_MetricsWindow_ValidWindowSetsPreferenceAndInvalidatesCharts(t *testing.T) {
	backend := newFakeSystemActionsBackend()
	deps := backend.deps()
	v := sdui.ViewerFrom(testSystemCfg(), "sys-window-test-valid")

	handler := handleSystemMetricsWindow(deps)
	body, _ := json.Marshal(systemMetricsWindowInput{Window: "1h"})
	result, err := handler(context.Background(), v, nil, body)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if valErr := result.Validate(); valErr != nil {
		t.Errorf("ActionResult.Validate: %v", valErr)
	}
	wantInvalidate := []string{"cpu-chart", "mem-chart", "disk-chart"}
	if len(result.Invalidate) != len(wantInvalidate) {
		t.Fatalf("Invalidate = %v, want %v", result.Invalidate, wantInvalidate)
	}
	for i, id := range wantInvalidate {
		if result.Invalidate[i] != id {
			t.Errorf("Invalidate[%d] = %q, want %q", i, result.Invalidate[i], id)
		}
	}

	if got := systemMetricsWindowFor("sys-window-test-valid"); got != "1h" {
		t.Errorf("systemMetricsWindowFor after set = %q, want \"1h\"", got)
	}

	log := backend.auditLog()
	if len(log) != 1 || log[0].action != "system.metrics.window" || log[0].target != "1h" {
		t.Errorf("audit log = %v, want 1 system.metrics.window entry target=1h", log)
	}
}

// TestSystemAction_MetricsWindow_InvalidWindowIsFieldError proves an
// out-of-set window value is rejected without touching the preference
// store.
func TestSystemAction_MetricsWindow_InvalidWindowIsFieldError(t *testing.T) {
	backend := newFakeSystemActionsBackend()
	deps := backend.deps()
	v := sdui.ViewerFrom(testSystemCfg(), "sys-window-test-invalid")

	handler := handleSystemMetricsWindow(deps)
	body, _ := json.Marshal(systemMetricsWindowInput{Window: "3h"})
	_, err := handler(context.Background(), v, nil, body)

	var fe sdui.FieldErrors
	if !errors.As(err, &fe) {
		t.Fatalf("err = %v (%T), want sdui.FieldErrors", err, err)
	}
	if _, ok := fe["window"]; !ok {
		t.Errorf("FieldErrors does not have the \"window\" key: %v", fe)
	}
	if got := systemMetricsWindowFor("sys-window-test-invalid"); got != defaultSystemMetricsWindow {
		t.Errorf("systemMetricsWindowFor after an invalid window = %q, want the default %q (should not have been written)", got, defaultSystemMetricsWindow)
	}
	if len(backend.auditLog()) != 0 {
		t.Errorf("AuditEvent should not have been called for an invalid window")
	}
}

// --- Test 1 (destructive gate) + Test 3 (RBAC parity) --------------------

// registerSystemActionsForTest registers the real system.* actions in this
// test binary's global sdui action registry EXACTLY once — mirrors
// registerDockerActionsForTest.
var (
	registerSystemActionsTestOnce sync.Once
	registerSystemActionsTestDeps *fakeSystemActionsBackend
)

func registerSystemActionsForTest() *fakeSystemActionsBackend {
	registerSystemActionsTestOnce.Do(func() {
		registerSystemActionsTestDeps = newFakeSystemActionsBackend()
		registerSystemActions(registerSystemActionsTestDeps.deps())
	})
	return registerSystemActionsTestDeps
}

// TestSystemAction_ProcessKillRequiresConfirmation proves
// system.process.kill is unreachable without confirmation: an unconfirmed
// RunAction call returns a ConfirmationFieldKey FieldErrors WITHOUT ever
// calling deps.KillProcess.
func TestSystemAction_ProcessKillRequiresConfirmation(t *testing.T) {
	backend := registerSystemActionsForTest()
	admin, _ := testSystemViewers()
	baseKills := backend.killCount()

	_, err := sdui.RunAction(context.Background(), systemActionProcessKill, admin,
		map[string]string{"id": "4242"}, nil, sdui.Confirmation{Confirmed: false})
	var fe sdui.FieldErrors
	if !errors.As(err, &fe) {
		t.Fatalf("without confirmation: err = %v (%T), want FieldErrors", err, err)
	}
	if _, ok := fe[sdui.ConfirmationFieldKey]; !ok {
		t.Errorf("without confirmation: FieldErrors does not have %q: %v", sdui.ConfirmationFieldKey, fe)
	}
	if got := backend.killCount(); got != baseKills {
		t.Errorf("KillProcess was called without confirmation: %d (base %d)", got, baseKills)
	}

	result, err := sdui.RunAction(context.Background(), systemActionProcessKill, admin,
		map[string]string{"id": "4242"}, nil, sdui.Confirmation{Confirmed: true})
	if err != nil {
		t.Fatalf("confirmed: err = %v", err)
	}
	if len(result.Invalidate) == 0 {
		t.Errorf("result = %+v, want Invalidate filled in", result)
	}
	if got := backend.killCount(); got != baseKills+1 {
		t.Errorf("KillProcess was called %d time(s) after confirmation, want %d", got, baseKills+1)
	}
}

// TestSystemAction_NonAdminDestructiveAndUnitActionsNotFound proves a
// non-admin invoking kill or any of the five unit actions directly — even
// fully confirmed — gets sdui.ErrActionNotFound at RunAction's authorize
// step, never reaching the handler. This is the golden-harness-cannot-
// express-ownership limitation from PLAN.md's Blocker 2: kill stays
// admin-only for every viewer, there is no "kill only your own process"
// carve-out.
func TestSystemAction_NonAdminDestructiveAndUnitActionsNotFound(t *testing.T) {
	backend := registerSystemActionsForTest()
	_, nonAdmin := testSystemViewers()

	baseKills := backend.killCount()
	baseUnitCounts := map[string]int{}
	unitActions := []string{
		systemActionUnitStart, systemActionUnitStop, systemActionUnitRestart,
		systemActionUnitEnable, systemActionUnitDisable,
	}
	for _, a := range unitActions {
		verb := a[len("system.unit."):]
		baseUnitCounts[verb] = backend.unitActionCount(verb)
	}

	_, err := sdui.RunAction(context.Background(), systemActionProcessKill, nonAdmin,
		map[string]string{"id": "4242"}, nil, sdui.Confirmation{Confirmed: true})
	if !errors.Is(err, sdui.ErrActionNotFound) {
		t.Errorf("system.process.kill non-admin: err = %v, want sdui.ErrActionNotFound", err)
	}
	if got := backend.killCount(); got != baseKills {
		t.Errorf("KillProcess was reached by non-admin: %d (base %d)", got, baseKills)
	}

	for _, a := range unitActions {
		_, err := sdui.RunAction(context.Background(), a, nonAdmin,
			map[string]string{"id": "nginx.service"}, nil, sdui.Confirmation{Confirmed: true})
		if !errors.Is(err, sdui.ErrActionNotFound) {
			t.Errorf("%s non-admin: err = %v, want sdui.ErrActionNotFound", a, err)
		}
	}
	for _, a := range unitActions {
		verb := a[len("system.unit."):]
		if got := backend.unitActionCount(verb); got != baseUnitCounts[verb] {
			t.Errorf("%s was reached by non-admin: %d (base %d)", a, got, baseUnitCounts[verb])
		}
	}
}

// TestSystemAction_MetricsWindow_ReachableByNonAdmin proves
// system.metrics.window is open to any authenticated viewer, not admin-only
// — reading/adjusting one's own metrics window has no destructive or
// privileged aspect. Uses a unique username (never the shared "sys-user"
// non-admin identity other tests in this file read/assert against) per
// systemMetricsWindowMu's test-isolation rule.
func TestSystemAction_MetricsWindow_ReachableByNonAdmin(t *testing.T) {
	registerSystemActionsForTest()
	nonAdmin := sdui.ViewerFrom(testSystemCfg(), "sys-window-test-nonadmin")

	body, _ := json.Marshal(systemMetricsWindowInput{Window: "2h"})
	_, err := sdui.RunAction(context.Background(), systemActionMetricsWindow, nonAdmin, nil, body, sdui.Confirmation{})
	if err != nil {
		t.Fatalf("non-admin: err = %v, want nil", err)
	}
	if got := systemMetricsWindowFor(nonAdmin.Username); got != "2h" {
		t.Errorf("systemMetricsWindowFor(%q) = %q, want \"2h\"", nonAdmin.Username, got)
	}
}
