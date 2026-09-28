package metrics

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func snap(key string, val float64) Snapshot {
	return Snapshot{T: time.Now().Unix(), Values: map[string]float64{key: val}}
}

func statusByName(ss []RuleStatus) map[string]RuleStatus {
	m := make(map[string]RuleStatus, len(ss))
	for _, s := range ss {
		m[s.Name] = s
	}
	return m
}

func TestStatusNormal(t *testing.T) {
	e := NewEngine()
	_ = e.AddRule(Rule{Name: "cpu-high", Metric: "sys.cpu", Op: ">", Threshold: 90, Duration: 60})
	e.Evaluate(snap("sys.cpu", 10))
	s := statusByName(e.Status())["cpu-high"]
	if s.State != "normal" {
		t.Fatalf("want normal, got %q", s.State)
	}
	if s.CurrentValue != 10 {
		t.Fatalf("current_value want 10 got %v", s.CurrentValue)
	}
	if s.MetricKey != "sys.cpu" {
		t.Fatalf("metric_key want sys.cpu got %q", s.MetricKey)
	}
}

func TestStatusPending(t *testing.T) {
	e := NewEngine()
	_ = e.AddRule(Rule{Name: "cost", Metric: "claude.cost.today", Op: ">", Threshold: 5, Duration: 60})
	e.Evaluate(snap("claude.cost.today", 7.5))
	s := statusByName(e.Status())["cost"]
	if s.State != "pending" {
		t.Fatalf("want pending, got %q", s.State)
	}
	if s.PendingSince == 0 {
		t.Fatal("pending_since should be set while pending")
	}
	if s.CurrentValue != 7.5 {
		t.Fatalf("current_value want 7.5 got %v", s.CurrentValue)
	}
}

func TestStatusFiring(t *testing.T) {
	e := NewEngine()
	_ = e.AddRule(Rule{Name: "jobs", Metric: "jobs.failed.total", Op: ">", Threshold: 0, Duration: 0})
	e.Evaluate(snap("jobs.failed.total", 1))
	e.Evaluate(snap("jobs.failed.total", 2))
	s := statusByName(e.Status())["jobs"]
	if s.State != "firing" {
		t.Fatalf("want firing, got %q", s.State)
	}
}

func TestStatusNodataWhenNeverSampled(t *testing.T) {
	e := NewEngine()
	_ = e.AddRule(Rule{Name: "r", Metric: "sys.cpu", Op: ">", Threshold: 90, Duration: 60})
	if statusByName(e.Status())["r"].State != "nodata" {
		t.Fatal("want nodata before any snapshot")
	}
}

func TestStatusNodataWhenMetricAbsent(t *testing.T) {
	e := NewEngine()
	_ = e.AddRule(Rule{Name: "docker", Metric: "docker.running", Op: ">", Threshold: 0, Duration: 0})
	e.Evaluate(snap("sys.cpu", 50))
	if statusByName(e.Status())["docker"].State != "nodata" {
		t.Fatal("want nodata when metric key absent from snapshot")
	}
}

func TestStatusNodataWhenStale(t *testing.T) {
	e := NewEngine()
	_ = e.AddRule(Rule{Name: "r", Metric: "sys.cpu", Op: ">", Threshold: 90, Duration: 0})
	e.Evaluate(Snapshot{T: time.Now().Unix() - (staleAfter + 90), Values: map[string]float64{"sys.cpu": 95}})
	if statusByName(e.Status())["r"].State != "nodata" {
		t.Fatal("want nodata when snapshot stale")
	}
}

func TestLegacyFieldResolves(t *testing.T) {
	e := NewEngine()
	if err := e.AddRule(Rule{Name: "legacy", Field: "disk_pct", Op: ">", Threshold: 80, Duration: 0}); err != nil {
		t.Fatalf("legacy field rule should be valid: %v", err)
	}
	e.Evaluate(snap("sys.disk.max", 95))
	s := statusByName(e.Status())["legacy"]
	if s.MetricKey != "sys.disk.max" {
		t.Fatalf("legacy disk_pct should map to sys.disk.max, got %q", s.MetricKey)
	}
	if s.State != "firing" {
		t.Fatalf("want firing, got %q", s.State)
	}
}

