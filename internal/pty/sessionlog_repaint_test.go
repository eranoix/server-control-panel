package pty

import (
	"bytes"
	"strings"
	"testing"
)

func inkStream(times int, lines string) []byte {
	var b bytes.Buffer
	for i := 0; i < times; i++ {
		b.WriteString("some line of conversation\r\n")
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
	readline := []byte(strings.Repeat("\x1b[1A", 40) + strings.Repeat("\x1b[A", 40))
	if isRepaintStream(readline) {
		t.Fatal("a one-line prompt redraw was classified as a repaint")
	}
}

func TestIsRepaintStream_EmptyOrZeroParamMeansOneLine(t *testing.T) {
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
	if isRepaintStream(inkStream(11, "4")) {
		t.Fatal("11 ascents (worst real shell) should not have been enough")
	}
	if !isRepaintStream(inkStream(33, "4")) {
		t.Fatal("33 ascents (quietest real TUI) had to be enough")
	}
}

func TestIsRepaintStream_TruncatedSequenceDoesNotOverflow(t *testing.T) {
	for _, s := range []string{"text\x1b[12", "text\x1b", "text", ""} {
		if isRepaintStream([]byte(s)) {
			t.Fatalf("truncated input %q classified as a repaint", s)
		}
	}
}

func TestIsRepaintStream_OtherCsiSequencesDoNotCount(t *testing.T) {
	others := []byte(strings.Repeat("\x1b[2J\x1b[10B\x1b[3C\x1b[5D", 100))
	if isRepaintStream(others) {
		t.Fatal("a CSI that is not CUU was counted")
	}
}

func TestLogTail_ClassifiesOnlyWhatIsReplayed(t *testing.T) {
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
