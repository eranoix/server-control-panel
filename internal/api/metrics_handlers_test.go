package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"server-control-panel/internal/config"
	"server-control-panel/internal/metrics"
)

// minimal Router with a metric registry exposing one known metric + an engine.
func testRouterWithMetric(t *testing.T, key string) *Router {
	t.Helper()
	reg := metrics.NewRegistry()
	reg.Register(metrics.NewFuncCollector(0,
		[]metrics.MetricDescriptor{{Key: key, Label: "Teste", Unit: "count", Category: "Teste", Kind: "gauge"}},
		func(context.Context) map[string]float64 { return map[string]float64{key: 3} }))
	reg.Gather(context.Background())
	return &Router{
		alerts:     metrics.NewEngine(),
		metricReg:  reg,
		metricHist: metrics.NewMetricHistory(10),
		cfg:        &config.Config{DataDir: t.TempDir()},
	}
}

// buildMetricRegistry must be nil-safe: on a bare Router (no queue/notify/
// claude/docker/whatsapp), it still registers the dependency-free collectors
// (system, jobs, auth, sessions) and produces a non-empty catalog — proving boot
// wiring won't panic when a subsystem failed to initialize.
func TestBuildMetricRegistryNilSafe(t *testing.T) {
	r := &Router{cfg: &config.Config{DataDir: t.TempDir()}}
	r.buildMetricRegistry()
	if r.metricReg == nil || r.metricHist == nil {
		t.Fatal("registry/history not built")
	}
	cat := r.metricReg.Catalog()
	if len(cat) == 0 {
		t.Fatal("catalog empty — no dependency-free collectors registered")
	}
	// jobs + auth descriptors should be present even with nil subsystems.
	keys := map[string]bool{}
	for _, d := range cat {
		keys[d.Key] = true
	}
	if !keys["auth.login_attempts"] || !keys["jobs.failed.total"] {
		t.Fatalf("expected dependency-free metrics in catalog, got %d entries", len(cat))
	}
}

func TestMetricsCatalogHandler(t *testing.T) {
	r := testRouterWithMetric(t, "test.metric")
	w := httptest.NewRecorder()
	r.handleMetricsCatalog(w, httptest.NewRequest("GET", "/api/metrics/catalog", nil))
	if w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	var body struct {
		Metrics []metrics.MetricDescriptor `json:"metrics"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Metrics) != 1 || body.Metrics[0].Key != "test.metric" {
		t.Fatalf("catalog wrong: %+v", body.Metrics)
	}
}

func TestMetricsSnapshotHandler(t *testing.T) {
	r := testRouterWithMetric(t, "test.metric")
	w := httptest.NewRecorder()
	r.handleMetricsSnapshot(w, httptest.NewRequest("GET", "/api/metrics/snapshot", nil))
	var snap metrics.Snapshot
	if err := json.Unmarshal(w.Body.Bytes(), &snap); err != nil {
		t.Fatal(err)
	}
	if snap.Values["test.metric"] != 3 {
		t.Fatalf("snapshot value wrong: %+v", snap.Values)
	}
}

// Adding a rule with an unknown metric key is rejected (400).
func TestAlertAddRejectsUnknownMetric(t *testing.T) {
	r := testRouterWithMetric(t, "test.metric")
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/metrics/rules/add",
		strings.NewReader(`{"name":"r","metric":"bogus.nope","op":">","threshold":1}`))
	r.handleAlertAdd(w, req)
	if w.Code != 400 {
		t.Fatalf("unknown metric should be 400, got %d", w.Code)
	}
}

// Adding a rule with a known metric succeeds and the rule appears in the list
// with its label/unit resolved from the catalog.
func TestAlertAddKnownMetricEnrichesLabel(t *testing.T) {
	r := testRouterWithMetric(t, "test.metric")
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/metrics/rules/add",
		strings.NewReader(`{"name":"r","metric":"test.metric","op":">","threshold":1}`))
	r.handleAlertAdd(w, req)
	if w.Code != 200 {
		t.Fatalf("known metric should be 200, got %d (%s)", w.Code, w.Body.String())
	}
	// Re-evaluate so Status has a fresh snapshot, then list.
	r.alerts.Evaluate(r.metricReg.Latest())
	lw := httptest.NewRecorder()
	r.handleAlertList(lw, httptest.NewRequest("GET", "/api/metrics/rules", nil))
	var body struct {
		Rules []metrics.RuleStatus `json:"rules"`
	}
	if err := json.Unmarshal(lw.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Rules) != 1 {
		t.Fatalf("want 1 rule, got %d", len(body.Rules))
	}
	if body.Rules[0].Label != "Teste" || body.Rules[0].Unit != "count" {
		t.Fatalf("label/unit not enriched: %+v", body.Rules[0])
	}
}
