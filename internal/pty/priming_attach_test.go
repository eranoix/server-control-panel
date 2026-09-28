package pty

import "testing"

func TestHistoryAndRepaintAnswerDifferentQuestions(t *testing.T) {
	cases := []struct {
		name                   string
		attach, replay         string
		wantHistory, wantPaint bool
	}{
		{
			name:   "app, fresh attach",
			attach: "", replay: "0",
			wantHistory: false, wantPaint: true,
		},
		{
			name:   "web panel, fresh attach",
			attach: "", replay: "",
			wantHistory: true, wantPaint: true,
		},
		{
			name:   "reconnect",
			attach: "1", replay: "",
			wantHistory: false, wantPaint: false,
		},
		{
			name:   "app reconnect",
			attach: "1", replay: "0",
			wantHistory: false, wantPaint: false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			history, repaint := serverPriming(c.attach, c.replay)
			if history != c.wantHistory {
				t.Errorf("sendHistory = %v, want %v", history, c.wantHistory)
			}
			if repaint != c.wantPaint {
				t.Errorf("forceRepaint = %v, want %v", repaint, c.wantPaint)
			}
		})
	}
}

func TestReconnectNeverRepaints(t *testing.T) {
	for _, replay := range []string{"", "0", "1"} {
		if _, repaint := serverPriming("1", replay); repaint {
			t.Errorf("replay=%q: reconnect asked for repaint", replay)
		}
	}
}
