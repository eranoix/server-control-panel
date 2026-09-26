package pty

import (
	"bytes"
	"strings"
	"testing"

	"server-control-panel/internal/pty/vt10x"
)

// telaComTexto builds a glyph screen out of lines of text.
func telaComTexto(cols int, linhas ...string) [][]vt10x.Glyph {
	out := make([][]vt10x.Glyph, 0, len(linhas))
	for _, l := range linhas {
		linha := make([]vt10x.Glyph, cols)
		r := []rune(l)
		for i := 0; i < cols; i++ {
			c := ' '
			if i < len(r) {
				c = r[i]
			}
			linha[i] = vt10x.Glyph{Char: c, FG: vt10x.DefaultFG, BG: vt10x.DefaultBG}
		}
		out = append(out, linha)
	}
	return out
}

func semEscapes(b []byte) string { return stripANSI(string(b)) }

// The first frame comes out whole: the client has just entered frame mode and
// whatever was on its screen corresponds to nothing.
func TestQuadroPrimeiroVemInteiro(t *testing.T) {
	q := novoQuadroDoCliente(20, 3)
	saida := q.atualiza(telaComTexto(40, "primeira", "segunda", "terceira"), vt10x.Cursor{X: 0, Y: 0}, true)
	texto := semEscapes(saida)
	for _, alvo := range []string{"primeira", "segunda", "terceira"} {
		if !strings.Contains(texto, alvo) {
			t.Errorf("the first frame did not bring %q: %q", alvo, texto)
		}
	}
	if !bytes.Contains(saida, []byte("\x1b[H\x1b[2J")) {
		t.Error("the first frame has to clear the client's screen before painting")
	}
}

// WHAT THIS TEST PROTECTS: typing costs ONE line, not the screen.
//
// A 53x45 crop repainted in full costs a few KiB; at 10 frames per second that
// is tens of KiB/s on the phone — exactly what the data-saving work exists to
// prevent.
func TestQuadroSoMandaALinhaQueMudou(t *testing.T) {
	q := novoQuadroDoCliente(20, 3)
	tela := telaComTexto(40, "alfa", "beta", "gama")
	q.atualiza(tela, vt10x.Cursor{}, true)

	// Nothing changed: nothing to send.
	if s := q.atualiza(tela, vt10x.Cursor{}, true); s != nil {
		t.Errorf("identical screen generated %d bytes; wanted nothing", len(s))
	}

	// Only the middle one changed.
	tela2 := telaComTexto(40, "alfa", "BETA!", "gama")
	saida := q.atualiza(tela2, vt10x.Cursor{}, true)
	texto := semEscapes(saida)
	if !strings.Contains(texto, "BETA!") {
		t.Errorf("the line that changed was not sent: %q", texto)
	}
	if strings.Contains(texto, "alfa") || strings.Contains(texto, "gama") {
		t.Errorf("sent a line that did not change: %q", texto)
	}
	if !bytes.Contains(saida, []byte("\x1b[2;1H")) {
		t.Error("the line has to go with absolute addressing for line 2")
	}
}

// Scrolling is handled as scrolling: the client scrolls (and its content goes
// into ITS own scrollback), instead of the whole screen being repainted.
func TestQuadroRolagemNaoRepintaATelaInteira(t *testing.T) {
	q := novoQuadroDoCliente(20, 4)
	q.atualiza(telaComTexto(40, "um", "dois", "tres", "quatro"), vt10x.Cursor{}, true)

	rolagem := q.rolou(1)
	if !bytes.Contains(rolagem, []byte("\x1b[4;1H")) || !bytes.Contains(rolagem, []byte("\r\n")) {
		t.Fatalf("the scroll has to go to the last line and break: %q", rolagem)
	}

	// After scrolling by 1, the session shows two/three/four/five. The client
	// ALREADY HAS the first three (they scrolled up on its own screen).
	saida := q.atualiza(telaComTexto(40, "dois", "tres", "quatro", "cinco"), vt10x.Cursor{}, true)
	texto := semEscapes(saida)
	if !strings.Contains(texto, "cinco") {
		t.Errorf("the new line was not sent: %q", texto)
	}
	for _, velha := range []string{"dois", "tres", "quatro"} {
		if strings.Contains(texto, velha) {
			t.Errorf("repainted %q, which the client already had after scrolling: %q", velha, texto)
		}
	}
}

// Cropping: the narrower window sees a piece, and the pan reaches the rest.
// Without it, the right half of the session would be unreachable.
func TestQuadroRecortaEDesloca(t *testing.T) {
	q := novoQuadroDoCliente(10, 1)
	tela := telaComTexto(40, "0123456789ABCDEFGHIJ")

	esq := semEscapes(q.atualiza(tela, vt10x.Cursor{}, true))
	if !strings.Contains(esq, "0123456789") || strings.Contains(esq, "ABCDEF") {
		t.Errorf("the left crop came out wrong: %q", esq)
	}

	q.desloca(10, 40)
	dir := semEscapes(q.atualiza(tela, vt10x.Cursor{}, true))
	if !strings.Contains(dir, "ABCDEFGHIJ") {
		t.Errorf("the pan did not reach the right side: %q", dir)
	}
}