func TestMetricKeyResolution(t *testing.T) {
	cases := map[Rule]string{
		{Metric: "claude.cost.today"}: "claude.cost.today",
		{Field: "cpu"}:                "sys.cpu",
		{Field: "mem_pct"}:            "sys.mem_pct",
		{Field: "load1"}:              "sys.load1",
		{Field: "disk_pct"}:           "sys.disk.max",
	}
	for r, want := range cases {
		if got := r.metricKey(); got != want {
			t.Errorf("metricKey(%+v) = %q, want %q", r, got, want)
		}
	}
}

func TestAddRuleSeverityValidation(t *testing.T) {
	e := NewEngine()
	if err := e.AddRule(Rule{Name: "a", Metric: "sys.cpu", Op: ">", Threshold: 1, Severity: "critical"}); err != nil {
		t.Fatalf("critical should be valid: %v", err)
	}
	if err := e.AddRule(Rule{Name: "b", Metric: "sys.cpu", Op: ">", Threshold: 1, Severity: ""}); err != nil {
		t.Fatalf("empty severity should be valid (legacy): %v", err)
	}
	if err := e.AddRule(Rule{Name: "c", Metric: "sys.cpu", Op: ">", Threshold: 1, Severity: "bogus"}); err == nil {
		t.Fatal("bogus severity should be rejected")
	}
}

