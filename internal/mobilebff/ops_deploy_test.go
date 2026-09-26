package mobilebff

// ops_deploy_test.go proves the two server-side deploy gates and the
// live deploy.<jobID> bridge, WITHOUT ever invoking the real `agentctl
// deploy` — every test here runs against a stub `agentctl` on PATH (same
// technique as runners_selfdeploy_test.go), never the real pipeline.
import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/config"
	"server-control-panel/internal/queue"
)

// writeStubAgentctl mirrors internal/queue/runners_selfdeploy_test.go's
// helper (unexported there, so duplicated here rather than exported just for
// a test import) — a fake `agentctl` that prints deterministic lines and
// exits with exitCode, standing in for the real deploy pipeline.
func writeStubAgentctl(t *testing.T, exitCode int) string {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\necho stub-line-1\necho stub-line-2\necho stub-line-3\nexit " + strconv.Itoa(exitCode) + "\n"
	path := filepath.Join(dir, "agentctl")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func newTestOpsQueue(t *testing.T) *queue.Queue {
	t.Helper()
	q, err := queue.NewQueue(queue.Options{DataDir: t.TempDir(), Workers: 2, MaxKeep: 50})
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}
	q.Register(queue.SelfDeployRunner{})
	t.Cleanup(func() { q.Shutdown(context.Background()) })
	return q
}

const testPrimary = "sam"

func adminCfg() *config.Config { return &config.Config{Primary: testPrimary} }

// TestOpsDeploy_MissingConfirm_400 is the server-side confirmation gate:
// a client with NO confirmation dialog — one that never sends confirm:true —
// must be refused even though it is the primary account. Both an absent
// body field and a literal confirm:false take this path.
func TestOpsDeploy_MissingConfirm_400(t *testing.T) {
	dir := writeStubAgentctl(t, 0)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	q := newTestOpsQueue(t)

	mux := http.NewServeMux()
	Mount(mux, Deps{Cfg: adminCfg(), Queue: q})

	for _, body := range []string{`{}`, `{"confirm":false}`} {
		req := httptest.NewRequest(http.MethodPost, "/api/mobile/v1/ops/deploy", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req = req.WithContext(auth.WithUser(req.Context(), testPrimary))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body=%s: status = %d, want 400 (body=%s)", body, rec.Code, rec.Body.String())
		}
	}
	if running, queued := q.Counts(); running != 0 || queued != 0 {
		t.Fatalf("expected nothing enqueued without confirm:true, got running=%d queued=%d", running, queued)
	}
}

// TestOpsDeploy_NonAdmin_403 is the primary-only gate: a non-admin user
// WITH confirm:true is still refused — confirmation never substitutes for
// the RBAC check.
func TestOpsDeploy_NonAdmin_403(t *testing.T) {
	dir := writeStubAgentctl(t, 0)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	q := newTestOpsQueue(t)

	mux := http.NewServeMux()
	Mount(mux, Deps{Cfg: adminCfg(), Queue: q})

	req := httptest.NewRequest(http.MethodPost, "/api/mobile/v1/ops/deploy", strings.NewReader(`{"confirm":true}`))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(auth.WithUser(req.Context(), "someone-else"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body=%s)", rec.Code, rec.Body.String())
	}
	if running, queued := q.Counts(); running != 0 || queued != 0 {
		t.Fatalf("expected nothing enqueued for a non-admin caller, got running=%d queued=%d", running, queued)
	}
}

// TestOpsDeploy_QueueUnavailable_503 proves the endpoint fails closed (never
// panics) when the queue subsystem never started.
func TestOpsDeploy_QueueUnavailable_503(t *testing.T) {
	mux := http.NewServeMux()
	Mount(mux, Deps{Cfg: adminCfg()})

	req := httptest.NewRequest(http.MethodPost, "/api/mobile/v1/ops/deploy", strings.NewReader(`{"confirm":true}`))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(auth.WithUser(req.Context(), testPrimary))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body=%s)", rec.Code, rec.Body.String())
	}
}

