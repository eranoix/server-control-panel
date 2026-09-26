package pty

import (
	"os"
	"strings"
	"testing"
)

// EVERY PTY BYTE GOES INTO THE LOG EXACTLY ONCE, WITH ANY NUMBER OF CLIENTS.
//
// Every attached client is a `dtach -a` receiving the WHOLE PTY stream, and each
// connection teed it. With the app and the web panel open on the same session,
// the log held everything twice.
//
// It only hurts whoever READS the log, and what reads it is the Android app: it
// rebuilds the screen by replaying that log into its own libghostty-vt. The most
// visible damage is not repeated text, it is the BLANK SCREEN: Claude Code emits
// eleven `\r\n` to open room for its frame, the log records twenty-two, and on
// replay the whole content scrolls out of the active screen. That was the owner's
// report: "I opened the window and it was all black. I scrolled down and it appeared".
//
// The first attempt shared the WRITER and did not fix it: sharing the file
// prevents two descriptors, not two writes. This test fails on that version —
// that is the difference it exists to catch.
func TestDoisClientesNaMesmaSessaoGravamUmaVezSo(t *testing.T) {
	dir := t.TempDir()

	app, _, _, soltarApp := pegarLogDaSessao(dir, "sam", "Aplicativo")
	web, _, _, soltarWeb := pegarLogDaSessao(dir, "sam", "Aplicativo")

	// The PTY emits ONCE; BOTH connections receive it and tee it. That is exactly
	// how the defect happened.
	_, _ = app.Write([]byte("\r\n\r\n"))
	_, _ = web.Write([]byte("\r\n\r\n"))

	soltarWeb()
	// The web one left; the app is still attached and the recording must not stop.
	_, _ = app.Write([]byte("|depois"))
	soltarApp()

	conteudo, err := os.ReadFile(sessionLogPath(dir, "sam", "Aplicativo"))
	if err != nil {
		t.Fatalf("log was not written: %v", err)
	}
	if got := strings.Count(string(conteudo), "\r\n"); got != 2 {
		t.Errorf("wrote %d line breaks, wanted 2 — doubling the scrolls "+
			"empurra a tela inteira para o histórico e o app abre preto", got)
	}
	if !strings.Contains(string(conteudo), "|depois") {
		t.Error("the recording stopped when one of the connections left")
	}
}

// THE SCRIBE HAS A SUCCESSION.
//
// If the writer is always the first connection and nobody takes over when it
// leaves, closing the first tab leaves the session with no log — and the app's
// next attach rebuilds a screen frozen in time.
func TestQuandoAEscribaSaiOutraAssume(t *testing.T) {
	dir := t.TempDir()

	primeira, _, _, soltarPrimeira := pegarLogDaSessao(dir, "sam", "s")
	segunda, _, _, soltarSegunda := pegarLogDaSessao(dir, "sam", "s")
	defer soltarSegunda()

	_, _ = primeira.Write([]byte("A"))
	_, _ = segunda.Write([]byte("A")) // same thing, coming from the same PTY

	soltarPrimeira()

	// Now the second one is the scribe.
	_, _ = segunda.Write([]byte("B"))

	conteudo, _ := os.ReadFile(sessionLogPath(dir, "sam", "s"))
	if string(conteudo) != "AB" {
		t.Errorf("log = %q, wanted \"AB\" — either it doubled, or the succession did not happen", string(conteudo))
	}
}

// Releasing the same connection twice must not take down the log of whoever stayed.
func TestSoltarDuasVezesNaoFechaOLogDeQuemFicou(t *testing.T) {
	dir := t.TempDir()

	primeiro, _, _, soltarPrimeiro := pegarLogDaSessao(dir, "sam", "s")
	_, _, _, soltarSegundo := pegarLogDaSessao(dir, "sam", "s")

	soltarSegundo()
	soltarSegundo() // idempotent, on purpose

	if _, err := primeiro.Write([]byte("ainda vivo")); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	soltarPrimeiro()

	conteudo, _ := os.ReadFile(sessionLogPath(dir, "sam", "s"))
	if !strings.Contains(string(conteudo), "ainda vivo") {
		t.Error("releasing twice zeroed the count and closed the log of whoever was still attached")
	}
}

// Different sessions do not share a scribe — one's log must not end up in the
// other, nor may one silence the other.
func TestSessoesDiferentesGravamCadaUmaASua(t *testing.T) {
	dir := t.TempDir()
	a, _, _, soltarA := pegarLogDaSessao(dir, "sam", "uma")
	b, _, _, soltarB := pegarLogDaSessao(dir, "sam", "outra")

	_, _ = a.Write([]byte("da uma"))
	_, _ = b.Write([]byte("da outra"))
	soltarA()
	soltarB()

	umA, _ := os.ReadFile(sessionLogPath(dir, "sam", "uma"))
	umB, _ := os.ReadFile(sessionLogPath(dir, "sam", "outra"))
	if string(umA) != "da uma" || string(umB) != "da outra" {
		t.Errorf("one=%q other=%q — one session silenced or invaded the other", umA, umB)
	}
}
