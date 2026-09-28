package notify

import "testing"

func TestFormatText(t *testing.T) {
	ev := Event{
		Type: TypeJobFailed, Severity: SeverityCritical,
		Title: "Job failed", Owner: "sam", Body: "exit status 1",
		Labels: map[string]string{"kind": "shell", "origin": "user"},
	}
	got := FormatText(ev)
	want := "🔴 Job failed\nshell · user · sam\nexit status 1"
	if got != want {
		t.Fatalf("FormatText mismatch:\n got=%q\nwant=%q", got, want)
	}

	min := FormatText(Event{Type: "metric.threshold", Severity: SeverityWarning})
	if min != "🟡 metric.threshold" {
		t.Fatalf("minimal format wrong: %q", min)
	}
}
