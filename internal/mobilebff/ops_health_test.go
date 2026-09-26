package mobilebff

// ops_health_test.go covers Task 3's <done> criteria: GET /ops/status
// admin-gated; the live "ops.health" channel publishes an OpsStatus-shaped
// envelope while at least one connection is subscribed, and does nothing
// (no wasted health-check/alert-scan work) while nobody is listening.
import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/metrics"
)

func fakeHealthDetailed(ok bool) func() (bool, map[string]string) {
	return func() (bool, map[string]string) {
		return ok, map[string]string{"config": "ok", "docker": "ok"}
	}
}

// firingAlertsEngine returns an Engine with one rule already firing, so
// buildOpsStatus's alert-scan has something non-empty to project.
func firingAlertsEngine(t *testing.T) *metrics.Engine {
	t.Helper()
	e := metrics.NewEngine()
	if err := e.AddRule(metrics.Rule{
		Name: "cpu-alto", Metric: "test.cpu", Op: ">", Threshold: 0, Severity: "critical",
	}); err != nil {
		t.Fatalf("AddRule: %v", err)
	}
	// Duration 0 + a value above threshold fires immediately on this single
	// Evaluate call (see internal/metrics/alerts.go's edge-triggered logic).
	e.Evaluate(metrics.Snapshot{T: time.Now().Unix(), Values: map[string]float64{"test.cpu": 1}})
	return e
}

func TestOpsStatus_Admin_200(t *testing.T) {
	q := newTestOpsQueue(t)
	deps := Deps{
		Cfg:            adminCfg(),
		Queue:          q,
		Alerts:         firingAlertsEngine(t),
		HealthDetailed: fakeHealthDetailed(true),
	}
	mux := http.NewServeMux()
	Mount(mux, deps)

	req := httptest.NewRequest(http.MethodGet, "/api/mobile/v1/ops/status", nil)
	req = req.WithContext(auth.WithUser(req.Context(), testPrimary))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var body OpsStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, rec.Body.String())
	}
	if !body.HealthOK {
		t.Error("HealthOK = false, want true")
	}
	if body.Health["config"] != "ok" {
		t.Errorf("Health[config] = %q, want ok", body.Health["config"])
	}
	if len(body.Alerts) != 1 || body.Alerts[0].Name != "cpu-alto" {
		t.Errorf("Alerts = %#v, want one firing rule cpu-alto", body.Alerts)
	}
}

func TestOpsStatus_NonAdmin_403(t *testing.T) {
	mux := http.NewServeMux()
	Mount(mux, Deps{Cfg: adminCfg()})

	req := httptest.NewRequest(http.MethodGet, "/api/mobile/v1/ops/status", nil)
	req = req.WithContext(auth.WithUser(req.Context(), "someone-else"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body=%s)", rec.Code, rec.Body.String())
	}
}

func TestOpsStatus_Unauthenticated_401(t *testing.T) {
	mux := http.NewServeMux()
	Mount(mux, Deps{Cfg: adminCfg()})

	req := httptest.NewRequest(http.MethodGet, "/api/mobile/v1/ops/status", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (body=%s)", rec.Code, rec.Body.String())
	}
}

// TestOpsHealthPublisher_NoSubscriber_NoPublish proves the ticker skips
// computing/publishing OpsStatus entirely when nobody is subscribed to
// "ops.health" — the T-06 "wasted resources at scale" guard.
func TestOpsHealthPublisher_NoSubscriber_NoPublish(t *testing.T) {
	old := opsHealthTickInterval
	opsHealthTickInterval = 20 * time.Millisecond
	t.Cleanup(func() { opsHealthTickInterval = old })

	hub := NewHub()
	deps := Deps{Cfg: adminCfg(), HealthDetailed: fakeHealthDetailed(true)}
	deps.Hub = hub
	stop := StartOpsHealthPublisher(deps)
	t.Cleanup(stop)

	authSvc := auth.New("test-secret", nil)
	mux := http.NewServeMux()
	mux.HandleFunc("/ws/mobile-events", HandleMobileEventsWS(authSvc, hub))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	baseURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	// Connect WITHOUT subscribing to ops.health — several ticks pass with
	// nobody listening on that channel.
	conn, rd := dialWithTicket(t, baseURL, testPrimary)
	if err := conn.WriteJSON(controlFrame{Op: "subscribe", Channel: "unrelated"}); err != nil {
		t.Fatalf("subscribe unrelated: %v", err)
	}
	rd.next(t, time.Second) // ack
	rd.expectNone(t, 150*time.Millisecond)
	_ = conn.Close()
}

// TestOpsHealthPublisher_PublishesWhileSubscribed is Task 3's live-channel
// <done> criterion: a connection subscribed to "ops.health" receives at
// least one OpsStatus-shaped envelope within the tick interval.
func TestOpsHealthPublisher_PublishesWhileSubscribed(t *testing.T) {
	old := opsHealthTickInterval
	opsHealthTickInterval = 20 * time.Millisecond
	t.Cleanup(func() { opsHealthTickInterval = old })

	q := newTestOpsQueue(t)
	hub := NewHub()
	deps := Deps{
		Cfg:            adminCfg(),
		Queue:          q,
		Alerts:         firingAlertsEngine(t),
		HealthDetailed: fakeHealthDetailed(false),
	}
	deps.Hub = hub
	stop := StartOpsHealthPublisher(deps)
	t.Cleanup(stop)

	authSvc := auth.New("test-secret", nil)
	mux := http.NewServeMux()
	mux.HandleFunc("/ws/mobile-events", HandleMobileEventsWS(authSvc, hub))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	baseURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	conn, rd := dialWithTicket(t, baseURL, testPrimary)
	if err := conn.WriteJSON(controlFrame{Op: "subscribe", Channel: "ops.health"}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	rd.next(t, time.Second) // ack

	ev := rd.next(t, 2*time.Second)
	if ev["channel"] != "ops.health" || ev["type"] != "ops.status" {
		t.Fatalf("event = %#v, want channel=ops.health type=ops.status", ev)
	}
	data, _ := ev["data"].(map[string]any)
	if data == nil {
		t.Fatal("event data is nil")
	}
	if data["health_ok"] != false {
		t.Errorf("health_ok = %#v, want false", data["health_ok"])
	}
	alerts, _ := data["alerts"].([]any)
	if len(alerts) != 1 {
		t.Errorf("alerts = %#v, want one firing rule", data["alerts"])
	}
	_ = conn.Close()
}
