package pty

import (
	"bytes"
	"strings"
	"testing"
)

// inkStream builds a stream like the Ink renderer's: it writes a line and walks
// the cursor up to repaint over it.
func inkStream(times int, lines string) []byte {
	var b bytes.Buffer
	for i := 0; i < times; i++ {
		b.WriteString("uma linha de conversa qualquer\r\n")
		b.WriteString("\x1b[" + lines + "A")
	}
	return b.Bytes()
}

func TestIsRepaintStream_AppendOnlyShellIsNot(t *testing.T) {
	shell := []byte("$ ls -l\r\ntotal 4\r\ndrwxr-xr-x 2 root root 4096 dir\r\n$ ")
	if isRepaintStream(shell) {
		t.Fatal("shell append-only output was classified as a repaint")
	}
}

func TestIsRepaintStream_PromptRedrawDoesNotCount(t *testing.T) {
	// `ESC[1A` and `ESC[A` are what readline emits to redraw a two-line prompt.
	// The worst real shell on this machine had 34 of them and ZERO of two lines
	// or more; counting them would classify a shell as a TUI.
	readline := []byte(strings.Repeat("\x1b[1A", 40) + strings.Repeat("\x1b[A", 40))
	if isRepaintStream(readline) {
		t.Fatal("a one-line prompt redraw was classified as a repaint")
	}
}

func TestIsRepaintStream_EmptyOrZeroParamMeansOneLine(t *testing.T) {
	// ECMA-48: an omitted or 0 parameter takes the command's default, which for
	// CUU is 1 — it must not count as "went up several".
	if isRepaintStream([]byte(strings.Repeat("\x1b[0A\x1b[A", 200))) {
		t.Fatal("CUU with a 0/omitted parameter counted as moving up multiple lines")
	}
}

func TestIsRepaintStream_DiffRendererIsRecognized(t *testing.T) {
	if !isRepaintStream(inkStream(25, "7")) {
		t.Fatal("differential-renderer stream was not recognized")
	}
}

func TestIsRepaintStream_MeasuredGapBetweenShellAndTui(t *testing.T) {
	// 11 = the worst real shell measured; 33 = the quietest real TUI measured.
	if isRepaintStream(inkStream(11, "4")) {
		t.Fatal("11 ascents (worst real shell) should not have been enough")
	}
	if !isRepaintStream(inkStream(33, "4")) {
		t.Fatal("33 ascents (quietest real TUI) had to be enough")
	}
}

func TestIsRepaintStream_TruncatedSequenceDoesNotOverflow(t *testing.T) {
	// The replay cuts at 128 KiB and only aligns on the next line break: a
	// sequence can end up truncated. This must not read outside the slice.
	for _, s := range []string{"texto\x1b[12", "texto\x1b", "texto", ""} {
		if isRepaintStream([]byte(s)) {
			t.Fatalf("truncated input %q classified as a repaint", s)
		}
	}
}

func TestIsRepaintStream_OtherCsiSequencesDoNotCount(t *testing.T) {
	// `ESC[2J` limpar, `ESC[10B` descer, `ESC[3C` direita, `ESC[5D` esquerda.
	others := []byte(strings.Repeat("\x1b[2J\x1b[10B\x1b[3C\x1b[5D", 100))
	if isRepaintStream(others) {
		t.Fatal("a CSI that is not CUU was counted")
	}
}

func TestLogTail_ClassifiesOnlyWhatIsReplayed(t *testing.T) {
	// A session that went through a TUI hours ago and is a shell today must not
	// lose its replay because of that past: what matters is the slice attachReplay
	// really sends.
	old := inkStream(500, "9")
	recent := bytes.Repeat([]byte("$ echo ok\r\nok\r\n"), maxAttachReplayBytes/15+16)
	log := append(old, recent...)
	if isRepaintStream(logTail(log)) {
		t.Fatal("an old TUI, outside the re-emitted slice, still influenced the decision")
	}
	if len(logTail(log)) != maxAttachReplayBytes {
		t.Fatalf("slice = %d bytes, expected %d", len(logTail(log)), maxAttachReplayBytes)
	}
}
