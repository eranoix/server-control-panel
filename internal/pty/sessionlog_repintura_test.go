package pty

import (
	"bytes"
	"strings"
	"testing"
)

// fluxoInk builds a stream like the Ink renderer's: it writes a line and walks
// the cursor up to repaint over it.
func fluxoInk(vezes int, linhas string) []byte {
	var b bytes.Buffer
	for i := 0; i < vezes; i++ {
		b.WriteString("uma linha de conversa qualquer\r\n")
		b.WriteString("\x1b[" + linhas + "A")
	}
	return b.Bytes()
}

func TestFluxoERepintado_ShellAppendOnlyNaoE(t *testing.T) {
	shell := []byte("$ ls -l\r\ntotal 4\r\ndrwxr-xr-x 2 root root 4096 dir\r\n$ ")
	if fluxoERepintado(shell) {
		t.Fatal("shell append-only output was classified as a repaint")
	}
}

func TestFluxoERepintado_RedesenhoDePromptNaoConta(t *testing.T) {
	// `ESC[1A` and `ESC[A` are what readline emits to redraw a two-line prompt.
	// The worst real shell on this machine had 34 of them and ZERO of two lines
	// or more; counting them would classify a shell as a TUI.
	readline := []byte(strings.Repeat("\x1b[1A", 40) + strings.Repeat("\x1b[A", 40))
	if fluxoERepintado(readline) {
		t.Fatal("a one-line prompt redraw was classified as a repaint")
	}
}

func TestFluxoERepintado_ParametroVazioOuZeroValeUmaLinha(t *testing.T) {
	// ECMA-48: an omitted or 0 parameter takes the command's default, which for
	// CUU is 1 — it must not count as "went up several".
	if fluxoERepintado([]byte(strings.Repeat("\x1b[0A\x1b[A", 200))) {
		t.Fatal("CUU with a 0/omitted parameter counted as moving up multiple lines")
	}
}

func TestFluxoERepintado_RenderizadorDiferencialEReconhecido(t *testing.T) {
	if !fluxoERepintado(fluxoInk(25, "7")) {
		t.Fatal("differential-renderer stream was not recognized")
	}
}

func TestFluxoERepintado_VaoMedidoEntreShellETui(t *testing.T) {
	// 11 = the worst real shell measured; 33 = the quietest real TUI measured.
	if fluxoERepintado(fluxoInk(11, "4")) {
		t.Fatal("11 ascents (worst real shell) should not have been enough")
	}
	if !fluxoERepintado(fluxoInk(33, "4")) {
		t.Fatal("33 ascents (quietest real TUI) had to be enough")
	}
}

func TestFluxoERepintado_SequenciaTruncadaNaoEstoura(t *testing.T) {
	// The replay cuts at 128 KiB and only aligns on the next line break: a
	// sequence can end up truncated. This must not read outside the slice.
	for _, s := range []string{"texto\x1b[12", "texto\x1b", "texto", ""} {
		if fluxoERepintado([]byte(s)) {
			t.Fatalf("truncated input %q classified as a repaint", s)
		}
	}
}

func TestFluxoERepintado_OutrasSequenciasCsiNaoContam(t *testing.T) {
	// `ESC[2J` limpar, `ESC[10B` descer, `ESC[3C` direita, `ESC[5D` esquerda.
	outras := []byte(strings.Repeat("\x1b[2J\x1b[10B\x1b[3C\x1b[5D", 100))
	if fluxoERepintado(outras) {
		t.Fatal("a CSI that is not CUU was counted")
	}
}

func TestTailDoLog_ClassificaSoOQueVaiSerReemitido(t *testing.T) {
	// A session that went through a TUI hours ago and is a shell today must not
	// lose its replay because of that past: what matters is the slice attachReplay
	// really sends.
	antigo := fluxoInk(500, "9")
	recente := bytes.Repeat([]byte("$ echo ok\r\nok\r\n"), maxAttachReplayBytes/15+16)
	log := append(antigo, recente...)
	if fluxoERepintado(tailDoLog(log)) {
		t.Fatal("an old TUI, outside the re-emitted slice, still influenced the decision")
	}
	if len(tailDoLog(log)) != maxAttachReplayBytes {
		t.Fatalf("slice = %d bytes, expected %d", len(tailDoLog(log)), maxAttachReplayBytes)
	}
}
