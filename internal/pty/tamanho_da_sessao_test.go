package pty

import "testing"

// THE SESSION SIZE IS THE SMALLEST AMONG THE CLIENTS, AND EVERYONE IS TOLD.
//
// This is the classic multiplexer rule, and it has TWO halves. Copying only
// the first one was the
// defect: the app asked for 72 columns, the PTY stayed at 66 because of a
// smaller client, and the program wrapped its lines at 66 inside a grid of 72 —
// everything in the wrong place.
//
// The half that was missing is that EVERY client draws a grid the size of the
// SESSION, not of its own window. Hence the notice: the server picks AND SAYS
// SO, and the client draws that.
func TestOPtyFicaNoMenorEntreOsClientes(t *testing.T) {
	s := &logCompartilhado{}

	cols, rows, mudou, _ := s.registraTamanho(1, 120, 40, false)
	if !mudou || cols != 120 || rows != 40 {
		t.Fatalf("first client: %dx%d mudou=%v; wanted 120x40 mudou=true", cols, rows, mudou)
	}

	// A SMALLER client arrives: the session shrinks to fit it, per axis.
	cols, rows, mudou, _ = s.registraTamanho(2, 67, 53, false)
	if !mudou || cols != 67 || rows != 40 {
		t.Fatalf("with two clients: %dx%d mudou=%v; wanted 67x40 (smaller of each axis)", cols, rows, mudou)
	}
}

// WHOEVER IS ATTACHED HAS TO BE TOLD — otherwise the minimum becomes the defect.
//
// A client that does not know the effective size draws to the measure of its own
// window, and that is exactly where the text lands in the wrong place. The notice
// is not decoration: it is the half that was missing.
func TestTodosOsClientesSaoAvisadosQuandoOTamanhoMuda(t *testing.T) {
	s := &logCompartilhado{}
	var avisadoA, avisadoB [2]uint16
	s.registraTamanho(1, 120, 40, false)
	s.registraTamanho(2, 90, 50, false)
	s.registraAplicador(1, func(c, r uint16) { avisadoA = [2]uint16{c, r} })
	s.registraAplicador(2, func(c, r uint16) { avisadoB = [2]uint16{c, r} })

	_, _, mudou, avisos := s.registraTamanho(3, 67, 53, false)
	if !mudou {
		t.Fatal("the arrival of a smaller client did not change the size")
	}
	if len(avisos) != 2 {
		t.Fatalf("%d notices; wanted 2 — whoever was already attached has to know", len(avisos))
	}
	for _, a := range avisos {
		a(67, 40)
	}
	if avisadoA != [2]uint16{67, 40} || avisadoB != [2]uint16{67, 40} {
		t.Errorf("notices = %v and %v; wanted 67x40 on both", avisadoA, avisadoB)
	}
}

// WHOEVER ARRIVES KNOWS THE SIZE BEFORE THE FIRST BYTE.
//
// Without this, the new client draws the first frame on the wrong grid and only
// corrects itself on the next change — which may never come in an idle session.
func TestQuemChegaJaRecebeOTamanhoQueEstaValendo(t *testing.T) {
	s := &logCompartilhado{}
	s.registraTamanho(1, 67, 53, false)

	cols, rows := s.registraAplicador(2, func(uint16, uint16) {})
	if cols != 67 || rows != 53 {
		t.Errorf("registraAviso returned %dx%d; wanted what is already in effect, 67x53", cols, rows)
	}
}

// RE-ASSERTING THE SAME SIZE MUST NOT BECOME A SIGWINCH.
//
// The client re-asserts its size on every heartbeat, and that is what fixes the
// silent divergence. If every re-assertion touched the PTY, a TUI app would clear
// and repaint the screen every few seconds.
func TestReafirmarOMesmoTamanhoNaoMexeNoPty(t *testing.T) {
	s := &logCompartilhado{}
	s.registraTamanho(1, 80, 24, false)

	for i := 0; i < 5; i++ {
		if _, _, mudou, _ := s.registraTamanho(1, 80, 24, false); mudou {
			t.Fatalf("reassertion %d became SIGWINCH", i+1)
		}
	}
}

// WHEN THE SMALL CLIENT LEAVES, THE SESSION GROWS BACK.
//
// Without forgetting whoever left, closing the app on the phone would leave the
// web panel stuck at 67 columns forever.
func TestClienteQueSaiDeixaDeEncolherASessao(t *testing.T) {
	s := &logCompartilhado{}
	s.registraTamanho(1, 120, 40, false)
	s.registraTamanho(2, 67, 53, false)

	cols, rows, mudou, _ := s.esqueceTamanho(2)
	if !mudou || cols != 120 || rows != 40 {
		t.Fatalf("after the exit: %dx%d mudou=%v; wanted 120x40", cols, rows, mudou)
	}
}

// THE LAST ONE OUT DOES NOT TOUCH THE PTY.
//
// With nobody attached there is no screen for anything to fit into, and
// re-laying out the program against a screen nobody sees only produces a lost
// frame — which the next attach finds half-done.
func TestSemClientesOTamanhoFicaComoEstava(t *testing.T) {
	s := &logCompartilhado{}
	s.registraTamanho(1, 80, 24, false)

	cols, rows, mudou, _ := s.esqueceTamanho(1)
	if mudou {
		t.Error("touched the PTY with nobody attached")
	}
	if cols != 80 || rows != 24 {
		t.Errorf("returned %dx%d; wanted to keep 80x24", cols, rows)
	}
}

// A degenerate size never enters the minimum — and here that matters MORE than it
// did under "whoever spoke last": there, a client sending 1x1 ruined only itself;
// under the minimum, it drags the whole session down with it.
func TestTamanhoDegeneradoNaoArrastaASessao(t *testing.T) {
	s := &logCompartilhado{}
	s.registraTamanho(1, 80, 24, false)

	for _, d := range []struct{ c, r uint16 }{{1, 1}, {0, 0}, {80, 0}, {2000, 24}, {80, 2000}} {
		if _, _, mudou, _ := s.registraTamanho(2, d.c, d.r, false); mudou {
			t.Errorf("%dx%d entered the calculation", d.c, d.r)
		}
	}
	if s.aplicadoCols != 80 || s.aplicadoRows != 24 {
		t.Errorf("the session was dragged to %dx%d", s.aplicadoCols, s.aplicadoRows)
	}
}
