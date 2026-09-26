package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"server-control-panel/internal/config"
	"server-control-panel/internal/metrics"
)

// snapCPU builds a fresh (non-stale) snapshot carrying a sys.cpu value.
func snapCPU(v float64) metrics.Snapshot {
	return metrics.Snapshot{T: time.Now().Unix(), Values: map[string]float64{"sys.cpu": v}}
}

// newEdgeRouter wires the minimum Router for the recordFires→persist production
// path: a config with a tmp DataDir and a fresh alert Engine. notify stays nil
// (Dispatch is skipped), which is fine — this exercises ring + persistence.
func newEdgeRouter(t *testing.T) (*Router, string) {
	t.Helper()
	dir := t.TempDir()
	return &Router{cfg: &config.Config{DataDir: dir}, alerts: metrics.NewEngine()}, dir
}

// recordFires must persist the engine state on a transition so the new
// ActiveSince lands in alert_rules.json (the deploy-survival vector).
func TestRecordFiresPersistsOnTransition(t *testing.T) {
	r, dir := newEdgeRouter(t)
	if err := r.alerts.AddRule(metrics.Rule{Name: "cpu", Metric: "sys.cpu", Op: ">", Threshold: 80, Duration: 0}); err != nil {
		t.Fatal(err)
	}

	r.recordFires(r.alerts.Evaluate(snapCPU(95)))

	data, err := os.ReadFile(filepath.Join(dir, "alert_rules.json"))
	if err != nil {
		t.Fatalf("alert_rules.json not written on transition: %v", err)
	}
	var rules []map[string]any
	if err := json.Unmarshal(data, &rules); err != nil {
		t.Fatal(err)
	}
	if len(rules) != 1 {
		t.Fatalf("want 1 persisted rule, got %d", len(rules))
	}
	if _, ok := rules[0]["active_since"]; !ok {
		t.Fatalf("persisted rule missing active_since: %s", data)
	}
}

// End-to-end deploy survival through the PRODUCTION path: fire + persist via
// recordFires, then a fresh Engine.Load (simulating a restart) must NOT
// re-notify while the metric is still above the threshold.
func TestNoRefireAcrossReloadProductionPath(t *testing.T) {
	r, dir := newEdgeRouter(t)
	if err := r.alerts.AddRule(metrics.Rule{Name: "cpu", Metric: "sys.cpu", Op: ">", Threshold: 80, Duration: 0}); err != nil {
		t.Fatal(err)
	}
	r.recordFires(r.alerts.Evaluate(snapCPU(95))) // crossing + persist

	reloaded := metrics.NewEngine()
	if err := reloaded.Load(filepath.Join(dir, "alert_rules.json")); err != nil {
		t.Fatal(err)
	}
	fires := reloaded.Evaluate(snapCPU(96)) // still above after "restart"
	for _, f := range fires {
		if !f.Resolved {
			t.Fatalf("re-notified after reload while still firing: %+v", fires)
		}
	}
	if len(fires) != 0 {
		t.Fatalf("expected zero fires after reload, got %+v", fires)
	}
}

// The ring receives crossings only; a resolved fire reaches the notify spine but
// not the legacy "Disparos recentes" ring (which means crossings).
func TestRecordFiresRingExcludesResolved(t *testing.T) {
	r, _ := newEdgeRouter(t)
	r.recordFires([]metrics.Fire{
		{Rule: "cpu", Time: 1, Value: 95},
		{Rule: "cpu", Time: 2, Value: 10, Episode: 1, Resolved: true},
	})
	if len(r.fires) != 1 {
		t.Fatalf("ring should hold only the crossing, got %d: %+v", len(r.fires), r.fires)
	}
	if r.fires[0].Resolved {
		t.Fatal("ring must not contain a resolved fire")
	}
}
