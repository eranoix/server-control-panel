package pty

import (
	"os"
	"strings"
	"testing"
	"time"
)

func withClock(t *testing.T) *time.Time {
	t.Helper()
	now := time.Unix(1_700_000_000, 0)
	original := sessionClock
	sessionClock = func() time.Time { return now }
	t.Cleanup(func() { sessionClock = original })
	return &now
}

func TestStalledScribeIsReplaced(t *testing.T) {
	now := withClock(t)
	dir := t.TempDir()

	zombie, _, _, releaseZombie := acquireSessionLog(dir, "sam", "Panel")
	defer releaseZombie()
	live, _, _, releaseLive := acquireSessionLog(dir, "sam", "Panel")
	defer releaseLive()

	_, _ = zombie.Write([]byte("before"))

	_, _ = live.Write([]byte("|early"))

	*now = now.Add(leaseValidity + time.Millisecond)
	_, _ = live.Write([]byte("|after"))

	content, err := os.ReadFile(sessionLogPath(dir, "sam", "Panel"))
	if err != nil {
		t.Fatalf("log was not written: %v", err)
	}
	if got := string(content); got != "before|after" {
		t.Errorf("log = %q, want \"before|after\"", got)
	}
}

func TestTwoLiveConnectionsDoNotDoubleLog(t *testing.T) {
	now := withClock(t)
	dir := t.TempDir()

	app, _, _, releaseApp := acquireSessionLog(dir, "sam", "s")
	defer releaseApp()
	web, _, _, releaseWeb := acquireSessionLog(dir, "sam", "s")
	defer releaseWeb()

	for i := 0; i < 10; i++ {
		_, _ = app.Write([]byte("\r\n"))
		*now = now.Add(5 * time.Millisecond)
		_, _ = web.Write([]byte("\r\n"))
		*now = now.Add(45 * time.Millisecond)
	}

	content, _ := os.ReadFile(sessionLogPath(dir, "sam", "s"))
	if got := strings.Count(string(content), "\r\n"); got != 10 {
		t.Errorf("wrote %d line breaks, wanted 10 — doubling the ones scrolled"+
			" pushes the whole screen into the history and the app opens black", got)
	}
}

func TestExitReleasesLeaseImmediately(t *testing.T) {
	withClock(t)
	dir := t.TempDir()

	first, _, _, releaseFirst := acquireSessionLog(dir, "sam", "s")
	second, _, _, releaseSecond := acquireSessionLog(dir, "sam", "s")
	defer releaseSecond()

	_, _ = first.Write([]byte("A"))
	releaseFirst()
	_, _ = second.Write([]byte("B"))

	content, _ := os.ReadFile(sessionLogPath(dir, "sam", "s"))
	if string(content) != "AB" {
		t.Errorf("log = %q, wanted \"AB\" — the succession waited for the lease to expire", string(content))
	}
}

func TestDtachAttachClearStaysOutOfLog(t *testing.T) {
	withClock(t)
	dir := t.TempDir()

	w, _, _, release := acquireSessionLog(dir, "sam", "Panel")
	defer release()

	n, err := w.Write(append([]byte("\x1b[H\x1b[J"), []byte("hey")...))
	if err != nil {
		t.Fatalf("write failed: %v", err)
	}
	if n != 9 {
		t.Errorf("reported %d bytes; whoever writes into the tee must not be able to tell we trimmed", n)
	}
	_, _ = w.Write([]byte("\x1b[H\x1b[J|its"))

	content, _ := os.ReadFile(sessionLogPath(dir, "sam", "Panel"))
	if got := string(content); got != "hey\x1b[H\x1b[J|its" {
		t.Errorf("log = %q", got)
	}
}

func TestLoneClearInFirstBlockVanishes(t *testing.T) {
	withClock(t)
	dir := t.TempDir()

	w, _, _, release := acquireSessionLog(dir, "sam", "s")
	defer release()
	_, _ = w.Write([]byte("antes"))
	release()

	w2, _, _, release2 := acquireSessionLog(dir, "sam", "s")
	defer release2()
	if n, _ := w2.Write([]byte("\x1b[H\x1b[J")); n != 6 {
		t.Errorf("reported %d; want 6", n)
	}

	content, _ := os.ReadFile(sessionLogPath(dir, "sam", "s"))
	if string(content) != "antes" {
		t.Errorf("log = %q — the attach erased the recorded screen", string(content))
	}
}

func TestDtachFarewellStaysOutOfLog(t *testing.T) {
	withClock(t)
	dir := t.TempDir()

	w, _, _, release := acquireSessionLog(dir, "sam", "Panel")
	defer release()

	_, _ = w.Write([]byte("what the program painted"))
	n, err := w.Write([]byte("\x1b[999H\r\n[detached]\r\n\x1b[?25h"))
	if err != nil {
		t.Fatalf("write failed: %v", err)
	}
	if n != 26 {
		t.Errorf("reported %d bytes; whoever writes into the tee must not be able to tell we trimmed", n)
	}
	_, _ = w.Write([]byte("rest of the goodbye"))

	content, _ := os.ReadFile(sessionLogPath(dir, "sam", "Panel"))
	if got := string(content); got != "what the program painted" {
		t.Errorf("log = %q — the pipe's goodbye got into the program's record", got)
	}
}

func TestContentBeforeFarewellIsPreserved(t *testing.T) {
	withClock(t)
	dir := t.TempDir()

	w, _, _, release := acquireSessionLog(dir, "sam", "s")
	defer release()

	_, _ = w.Write([]byte("last line of the program\x1b[999H\r\n[detached]\r\n"))

	content, _ := os.ReadFile(sessionLogPath(dir, "sam", "s"))
	if got := string(content); got != "last line of the program" {
		t.Errorf("log = %q", got)
	}
}

func TestServedTailDoesNotEndInDtachNoise(t *testing.T) {
	tail := "\x1b[H\x1b[J\x1b[H\x1b[J\x1b[999H\r\n[detached]\r\n\x1b[?25h"
	got := string(trimTrailingDtachNoise([]byte("what the program painted" + tail)))
	if got != "what the program painted" {
		t.Errorf("slice = %q", got)
	}
}

func TestOldFarewellMidLogIsNotTrimmed(t *testing.T) {
	log := "before\x1b[999H\r\n[detached]\r\n" + strings.Repeat("real output after ", 5)
	got := string(trimTrailingDtachNoise([]byte(log)))
	if got != log {
		t.Errorf("trimmed the middle of the log; slice = %q", got)
	}
}
