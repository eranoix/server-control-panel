package pty

import (
	"os"
	"strings"
	"testing"
)

func TestTwoClientsOnSameSessionRecordOnce(t *testing.T) {
	dir := t.TempDir()

	app, _, _, releaseApp := acquireSessionLog(dir, "sam", "App")
	web, _, _, releaseWeb := acquireSessionLog(dir, "sam", "App")

	_, _ = app.Write([]byte("\r\n\r\n"))
	_, _ = web.Write([]byte("\r\n\r\n"))

	releaseWeb()
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

func TestWhenScribeLeavesAnotherTakesOver(t *testing.T) {
	dir := t.TempDir()

	first, _, _, releaseFirst := acquireSessionLog(dir, "sam", "s")
	second, _, _, releaseSecond := acquireSessionLog(dir, "sam", "s")
	defer releaseSecond()

	_, _ = first.Write([]byte("A"))
	_, _ = second.Write([]byte("A"))

	releaseFirst()

	_, _ = second.Write([]byte("B"))

	content, _ := os.ReadFile(sessionLogPath(dir, "sam", "s"))
	if string(content) != "AB" {
		t.Errorf("log = %q, wanted \"AB\" — either it doubled, or the succession did not happen", string(content))
	}
}

func TestDoubleReleaseDoesNotCloseRemainingLog(t *testing.T) {
	dir := t.TempDir()

	first, _, _, releaseFirst := acquireSessionLog(dir, "sam", "s")
	_, _, _, releaseSecond := acquireSessionLog(dir, "sam", "s")

	releaseSecond()
	releaseSecond()

	if _, err := first.Write([]byte("still alive")); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	releaseFirst()

	content, _ := os.ReadFile(sessionLogPath(dir, "sam", "s"))
	if !strings.Contains(string(content), "still alive") {
		t.Error("releasing twice zeroed the count and closed the log of whoever was still attached")
	}
}

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
