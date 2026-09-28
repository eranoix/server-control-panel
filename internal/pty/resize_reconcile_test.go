package pty

import (
	"encoding/json"
	"testing"
)

type appliedSize struct{ cols, rows uint16 }

func decideResizes(t *testing.T, messages []string) []appliedSize {
	t.Helper()
	session := &sharedLog{}
	var applied []appliedSize
	for _, raw := range messages {
		var m ctrlMsg
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			t.Fatalf("invalid json in the test: %v", err)
		}
		if m.Type != "resize" {
			continue
		}
		if !sizeIsSane(m.Cols, m.Rows) {
			continue
		}
		if cols, rows, changed, _ := session.registerSize(1, m.Cols, m.Rows, false); changed {
			applied = append(applied, appliedSize{cols, rows})
		}
	}
	return applied
}

func TestReassertingSameSizeDoesNotDisturbProgram(t *testing.T) {
	msgs := []string{
		`{"type":"resize","cols":120,"rows":40}`,
		`{"type":"resize","cols":120,"rows":40}`,
		`{"type":"resize","cols":120,"rows":40}`,
		`{"type":"resize","cols":120,"rows":40}`,
	}
	applied := decideResizes(t, msgs)
	if len(applied) != 1 {
		t.Fatalf("applied %d resizes for the same size; wanted 1 — repeated SIGWINCH makes the TUI app clear and repaint the screen on every heartbeat", len(applied))
	}
	if applied[0] != (appliedSize{120, 40}) {
		t.Errorf("applied %v, want 120x40", applied[0])
	}
}

func TestRealChangeStillReachesPty(t *testing.T) {
	msgs := []string{
		`{"type":"resize","cols":120,"rows":40}`,
		`{"type":"resize","cols":120,"rows":40}`,
		`{"type":"resize","cols":121,"rows":40}`,
		`{"type":"resize","cols":121,"rows":40}`,
		`{"type":"resize","cols":80,"rows":24}`,
	}
	applied := decideResizes(t, msgs)
	expected := []appliedSize{{120, 40}, {121, 40}, {80, 24}}
	if len(applied) != len(expected) {
		t.Fatalf("applied %v; want %v", applied, expected)
	}
	for i := range expected {
		if applied[i] != expected[i] {
			t.Errorf("resize %d = %v, want %v", i, applied[i], expected[i])
		}
	}
}

func TestDivergenceIsFixedOnReassertion(t *testing.T) {
	msgs := []string{
		`{"type":"resize","cols":80,"rows":24}`,
		`{"type":"resize","cols":120,"rows":40}`,
		`{"type":"resize","cols":120,"rows":40}`,
		`{"type":"resize","cols":120,"rows":40}`,
	}
	applied := decideResizes(t, msgs)
	if len(applied) != 2 {
		t.Fatalf("applied %d; wanted 2 (the initial one and the real change)", len(applied))
	}
	if applied[len(applied)-1] != (appliedSize{120, 40}) {
		t.Errorf("final state %v; the server has to end up agreeing with the client", applied[len(applied)-1])
	}
}

func TestDegenerateSizeStillBlocked(t *testing.T) {
	msgs := []string{
		`{"type":"resize","cols":1,"rows":1}`,
		`{"type":"resize","cols":0,"rows":0}`,
		`{"type":"resize","cols":5000,"rows":5000}`,
	}
	if applied := decideResizes(t, msgs); len(applied) != 0 {
		t.Errorf("applied %v; none of these sizes may reach the PTY", applied)
	}
}
