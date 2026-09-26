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
func TestTwoClientsOnSameSessionRecordOnce(t *testing.T) {
	dir := t.TempDir()

	app, _, _, releaseApp := acquireSessionLog(dir, "sam", "App")
	web, _, _, releaseWeb := acquireSessionLog(dir, "sam", "App")

	// The PTY emits ONCE; BOTH connections receive it and tee it. That is exactly
	// how the defect happened.
	_, _ = app.Write([]byte("\r\n\r\n"))
	_, _ = web.Write([]byte("\r\n\r\n"))

	releaseWeb()
	// The web one left; the app is still attached and the recording must not stop.
	_, _ = app.Write([]byte("|after"))
	releaseApp()

	content, err := os.ReadFile(sessionLogPath(dir, "sam", "App"))
	if err != nil {
		t.Fatalf("log was not written: %v", err)
	}
	if got := strings.Count(string(content), "\r\n"); got != 2 {
		t.Errorf("wrote %d line breaks, wanted 2 — doubling the scrolls "+
			"pushes the whole screen into the history and the app opens black", got)
	}
	if !strings.Contains(string(content), "|after") {
		t.Error("the recording stopped when one of the connections left")
	}
}

// THE SCRIBE HAS A SUCCESSION.
//
// If the writer is always the first connection and nobody takes over when it
// leaves, closing the first tab leaves the session with no log — and the app's
// next attach rebuilds a screen frozen in time.
func TestWhenScribeLeavesAnotherTakesOver(t *testing.T) {
	dir := t.TempDir()

	first, _, _, releaseFirst := acquireSessionLog(dir, "sam", "s")
	second, _, _, releaseSecond := acquireSessionLog(dir, "sam", "s")
	defer releaseSecond()

	_, _ = first.Write([]byte("A"))
	_, _ = second.Write([]byte("A")) // same thing, coming from the same PTY

	releaseFirst()

	// Now the second one is the scribe.
	_, _ = second.Write([]byte("B"))

	content, _ := os.ReadFile(sessionLogPath(dir, "sam", "s"))
	if string(content) != "AB" {
		t.Errorf("log = %q, wanted \"AB\" — either it doubled, or the succession did not happen", string(content))
	}
}

// Releasing the same connection twice must not take down the log of whoever stayed.
func TestDoubleReleaseDoesNotCloseRemainingLog(t *testing.T) {
	dir := t.TempDir()

	first, _, _, releaseFirst := acquireSessionLog(dir, "sam", "s")
	_, _, _, releaseSecond := acquireSessionLog(dir, "sam", "s")

	releaseSecond()
	releaseSecond() // idempotent, on purpose

	if _, err := first.Write([]byte("still alive")); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	releaseFirst()

	content, _ := os.ReadFile(sessionLogPath(dir, "sam", "s"))
	if !strings.Contains(string(content), "still alive") {
		t.Error("releasing twice zeroed the count and closed the log of whoever was still attached")
	}
}

// Different sessions do not share a scribe — one's log must not end up in the
// other, nor may one silence the other.
func TestDifferentSessionsEachRecordTheirOwn(t *testing.T) {
	dir := t.TempDir()
	a, _, _, releaseA := acquireSessionLog(dir, "sam", "one")
	b, _, _, releaseB := acquireSessionLog(dir, "sam", "other")

	_, _ = a.Write([]byte("from one"))
	_, _ = b.Write([]byte("from other"))
	releaseA()
	releaseB()

	umA, _ := os.ReadFile(sessionLogPath(dir, "sam", "one"))
	umB, _ := os.ReadFile(sessionLogPath(dir, "sam", "other"))
	if string(umA) != "from one" || string(umB) != "from other" {
		t.Errorf("one=%q other=%q — one session silenced or invaded the other", umA, umB)
	}
}