func TestAddRuleRequiresMetricOrField(t *testing.T) {
	e := NewEngine()
	if err := e.AddRule(Rule{Name: "x", Op: ">", Threshold: 1}); err == nil {
		t.Fatal("rule with no metric and no valid field should be rejected")
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alert_rules.json")
	e := NewEngine()
	_ = e.AddRule(Rule{Name: "cost", Metric: "claude.cost.today", Op: ">", Threshold: 5, Duration: 60, LastFired: 12345, Severity: "critical"})
	if err := e.Save(path); err != nil {
		t.Fatal(err)
	}
	e2 := NewEngine()
	if err := e2.Load(path); err != nil {
		t.Fatal(err)
	}
	got := e2.List()
	if len(got) != 1 {
		t.Fatalf("want 1 rule, got %d", len(got))
	}
	if got[0].Metric != "claude.cost.today" || got[0].LastFired != 12345 || got[0].Severity != "critical" {
		t.Fatalf("round-trip lost fields: %+v", got[0])
	}
}

func TestLoadMissingFileNoop(t *testing.T) {
	e := NewEngine()
	if err := e.Load(filepath.Join(t.TempDir(), "nope.json")); err != nil {
		t.Fatalf("missing file should be a no-op, got %v", err)
	}
}

func TestLoadSkipsInvalidRules(t *testing.T) {
	path := filepath.Join(t.TempDir(), "r.json")
	if err := os.WriteFile(path, []byte(`[{"name":"ok","metric":"sys.cpu","op":">","threshold":1},{"name":"bad","op":"??","threshold":1}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	e := NewEngine()
	if err := e.Load(path); err != nil {
		t.Fatal(err)
	}
	if len(e.List()) != 1 {
		t.Fatalf("invalid rule should be skipped, got %d rules", len(e.List()))
	}
}

func countFires(fires []Fire) (crossings, resolved int) {
	for _, f := range fires {
		if f.Resolved {
			resolved++
		} else {
			crossings++
		}
	}
	return
}

func TestFiresOnceWhileConditionHeld(t *testing.T) {
	e := NewEngine()
	_ = e.AddRule(Rule{Name: "cpu", Metric: "sys.cpu", Op: ">", Threshold: 80, Duration: 0})
	total := 0
	for i := 0; i < 5; i++ {
		fires := e.Evaluate(snap("sys.cpu", 95))
		c, r := countFires(fires)
		if r != 0 {
			t.Fatalf("eval %d: unexpected resolved fire", i)
		}
		total += c
	}
	if total != 1 {
		t.Fatalf("want exactly 1 crossing fire while held, got %d", total)
	}
}

func TestRefiresAfterRecovery(t *testing.T) {
	e := NewEngine()
	_ = e.AddRule(Rule{Name: "cpu", Metric: "sys.cpu", Op: ">", Threshold: 80, Duration: 0})

	up1 := e.Evaluate(snap("sys.cpu", 95))
	if c, r := countFires(up1); c != 1 || r != 0 {
		t.Fatalf("first crossing: want 1 crossing 0 resolved, got %d/%d", c, r)
	}
	ep1 := up1[0].Episode

	down := e.Evaluate(snap("sys.cpu", 10))
	if c, r := countFires(down); c != 0 || r != 1 {
		t.Fatalf("recovery: want 0 crossing 1 resolved, got %d/%d", c, r)
	}
	if down[0].Episode != ep1 {
		t.Fatalf("resolved fire should carry the opening episode %d, got %d", ep1, down[0].Episode)
	}

	up2 := e.Evaluate(snap("sys.cpu", 90))
	if c, r := countFires(up2); c != 1 || r != 0 {
		t.Fatalf("re-fire: want 1 crossing 0 resolved, got %d/%d", c, r)
	}
	if up2[0].Episode == 0 {
		t.Fatal("re-fire must carry an episode id")
	}
	_ = ep1
}

func TestEvaluateMarksActiveSince(t *testing.T) {
	e := NewEngine()
	_ = e.AddRule(Rule{Name: "cpu", Metric: "sys.cpu", Op: ">", Threshold: 80, Duration: 0})
	e.Evaluate(snap("sys.cpu", 95))
	r := e.List()[0]
	if r.ActiveSince == 0 {
		t.Fatal("ActiveSince should be set after a crossing fire")
	}
	e.Evaluate(snap("sys.cpu", 10))
	if e.List()[0].ActiveSince != 0 {
		t.Fatal("ActiveSince should clear after recovery")
	}
}

func TestNoResolveOrRefireOnNodataGap(t *testing.T) {
	e := NewEngine()
	_ = e.AddRule(Rule{Name: "cpu", Metric: "sys.cpu", Op: ">", Threshold: 80, Duration: 0})
	e.Evaluate(snap("sys.cpu", 95))

	gap := e.Evaluate(snap("other.metric", 1))
	if len(gap) != 0 {
		t.Fatalf("nodata gap must emit no fire, got %+v", gap)
	}
	if e.List()[0].ActiveSince == 0 {
		t.Fatal("episode must stay open across a nodata gap")
	}

	back := e.Evaluate(snap("sys.cpu", 96))
	if len(back) != 0 {
		t.Fatalf("still-high after gap must not re-notify, got %+v", back)
	}
}

func TestNoRefireAcrossReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alert_rules.json")
	e := NewEngine()
	_ = e.AddRule(Rule{Name: "cpu", Metric: "sys.cpu", Op: ">", Threshold: 80, Duration: 0})
	e.Evaluate(snap("sys.cpu", 95))
	if err := e.Save(path); err != nil {
		t.Fatal(err)
	}

	e2 := NewEngine()
	if err := e2.Load(path); err != nil {
		t.Fatal(err)
	}
	if e2.List()[0].ActiveSince == 0 {
		t.Fatal("ActiveSince must survive Save/Load")
	}
	fires := e2.Evaluate(snap("sys.cpu", 96))
	if len(fires) != 0 {
		t.Fatalf("must not re-notify after reload while still firing, got %+v", fires)
	}
	if statusByName(e2.Status())["cpu"].State != "firing" {
		t.Fatal("reloaded episode should report firing, not pending")
	}
}

func TestRearmMarginHysteresisGreater(t *testing.T) {
	e := NewEngine()
	_ = e.AddRule(Rule{Name: "cpu", Metric: "sys.cpu", Op: ">", Threshold: 80, RearmMargin: 5, Duration: 0})

	if c, r := countFires(e.Evaluate(snap("sys.cpu", 85))); c != 1 || r != 0 {
		t.Fatalf("cross: want 1/0, got %d/%d", c, r)
	}
	if fires := e.Evaluate(snap("sys.cpu", 78)); len(fires) != 0 {
		t.Fatalf("dip into deadband must not resolve, got %+v", fires)
	}
	if statusByName(e.Status())["cpu"].State != "firing" {
		t.Fatal("rule in deadband should still report firing")
	}
	if c, r := countFires(e.Evaluate(snap("sys.cpu", 74))); c != 0 || r != 1 {
		t.Fatalf("clearing deadband: want 0/1, got %d/%d", c, r)
	}
	if c, r := countFires(e.Evaluate(snap("sys.cpu", 81))); c != 1 || r != 0 {
		t.Fatalf("re-cross after recovery: want 1/0, got %d/%d", c, r)
	}
}

func TestRearmMarginHysteresisLess(t *testing.T) {
	e := NewEngine()
	_ = e.AddRule(Rule{Name: "low", Metric: "sys.cpu", Op: "<", Threshold: 20, RearmMargin: 5, Duration: 0})

	if c, r := countFires(e.Evaluate(snap("sys.cpu", 15))); c != 1 || r != 0 {
		t.Fatalf("cross below: want 1/0, got %d/%d", c, r)
	}
	if fires := e.Evaluate(snap("sys.cpu", 22)); len(fires) != 0 {
		t.Fatalf("rise into deadband must not resolve, got %+v", fires)
	}
	if c, r := countFires(e.Evaluate(snap("sys.cpu", 26))); c != 0 || r != 1 {
		t.Fatalf("clearing deadband: want 0/1, got %d/%d", c, r)
	}
	if c, r := countFires(e.Evaluate(snap("sys.cpu", 19))); c != 1 || r != 0 {
		t.Fatalf("re-cross below after recovery: want 1/0, got %d/%d", c, r)
	}
}

func TestRenotifySecReminderWhileHeld(t *testing.T) {
	e := NewEngine()
	past := time.Now().Unix() - 100
	_ = e.AddRule(Rule{Name: "cpu", Metric: "sys.cpu", Op: ">", Threshold: 80, Duration: 0,
		RenotifySec: 30, ActiveSince: past, LastFired: past})

	fires := e.Evaluate(snap("sys.cpu", 95))
	if c, r := countFires(fires); c != 1 || r != 0 {
		t.Fatalf("reminder: want 1 crossing-type 0 resolved, got %d/%d", c, r)
	}
	if fires[0].Episode != past {
		t.Fatalf("reminder must reuse the open episode %d, got %d", past, fires[0].Episode)
	}
	if fires[0].Time == fires[0].Episode {
		t.Fatal("reminder Time must differ from Episode so the dedup key is unique")
	}
}

func TestRenotifySecZeroNeverReminds(t *testing.T) {
	e := NewEngine()
	past := time.Now().Unix() - 100
	_ = e.AddRule(Rule{Name: "cpu", Metric: "sys.cpu", Op: ">", Threshold: 80, Duration: 0,
		RenotifySec: 0, ActiveSince: past, LastFired: past})
	if fires := e.Evaluate(snap("sys.cpu", 95)); len(fires) != 0 {
		t.Fatalf("RenotifySec 0 must never remind, got %+v", fires)
	}
}

func TestRearmRenotifyValidation(t *testing.T) {
	e := NewEngine()
	if err := e.AddRule(Rule{Name: "a", Metric: "sys.cpu", Op: ">", Threshold: 1, RearmMargin: -1}); err == nil {
		t.Fatal("negative rearm_margin should be rejected")
	}
	if err := e.AddRule(Rule{Name: "b", Metric: "sys.cpu", Op: ">", Threshold: 1, RenotifySec: -1}); err == nil {
		t.Fatal("negative renotify_sec should be rejected")
	}
}
