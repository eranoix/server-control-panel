package mobilebff

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

func useStubDeployCommand(t *testing.T, exitCode int) {
	t.Helper()
	script := "#!/bin/sh\necho stub-line-1\necho stub-line-2\necho stub-line-3\nexit " + strconv.Itoa(exitCode) + "\n"
	path := filepath.Join(t.TempDir(), "deploy")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(queue.DeployCommandEnv, path)
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

func TestOpsDeploy_MissingConfirm_400(t *testing.T) {
	useStubDeployCommand(t, 0)
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

func TestOpsDeploy_NonAdmin_403(t *testing.T) {
	useStubDeployCommand(t, 0)
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

func TestOpsDeploy_Success_EnqueuesAndBridgesLiveEvents(t *testing.T) {
	useStubDeployCommand(t, 0)
	q := newTestOpsQueue(t)
	hub := NewHub()
	authSvc := auth.New("test-secret", nil)

	mux := http.NewServeMux()
	Mount(mux, Deps{Cfg: adminCfg(), Queue: q, Hub: hub})
	mux.HandleFunc("/ws/mobile-events", HandleMobileEventsWS(authSvc, hub))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

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

	statusReq := httptest.NewRequest(http.MethodGet, "/api/mobile/v1/ops/deploy/"+out.JobID, nil)
	statusReq = statusReq.WithContext(auth.WithUser(statusReq.Context(), testPrimary))
	statusRec := httptest.NewRecorder()
	mux.ServeHTTP(statusRec, statusReq)
	if statusRec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200 (body=%s)", statusRec.Code, statusRec.Body.String())
	}

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
		t.Error("expected at least one log-type event forwarded from the stub deploy command's output")
	}
	_ = conn.Close()
}

func TestOpsDeploy_RunnerFailure_ReflectsInStatus(t *testing.T) {
	useStubDeployCommand(t, 1)
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

func TestOpsDeployStatus_NonAdmin_403(t *testing.T) {
	useStubDeployCommand(t, 0)
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
