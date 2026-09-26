package pty

import (
	"encoding/json"
	"testing"
)

// The terminal size is RECONCILED STATE, not an event.
//
// The bug: the client only sent the size when the xterm CHANGED, and the send was
// dropped silently when the socket was not open at that instant —
// which is exactly what happens with a hidden window (throttled timer,
// watchdog recycling the connection). Once diverged, the two sides stayed
// that way FOREVER, and a one-column off-by-one is enough for the program to write each
// line one column further along: the unreadable screen the operator photographed.
//
// The fix depends on reasserting the size being CHEAP — otherwise the heartbeat
// would send SIGWINCH to the PTY every 20s, and SIGWINCH in a TUI app means
// "clear the screen and repaint". The dedup here is what makes reassertion possible,
// and that is why it is the guarantee this test protects.
//
// The test exercises the REAL DECISION (tamanhoAplicado.aceita) — replicating the rule
// inside the test would keep it passing after someone removed the dedup from
// the code, which is exactly the regression to protect against.
type aplicacao struct{ cols, rows uint16 }

// decideResizes runs the REAL DECISION the server takes for each resize
// message, with a single client attached: the proxy's degenerate guard
// (`tamanhoSao`) followed by the per-session reconciliation (`registraTamanho`), which is
// what decides whether anything reaches the PTY.
//
// Replicating the rule inside the test would keep it passing after
// someone removed the dedup from the code, which is exactly the regression to protect against.
//
// NOTE: this used to exercise a PER-CONNECTION dedup. It was
// removed — it kept the last size the CONNECTION asked for, not the one the PTY
// actually had, and with two clients that stopped the large client from recovering the
// session after the small one left. The guarantees below are the same; what now
// enforces them is the session.
func decideResizes(t *testing.T, mensagens []string) []aplicacao {
	t.Helper()
	sessao := &logCompartilhado{}
	var aplicadas []aplicacao
	for _, raw := range mensagens {
		var m ctrlMsg
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			t.Fatalf("invalid json in the test: %v", err)
		}
		if m.Type != "resize" {
			continue
		}
		if !tamanhoSao(m.Cols, m.Rows) {
			continue
		}
		if cols, rows, mudou, _ := sessao.registraTamanho(1, m.Cols, m.Rows, false); mudou {
			aplicadas = append(aplicadas, aplicacao{cols, rows})
		}
	}
	return aplicadas
}

func TestReafirmarOMesmoTamanhoNaoIncomodaOPrograma(t *testing.T) {
	// What the heartbeat does: repeat the same size indefinitely.
	msgs := []string{
		`{"type":"resize","cols":120,"rows":40}`,
		`{"type":"resize","cols":120,"rows":40}`,
		`{"type":"resize","cols":120,"rows":40}`,
		`{"type":"resize","cols":120,"rows":40}`,
	}
	aplicadas := decideResizes(t, msgs)
	if len(aplicadas) != 1 {
		t.Fatalf("applied %d resizes for the same size; wanted 1 — repeated SIGWINCH makes the TUI app clear and repaint the screen on every heartbeat", len(aplicadas))
	}
	if aplicadas[0] != (aplicacao{120, 40}) {
		t.Errorf("applied %v, want 120x40", aplicadas[0])
	}
}

func TestMudancaDeVerdadeAindaChegaAoPty(t *testing.T) {
	// The dedup must not swallow a real change — that would trade one bug for another.
	msgs := []string{
		`{"type":"resize","cols":120,"rows":40}`,
		`{"type":"resize","cols":120,"rows":40}`,
		`{"type":"resize","cols":121,"rows":40}`, // the off-by-one from the report
		`{"type":"resize","cols":121,"rows":40}`,
		`{"type":"resize","cols":80,"rows":24}`,
	}
	aplicadas := decideResizes(t, msgs)
	esperado := []aplicacao{{120, 40}, {121, 40}, {80, 24}}
	if len(aplicadas) != len(esperado) {
		t.Fatalf("applied %v; want %v", aplicadas, esperado)
	}
	for i := range esperado {
		if aplicadas[i] != esperado[i] {
			t.Errorf("resize %d = %v, want %v", i, aplicadas[i], esperado[i])
		}
	}
}

// Reconciliation is what fixes the screen after a divergence: the client
// re-asserts and the server agrees again, even if the original resize was lost.
// Without the dedup, this same sequence would bombard the PTY.
func TestDivergenciaSeCorrigeNaReafirmacao(t *testing.T) {
	msgs := []string{
		`{"type":"resize","cols":80,"rows":24}`,  // estado inicial
		`{"type":"resize","cols":120,"rows":40}`, // user switched windows; this one gets through
		`{"type":"resize","cols":120,"rows":40}`, // heartbeat reafirma
		`{"type":"resize","cols":120,"rows":40}`, // coming back to the window reasserts it
	}
	aplicadas := decideResizes(t, msgs)
	if len(aplicadas) != 2 {
		t.Fatalf("applied %d; wanted 2 (the initial one and the real change)", len(aplicadas))
	}
	if aplicadas[len(aplicadas)-1] != (aplicacao{120, 40}) {
		t.Errorf("final state %v; the server has to end up agreeing with the client", aplicadas[len(aplicadas)-1])
	}
}

// A degenerate size stays barred: a hidden or buggy client sending 1x1 would
// make the program redraw into a single column — pure garbage.
func TestTamanhoDegeneradoContinuaBarrado(t *testing.T) {
	msgs := []string{
		`{"type":"resize","cols":1,"rows":1}`,
		`{"type":"resize","cols":0,"rows":0}`,
		`{"type":"resize","cols":5000,"rows":5000}`,
	}
	if aplicadas := decideResizes(t, msgs); len(aplicadas) != 0 {
		t.Errorf("applied %v; none of these sizes may reach the PTY", aplicadas)
	}
}
