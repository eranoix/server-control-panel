package api

import (
	"encoding/json"
	"testing"

	"server-control-panel/internal/metrics"
	"server-control-panel/internal/notify"
)

// RuleStatus embeds Rule anonymously; encoding/json must flatten it so the
// existing front-end keeps reading r.name/r.field/... at the top level while
// the new state fields are purely additive.
func TestRuleStatusMarshalsFlat(t *testing.T) {
	rs := metrics.RuleStatus{
		Rule:         metrics.Rule{Name: "cpu", Field: "cpu", Op: ">", Threshold: 90, Duration: 60, Severity: "critical"},
		State:        "firing",
		CurrentValue: 95.5,
		PendingSince: 1000,
	}
	b, _ := json.Marshal(rs)
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"name", "field", "op", "threshold", "duration", "severity", "state", "current_value", "pending_since"} {
		if _, ok := m[k]; !ok {
			t.Fatalf("missing flattened key %q in %s", k, b)
		}
	}
	if m["name"] != "cpu" || m["state"] != "firing" {
		t.Fatalf("unexpected flattened values: %s", b)
	}
}

// sanitizeList must dedup []RuleStatus by the promoted (embedded) "Name" field.
func TestSanitizeListPromotesEmbeddedName(t *testing.T) {
	in := []metrics.RuleStatus{
		{Rule: metrics.Rule{Name: "a"}, State: "normal"},
		{Rule: metrics.Rule{Name: "a"}, State: "firing"}, // duplicate name
		{Rule: metrics.Rule{Name: "b"}, State: "pending"},
	}
	out, ok := sanitizeList(in, "Name").([]metrics.RuleStatus)
	if !ok {
		t.Fatalf("sanitizeList did not return []RuleStatus")
	}
	if len(out) != 2 {
		t.Fatalf("want 2 after dedup by promoted Name, got %d", len(out))
	}
}

// A rule with severity:"critical" must emit a metric.threshold event carrying
// critical severity through the notify spine.
func TestRecordFiresPropagatesCriticalSeverity(t *testing.T) {
	rt, err := notify.New(notify.Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	r := &Router{notify: rt}
	r.recordFires([]metrics.Fire{{Rule: "cpu", Time: 300, Value: 99, Severity: "critical"}})
	hist := rt.History(notify.HistoryFilter{Type: notify.TypeMetricThreshold})
	if len(hist) != 1 {
		t.Fatalf("want 1 event, got %d", len(hist))
	}
	if hist[0].Severity != notify.SeverityCritical {
		t.Fatalf("severity not propagated: %q", hist[0].Severity)
	}
}

// A fire with no severity (legacy rule) defaults to warning.
func TestRecordFiresDefaultsSeverityToWarning(t *testing.T) {
	rt, err := notify.New(notify.Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	r := &Router{notify: rt}
	r.recordFires([]metrics.Fire{{Rule: "cpu", Time: 301, Value: 99}})
	hist := rt.History(notify.HistoryFilter{Type: notify.TypeMetricThreshold})
	if len(hist) != 1 || hist[0].Severity != notify.SeverityWarning {
		t.Fatalf("legacy default should be warning: %+v", hist)
	}
}
