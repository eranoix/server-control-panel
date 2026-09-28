package api

import (
	"testing"

	"server-control-panel/internal/metrics"
	"server-control-panel/internal/notify"
)

func TestRecordFiresStillPopulatesRing(t *testing.T) {
	r := &Router{}
	fires := []metrics.Fire{
		{Rule: "cpu", Time: 100, Value: 95.5},
		{Rule: "mem", Time: 101, Value: 88.0},
	}
	r.recordFires(fires)

	if len(r.fires) != 2 {
		t.Fatalf("ring not populated: got %d want 2", len(r.fires))
	}
	if r.fires[0].Rule != "cpu" || r.fires[1].Rule != "mem" {
		t.Fatalf("ring contents wrong: %+v", r.fires)
	}
}

func TestRecordFiresDualWritesToNotify(t *testing.T) {
	rt, err := notify.New(notify.Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	r := &Router{notify: rt}

	r.recordFires([]metrics.Fire{{Rule: "disk", Time: 200, Value: 91.0}})

	if len(r.fires) != 1 {
		t.Fatalf("ring not populated alongside dispatch: %d", len(r.fires))
	}
	hist := rt.History(notify.HistoryFilter{Type: notify.TypeMetricThreshold})
	if len(hist) != 1 || hist[0].DedupKey != "metric:disk:0" {
		t.Fatalf("metric event not dispatched: %+v", hist)
	}
}

func TestRecordFiresEmptyNoop(t *testing.T) {
	r := &Router{}
	r.recordFires(nil)
	if len(r.fires) != 0 {
		t.Fatalf("empty fires grew the ring: %d", len(r.fires))
	}
}