// TestOpsDeploy_Success_EnqueuesAndBridgesLiveEvents is Task 2's literal
// <done> criterion: primary + confirm:true returns a job_id, and a
// subsequent /ws/mobile-events subscription to deploy.<jobID> receives
// progress/log events ending in a distinct terminal event. The stub
// `agentctl` on PATH stands in for the real pipeline — this test never
// deploys anything.
func TestOpsDeploy_Success_EnqueuesAndBridgesLiveEvents(t *testing.T) {
	dir := writeStubAgentctl(t, 0)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	q := newTestOpsQueue(t)
	hub := NewHub()
	authSvc := auth.New("test-secret", nil)

	mux := http.NewServeMux()
	Mount(mux, Deps{Cfg: adminCfg(), Queue: q, Hub: hub})
	mux.HandleFunc("/ws/mobile-events", HandleMobileEventsWS(authSvc, hub))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	// Trigger the deploy in-process (matches the other REST tests here); the
	// Hub instance is shared with the httptest.Server above, so
	// bridgeSelfDeployJob's Publish calls reach a WS client dialed against it.
	req := httptest.NewRequest(http.MethodPost, "/api/mobile/v1/ops/deploy", strings.NewReader(`{"confirm":true}`))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(auth.WithUser(req.Context(), testPrimary))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var out struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, rec.Body.String())
	}
	if out.JobID == "" {
		t.Fatal("empty job_id")
	}

	baseURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, rd := dialWithTicket(t, baseURL, testPrimary)
	channel := "deploy." + out.JobID
	if err := conn.WriteJSON(controlFrame{Op: "subscribe", Channel: channel}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	ack := rd.next(t, time.Second)
	if ack["op"] != "subscribed" || ack["channel"] != channel {
		t.Fatalf("ack = %#v", ack)
	}

	// GET /ops/deploy/{jobID} gives the initial paint — assert it resolves to
	// a real, known job of the right kind (T-06's "screen paints before the
	// WS subscription catches up" contract), independent of the live stream.
	statusReq := httptest.NewRequest(http.MethodGet, "/api/mobile/v1/ops/deploy/"+out.JobID, nil)
	statusReq = statusReq.WithContext(auth.WithUser(statusReq.Context(), testPrimary))
	statusRec := httptest.NewRecorder()
	mux.ServeHTTP(statusRec, statusReq)
	if statusRec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200 (body=%s)", statusRec.Code, statusRec.Body.String())
	}

	// Drain live events on the deploy.<jobID> channel until the terminal
	// "status" event with status=done arrives — proving a DISTINCT terminal
	// message closes the stream, not just silence.
	deadline := time.Now().Add(5 * time.Second)
	sawLog := false
	terminalSeen := false
	for time.Now().Before(deadline) && !terminalSeen {
		ev := rd.next(t, 5*time.Second)
		if ev["channel"] != channel {
			t.Fatalf("event on unexpected channel: %#v", ev)
		}
		switch ev["type"] {
		case "log":
			sawLog = true
		case "status":
			data, _ := ev["data"].(map[string]any)
			if data != nil && data["status"] == "done" {
				terminalSeen = true
			}
		}
	}
	if !terminalSeen {
		t.Fatal("never saw the terminal deploy.<jobID> status=done event")
	}
	if !sawLog {
		t.Error("expected at least one log-type event forwarded from the stub agentctl's output")
	}
	_ = conn.Close()
}

// TestOpsDeploy_RunnerFailure_ReflectsInStatus proves a forced runner failure
// (stub exits non-zero) surfaces as a failed status with a non-empty Error
// via GET /ops/deploy/{jobID} — the terminal-job notification itself
// (job.failed dispatched to notify.Router) is generic, pre-existing
// plumbing wired once at the internal/api.Router level for EVERY job kind
// (see internal/api/notify_wire.go's initNotify/SetNotifier), not something
// self_deploy adds or this package can reach — mobilebff.Deps carries no
// notify.Router reference by design.
func TestOpsDeploy_RunnerFailure_ReflectsInStatus(t *testing.T) {
	dir := writeStubAgentctl(t, 1)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	q := newTestOpsQueue(t)

	mux := http.NewServeMux()
	Mount(mux, Deps{Cfg: adminCfg(), Queue: q})

	req := httptest.NewRequest(http.MethodPost, "/api/mobile/v1/ops/deploy", strings.NewReader(`{"confirm":true}`))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(auth.WithUser(req.Context(), testPrimary))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var out struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		statusReq := httptest.NewRequest(http.MethodGet, "/api/mobile/v1/ops/deploy/"+out.JobID, nil)
		statusReq = statusReq.WithContext(auth.WithUser(statusReq.Context(), testPrimary))
		statusRec := httptest.NewRecorder()
		mux.ServeHTTP(statusRec, statusReq)
		var body struct {
			Status string `json:"status"`
			Error  string `json:"error"`
		}
		if err := json.Unmarshal(statusRec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Status == "failed" {
			if body.Error == "" {
				t.Fatal("expected non-empty error on a failed self_deploy job")
			}
			if !strings.Contains(body.Error, "stub-line-3") {
				t.Errorf("expected failure summary to include the stub's last output, got: %s", body.Error)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("job never reached failed status")
}

// TestOpsDeployStatus_UnknownJob_404 and wrong-kind isolation: a job id from
// another kind must never be exposed via this endpoint.
func TestOpsDeployStatus_UnknownJob_404(t *testing.T) {
	q := newTestOpsQueue(t)
	mux := http.NewServeMux()
	Mount(mux, Deps{Cfg: adminCfg(), Queue: q})

	req := httptest.NewRequest(http.MethodGet, "/api/mobile/v1/ops/deploy/nope", nil)
	req = req.WithContext(auth.WithUser(req.Context(), testPrimary))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body=%s)", rec.Code, rec.Body.String())
	}
}

// TestOpsDeployStatus_NonAdmin_403 mirrors the trigger endpoint's RBAC gate
// on the status read path — a non-primary caller must not learn anything
// about a deploy job, not even that it exists.
func TestOpsDeployStatus_NonAdmin_403(t *testing.T) {
	dir := writeStubAgentctl(t, 0)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	q := newTestOpsQueue(t)
	j, err := q.Enqueue("self_deploy", nil, testPrimary, "mobile")
	if err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	Mount(mux, Deps{Cfg: adminCfg(), Queue: q})

	req := httptest.NewRequest(http.MethodGet, "/api/mobile/v1/ops/deploy/"+j.ID, nil)
	req = req.WithContext(auth.WithUser(req.Context(), "someone-else"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body=%s)", rec.Code, rec.Body.String())
	}
}
