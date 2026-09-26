package pty

import "testing"

// THE TEST THAT SAID "A CLIENT THAT PRIMES ITSELF GETS NO WOBBLE" IS GONE.
//
// It pinned a conclusion of mine that measurement later knocked down — that the
// app, because it rebuilds the screen by replaying the log, did not need the
// repaint. What it really protected is still protected, and in a better place:
// the wobble must not SHRINK the PTY (that is what fuses two layouts and
// scrambles the screen), and that now lives in the body of `wobble`, where
// whoever goes to touch the number will read it.
//
// What stays on record is that the same behaviour was switched off and back on
// the same day, and both times by measurement — not by taste.

// THE TWO ANSWERS ARE DIFFERENT, AND THE STORY OF HOW I GOT IT WRONG AT BOTH
// ENDS IS WHAT THIS TEST KEEPS.
//
// At first they were separate by accident, and the app — which rebuilds its own
// screen — took a repaint-wobble on every attach that fused two layouts and
// scrambled the screen. So I tied them together, and this test said "they are
// the same question".
//
// That was wrong, and the measurement showed why. History and repaint answer
// different things:
//
//	"who shows the PAST?" -> the app, replaying the log. It does not want the
//	                         server's: it would show everything twice.
//	"who draws the NOW?"  -> only the remote program knows. The log is a cut of
//	                         a live stream taken at an arbitrary instant, and in
//	                         the middle of a frame it rebuilds a HALF-PAINTED
//	                         screen.
//
// Measured: the bytes `rawLogTail` returns for the "Aplicativo" session end in
// the middle of a table being drawn; fed into the app's own engine they yield
// four lines of content and forty-nine blank ones. And that is why attaching an
// image fixed the screen — the sheet changes the grid height, the resize reaches
// the PTY, and the program repaints a WHOLE frame.
//
// What made it safe to give the repaint back was not changing my mind: it was
// the nudge ceasing to SHRINK and starting to GROW. Shrinking scrolls the screen
// and loses content; growing only adds blank lines at the bottom. See the body
// of `wobble`.
func TestHistoricoERepaintRespondemPerguntasDiferentes(t *testing.T) {
	casos := []struct {
		nome                     string
		attach, replay           string
		querHistorico, querPaint bool
	}{
		{
			// The app: it primes the past on its own, but it needs the program to
			// draw the now.
			nome:   "app, fresh attach",
			attach: "", replay: "0",
			querHistorico: false, querPaint: true,
		},
		{
			// The web panel: it rebuilds nothing on its own. Both.
			nome:   "web panel, fresh attach",
			attach: "", replay: "",
			querHistorico: true, querPaint: true,
		},
		{
			// Reconnect: the in-memory grid is intact on both sides.
			// Repainting would duplicate; replaying history would duplicate.
			nome:   "reconnect",
			attach: "1", replay: "",
			querHistorico: false, querPaint: false,
		},
		{
			nome:   "app reconnect",
			attach: "1", replay: "0",
			querHistorico: false, querPaint: false,
		},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			historico, repaint := primingDoServidor(c.attach, c.replay)
			if historico != c.querHistorico {
				t.Errorf("mandarHistorico = %v, want %v", historico, c.querHistorico)
			}
			if repaint != c.querPaint {
				t.Errorf("forcarRepaint = %v, want %v", repaint, c.querPaint)
			}
		})
	}
}

// NO RECONNECT REPAINTS. It is the one rule that did not change in either turn:
// with the client's grid intact, repainting over it duplicates.
func TestReconexaoNuncaRepinta(t *testing.T) {
	for _, replay := range []string{"", "0", "1"} {
		if _, repaint := primingDoServidor("1", replay); repaint {
			t.Errorf("replay=%q: reconnect asked for repaint", replay)
		}
	}
}