// The pan is clamped to the bounds: there is no negative column, and going past
// the end of the session achieves nothing.
func TestQuadroDeslocamentoFicaNosLimites(t *testing.T) {
	q := novoQuadroDoCliente(10, 1)
	q.desloca(-5, 40)
	if q.desloc != 0 {
		t.Errorf("a negative pan became %d; want 0", q.desloc)
	}
	q.desloca(999, 40)
	if q.desloc != 30 {
		t.Errorf("pan past the end became %d; wanted 30 (40-10)", q.desloc)
	}
	q.desloca(999, 5) // session smaller than the window
	if q.desloc != 0 {
		t.Errorf("with a session smaller than the window the pan has to be 0; became %d", q.desloc)
	}
}

// Resizing the window invalidates what we knew: sending a diff against a base of
// another size writes a line in the wrong place.
func TestQuadroRedimensionarRepintaTudo(t *testing.T) {
	q := novoQuadroDoCliente(20, 2)
	tela := telaComTexto(40, "alfa", "beta")
	q.atualiza(tela, vt10x.Cursor{}, true)
	q.redimensiona(30, 2)
	saida := semEscapes(q.atualiza(tela, vt10x.Cursor{}, true))
	if !strings.Contains(saida, "alfa") || !strings.Contains(saida, "beta") {
		t.Errorf("after resizing, the frame has to come whole: %q", saida)
	}
}

// The cursor goes last and in CLIENT coordinates — otherwise it stays where the
// last painted line ended.
func TestQuadroPoeOCursorPorUltimo(t *testing.T) {
	q := novoQuadroDoCliente(20, 3)
	saida := q.atualiza(telaComTexto(40, "a", "b", "c"), vt10x.Cursor{X: 5, Y: 1}, true)
	i := bytes.LastIndex(saida, []byte("\x1b[2;6H"))
	if i < 0 {
		t.Fatalf("did not find the cursor positioning: %q", saida)
	}
	if bytes.Contains(saida[i:], []byte(";1H")) {
		t.Error("a line came after the cursor — it has to be the last thing")
	}
}

// With a pan, the cursor pans too: it is a window coordinate.
func TestQuadroCursorSegueODeslocamento(t *testing.T) {
	q := novoQuadroDoCliente(10, 1)
	q.desloca(10, 40)
	saida := q.atualiza(telaComTexto(40, "0123456789ABCDEFGHIJ"), vt10x.Cursor{X: 12, Y: 0}, true)
	if !bytes.Contains(saida, []byte("\x1b[1;3H")) {
		t.Errorf("cursor at column 12 of the session, with the crop starting at 10, has to come out at 3: %q", saida)
	}
}

// THE VERTICAL CROP IS ANCHORED AT THE BOTTOM.
//
// A 2-line window looking at a 5-line session has to see the LAST 2: the work
// happens where the cursor is, and the cursor is at the bottom. Cropping from the
// top hands over a screen that never changes while the person types — that was
// the design mistake that only showed up in the browser.
func TestQuadroRecorteVerticalPegaOFim(t *testing.T) {
	q := novoQuadroDoCliente(20, 2)
	tela := telaComTexto(40, "um", "dois", "tres", "quatro", "cinco")
	texto := semEscapes(q.atualiza(tela, vt10x.Cursor{X: 0, Y: 4}, true))
	if !strings.Contains(texto, "quatro") || !strings.Contains(texto, "cinco") {
		t.Errorf("the crop has to bring the LAST lines: %q", texto)
	}
	if strings.Contains(texto, "um") || strings.Contains(texto, "dois") {
		t.Errorf("the crop brought a line from the top: %q", texto)
	}
}

// And the cursor follows the crop: on line 5 of the session, with a 2-line
// window, it comes out on line 2 of the client.
func TestQuadroCursorSegueORecorteVertical(t *testing.T) {
	q := novoQuadroDoCliente(20, 2)
	tela := telaComTexto(40, "um", "dois", "tres", "quatro", "cinco")
	saida := q.atualiza(tela, vt10x.Cursor{X: 3, Y: 4}, true)
	if !bytes.Contains(saida, []byte("\x1b[2;4H")) {
		t.Errorf("cursor on line 5 of the session, window of 2, has to come out on line 2: %q", saida)
	}
}

// In a freshly opened shell the content is at the TOP — the screen has not filled
// yet. A crop anchored at the bottom would show empty lines, which was this
// file's second mistake, caught by the end-to-end test in Go.
func TestQuadroComTelaVaziaEmbaixoMostraOTopo(t *testing.T) {
	q := novoQuadroDoCliente(20, 3)
	// A 10-line session, content only on the first two, cursor on the second.
	tela := telaComTexto(40, "prompt$ echo oi", "oi", "", "", "", "", "", "", "", "")
	texto := semEscapes(q.atualiza(tela, vt10x.Cursor{X: 0, Y: 1}, true))
	if !strings.Contains(texto, "prompt$ echo oi") || !strings.Contains(texto, "oi") {
		t.Errorf("with the cursor at the top, the crop has to show the top: %q", texto)
	}
}

// And the anchor does not move while the cursor stays in the visible band: moving
// it shifts every line and costs a full repaint per new line.
func TestQuadroAncoraNaoSeMexeSemPrecisar(t *testing.T) {
	q := novoQuadroDoCliente(20, 3)
	tela := telaComTexto(40, "um", "dois", "tres", "quatro", "cinco")
	q.atualiza(tela, vt10x.Cursor{Y: 4}, true) // anchor goes to 2 (shows 3..5)
	ancoraAntes := q.ancora
	q.atualiza(tela, vt10x.Cursor{Y: 3}, true) // cursor still inside the band
	if q.ancora != ancoraAntes {
		t.Errorf("the anchor moved (%d -> %d) while the cursor was still visible", ancoraAntes, q.ancora)
	}
}
