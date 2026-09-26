package screens

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/mobilebff"
)

// newSchedulerRowsMux wires ONLY registerSchedulerRows onto a throwaway
// huma.API — never through screens.Register or mobilebff.Mount's global
// registrar list. Those two go through the process-global sdui.Register /
// sdui.RegisterAction registries (which panic on a second registration of
// the same id — see scheduler_actions_test.go's registerSchedulerActionsForTest),
// so a test file that ran them again here would collide with that file's
// Test 6. registerSchedulerRows itself has no such global state: it only
// registers onto the *local* huma.API instance passed to it, so this helper
// can be called freely, once per test.
func newSchedulerRowsMux(deps SchedulerDeps) *http.ServeMux {
	mux := http.NewServeMux()
	api := humago.NewWithPrefix(mux, mobilebff.Prefix, huma.DefaultConfig("scheduler-rows-test", "0"))
	registerSchedulerRows(api, deps, mobilebff.Deps{Cfg: testSchedulerCfg()})
	return mux
}

func TestSchedulerRows_Unauthenticated(t *testing.T) {
	mux := newSchedulerRowsMux(testSchedulerDeps())

	req := httptest.NewRequest(http.MethodGet, mobilebff.Prefix+"/scheduler/jobs", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (body=%s)", rec.Code, rec.Body.String())
	}
}

type schedulerRowsBody struct {
	Rows []map[string]any `json:"rows"`
}

func doSchedulerRowsRequest(t *testing.T, mux *http.ServeMux, username string) schedulerRowsBody {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, mobilebff.Prefix+"/scheduler/jobs", nil)
	req = req.WithContext(auth.WithUser(req.Context(), username))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var body schedulerRowsBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, rec.Body.String())
	}
	return body
}

// Test 1 (RBAC by omission, non-vacuity): an admin sees every job, including
// run_as_root of a job owned by someone else.
func TestSchedulerRows_AdminSeesEveryJobIncludingRunAsRoot(t *testing.T) {
	backend := newFakeSchedulerBackend()
	mux := newSchedulerRowsMux(backend.deps())

	body := doSchedulerRowsRequest(t, mux, "sched-admin")
	if len(body.Rows) != 4 {
		t.Fatalf("admin saw %d row(s), want 4 (all jobs): %+v", len(body.Rows), body.Rows)
	}

	var root map[string]any
	for _, r := range body.Rows {
		if r["id"] == "job-admin-root" {
			root = r
		}
	}
	if root == nil {
		t.Fatal("job-admin-root missing from admin's rows")
	}
	if v, ok := root["run_as_root"]; !ok || v != true {
		t.Errorf("admin's run_as_root = %v (present=%v), want true", v, ok)
	}
}

// Test 2 (RBAC by omission): a non-admin sees only their own jobs, and never
// the run_as_root key — not even in the response's raw bytes, not just in the
// decoded value.
func TestSchedulerRows_NonAdminSeesOnlyOwnJobsWithoutRunAsRoot(t *testing.T) {
	backend := newFakeSchedulerBackend()
	mux := newSchedulerRowsMux(backend.deps())

	req := httptest.NewRequest(http.MethodGet, mobilebff.Prefix+"/scheduler/jobs", nil)
	req = req.WithContext(auth.WithUser(req.Context(), "sched-user"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}

	rawBody := rec.Body.String()
	if strings.Contains(rawBody, "run_as_root") {
		t.Errorf("non-admin response contains \"run_as_root\" in the raw bytes: %s", rawBody)
	}
	if strings.Contains(rawBody, "job-admin-root") || strings.Contains(rawBody, "job-other-owned") {
		t.Errorf("non-admin response leaked another owner's job: %s", rawBody)
	}

	var body schedulerRowsBody
	if err := json.Unmarshal([]byte(rawBody), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// sched-user owns job-user-owned and job-orphaned-kind.
	if len(body.Rows) != 2 {
		t.Fatalf("non-admin saw %d row(s), want 2 (only their own): %+v", len(body.Rows), body.Rows)
	}
	for _, r := range body.Rows {
		if r["owner"] != "sched-user" {
			t.Errorf("another owner's row leaked: %v", r)
		}
	}
}

// Test 3 (wire format — pinned here, see registerSchedulerRows' comment in
// scheduler.go): {"rows":[{...}]}, one object per job with exactly the
// documented keys, timestamps already formatted as text (never a raw
// epoch).
func TestSchedulerRows_WireShape(t *testing.T) {
	backend := newFakeSchedulerBackend()
	mux := newSchedulerRowsMux(backend.deps())

	req := httptest.NewRequest(http.MethodGet, mobilebff.Prefix+"/scheduler/jobs", nil)
	req = req.WithContext(auth.WithUser(req.Context(), "sched-admin"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	var generic map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &generic); err != nil {
		t.Fatalf("generic decode: %v (body=%s)", err, rec.Body.String())
	}
	rowsRaw, ok := generic["rows"]
	if !ok {
		t.Fatalf("response does not have the \"rows\" key: %s", rec.Body.String())
	}
	rows, ok := rowsRaw.([]any)
	if !ok || len(rows) == 0 {
		t.Fatalf("\"rows\" is not a non-empty list: %v", rowsRaw)
	}

	var adminRoot map[string]any
	for _, raw := range rows {
		row, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("row is not an object: %v", raw)
		}
		if row["id"] == "job-admin-root" {
			adminRoot = row
		}
	}
	if adminRoot == nil {
		t.Fatal("job-admin-root ausente")
	}

	wantKeys := []string{"id", "name", "schedule", "kind", "enabled", "owner", "last_status", "last_fire", "next_fire", "run_as_root"}
	for _, k := range wantKeys {
		if _, ok := adminRoot[k]; !ok {
			t.Errorf("row does not have the key %q: %v", k, adminRoot)
		}
	}
	if _, isString := adminRoot["last_fire"].(string); !isString {
		t.Errorf("last_fire = %v (%T), want a pre-formatted string", adminRoot["last_fire"], adminRoot["last_fire"])
	}
	if _, isString := adminRoot["next_fire"].(string); !isString {
		t.Errorf("next_fire = %v (%T), want a pre-formatted string", adminRoot["next_fire"], adminRoot["next_fire"])
	}
}

// Test 4: a job with next_fire/last_fire zeroed (never fired) becomes an
// empty string, never "0" or null — the same rule as formatSchedulerTimestamp,
// already covered in isolation by TestFormatSchedulerTimestamp_ZeroIsEmpty.
func TestSchedulerRows_NeverFiredJobHasEmptyTimestamps(t *testing.T) {
	backend := newFakeSchedulerBackend()
	mux := newSchedulerRowsMux(backend.deps())

	body := doSchedulerRowsRequest(t, mux, "sched-user")
	var orphan map[string]any
	for _, r := range body.Rows {
		if r["id"] == "job-orphaned-kind" {
			orphan = r
		}
	}
	if orphan == nil {
		t.Fatal("job-orphaned-kind ausente")
	}
	// newFakeSchedulerBackend's fixture does not set LastFire/NextFire for
	// job-orphaned-kind — they must arrive as "".
	if orphan["last_fire"] != "" {
		t.Errorf("last_fire = %v, want \"\" (job never ran)", orphan["last_fire"])
	}
	if orphan["next_fire"] != "" {
		t.Errorf("next_fire = %v, want \"\" (job never ran)", orphan["next_fire"])
	}
}
