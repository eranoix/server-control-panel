package pty

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// leHistorico returns the history file as lines of text, with no escapes.
func leHistorico(t *testing.T, dir, user, name string) []string {
	t.Helper()
	d, err := os.ReadFile(sessionHistPath(dir, user, name))
	if err != nil {
		return nil
	}
	var linhas []string
	for _, l := range strings.Split(stripANSI(string(d)), "\n") {
		l = strings.TrimRight(l, " \r")
		if l != "" {
			linhas = append(linhas, l)
		}
	}
	return linhas
}

// The basics: what leaves through the top of the screen enters the history, in order, once.
func TestHistoricoGuardaOQueSaiDaTela(t *testing.T) {
	dir := t.TempDir()
	tela := novaTelaDaSessao(dir, "u", "s")
	tela.redimensiona(40, 10)

	for i := 1; i <= 30; i++ {
		tela.alimenta([]byte(fmt.Sprintf("L%02d\r\n", i)))
	}
	tela.fecha()

	linhas := leHistorico(t, dir, "u", "s")
	// 30 lines on a 10-line screen: the first ~20 scrolled off.
	if len(linhas) < 18 || len(linhas) > 21 {
		t.Fatalf("history with %d lines; expected ~20 (30 written, 10-line screen)", len(linhas))
	}
	for i, l := range linhas {
		if quero := fmt.Sprintf("L%02d", i+1); l != quero {
			t.Fatalf("history line %d = %q; wanted %q (wrong order or content)", i, l, quero)
		}
	}
}

// WHAT THIS TEST PROTECTS, AND WHAT NO OTHER TEST DOES.
//
// A program that redraws (Claude Code, via Ink) walks the cursor up with
// `ESC[nA` and repaints over itself. Replaying those bytes onto a new grid
// DUPLICATES: the CUU saturates at the first line of the screen, never reaches
// the scrollback, and the previous copy stays — that was the "the text is
// duplicated" of the report.
//
// The server's history is not a replay: the emulator is alive, on the same grid
// the program is looking at, so the repaint lands where the program meant it to.
// What reaches the file is each line's FINAL result, once.
func TestHistoricoNaoDuplicaOQueOProgramaRepintou(t *testing.T) {
	dir := t.TempDir()
	tela := novaTelaDaSessao(dir, "u", "s")
	tela.redimensiona(40, 10)

	// A 6-line "frame", repainted three times in the same place — which is what
	// Ink does on every keystroke.
	for volta := 1; volta <= 3; volta++ {
		for i := 1; i <= 6; i++ {
			tela.alimenta([]byte(fmt.Sprintf("quadro %d linha %d\r\n", volta, i)))
		}
		if volta < 3 {
			tela.alimenta([]byte("\x1b[6A")) // sobe 6 linhas para repintar
		}
	}
	// Push everything off the screen so the history receives the result.
	for i := 0; i < 20; i++ {
		tela.alimenta([]byte("\r\n"))
	}
	tela.fecha()

	linhas := leHistorico(t, dir, "u", "s")
	contagem := map[string]int{}
	for _, l := range linhas {
		contagem[l]++
	}
	// Only the last frame survived the repainting — the earlier ones were
	// overwritten on the screen itself, which is what actually happened.
	for i := 1; i <= 6; i++ {
		alvo := fmt.Sprintf("quadro 3 linha %d", i)
		if n := contagem[alvo]; n != 1 {
			t.Errorf("%q appears %d time(s) in the history; wanted exactly 1", alvo, n)
		}
	}
	for _, morto := range []string{"quadro 1 linha 1", "quadro 2 linha 1"} {
		if n := contagem[morto]; n != 0 {
			t.Errorf("%q appears %d time(s); a frame repainted over is not history", morto, n)
		}
	}
}

// A multibyte character split on a chunk boundary: the recorder delivers whatever
// `read()` returned, and this happens all the time. Without carrying the
// remainder into the next chunk, every boundary would become a wrong character in
// the history.
func TestHistoricoAguentaRunePartidoEntreBlocos(t *testing.T) {
	dir := t.TempDir()
	tela := novaTelaDaSessao(dir, "u", "s")
	tela.redimensiona(40, 4)

	texto := []byte("ação não é só ção\r\n")
	// Delivered byte by byte: every possible rune boundary is exercised.
	for _, b := range texto {
		tela.alimenta([]byte{b})
	}
	for i := 0; i < 8; i++ {
		tela.alimenta([]byte("\r\n"))
	}
	tela.fecha()

	linhas := leHistorico(t, dir, "u", "s")
	achou := false
	for _, l := range linhas {
		if l == "ação não é só ção" {
			achou = true
		}
	}
	if !achou {
		t.Errorf("the accented text did not survive the chunk boundary; history = %q", linhas)
	}
}

// The alternate screen (vim, htop) is not history: what scrolls in there is the
// scratch of a full-screen program and would fill the file with junk.
func TestHistoricoIgnoraTelaAlternativa(t *testing.T) {
	dir := t.TempDir()
	tela := novaTelaDaSessao(dir, "u", "s")
	tela.redimensiona(40, 6)

	tela.alimenta([]byte("antes do vim\r\n"))
	tela.alimenta([]byte("\x1b[?1049h")) // entra na tela alternativa
	for i := 0; i < 30; i++ {
		tela.alimenta([]byte(fmt.Sprintf("rascunho %d\r\n", i)))
	}
	tela.alimenta([]byte("\x1b[?1049l")) // sai
	for i := 0; i < 10; i++ {
		tela.alimenta([]byte("\r\n"))
	}
	tela.fecha()

	for _, l := range leHistorico(t, dir, "u", "s") {
		if strings.HasPrefix(l, "rascunho") {
			t.Errorf("the history kept %q, which is alternate-screen scratch", l)
			break
		}
	}
}

// Reading the history cuts at the start of a LINE, never in the middle: half a
// line at the top is dirt the terminal draws as though it were content.
func TestHistoricoCortaEmLinhaInteira(t *testing.T) {
	if i := indiceDaProximaLinha([]byte("meio de linha\r\ninteira\r\n")); i != 15 {
		t.Errorf("next-line index = %d; wanted 15", i)
	}
	if i := indiceDaProximaLinha([]byte("sem quebra nenhuma")); i != -1 {
		t.Errorf("with no break, should return -1; returned %d", i)
	}
}
