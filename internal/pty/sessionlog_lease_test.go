package pty

import (
	"os"
	"strings"
	"testing"
	"time"
)

// test clock — the lease is a rule about TIME, and testing it with
// `time.Sleep` would trade an assertion for a bet.
func withClock(t *testing.T) *time.Time {
	t.Helper()
	now := time.Unix(1_700_000_000, 0)
	original := sessionClock
	sessionClock = func() time.Time { return now }
	t.Cleanup(func() { sessionClock = original })
	return &now
}

// A SCRIBE THAT STOPPED DELIVERING MUST NOT HOLD THE POST.
//
// The first version elected the scribe by POSITION in the list and only promoted
// the next one when it LEFT. But a connection can stop delivering bytes without
// ever leaving — a hung `HostShell`, a dead `dtach` client, a network cut with
// no FIN. Then nobody writes and **the session log stops**.
//
// And it does not stop in harmless silence: it stops IN THE MIDDLE. Measured on
// the "Panel" session, the log ended exactly at `ESC[H ESC[J` — "erase the whole
// screen" — without the repaint that follows in the other fifteen occurrences of
// the same sequence in the file. The app replays the log to rebuild the screen,
// so it faithfully reproduced "erase everything" and stopped: a black screen,
// with all the content intact one scroll above.
func TestStalledScribeIsReplaced(t *testing.T) {
	now := withClock(t)
	dir := t.TempDir()

	zombie, _, _, releaseZombie := acquireSessionLog(dir, "sam", "Panel")
	defer releaseZombie()
	live, _, _, releaseLive := acquireSessionLog(dir, "sam", "Panel")
	defer releaseLive()

	// The zombie takes the lease by writing the first chunk.
	_, _ = zombie.Write([]byte("before"))
	// ...and stops. The connection does NOT leave — that is what the earlier version missed.

	// The live one tries to write inside the lease: suppressed, or it would duplicate.
	_, _ = live.Write([]byte("|early"))

	// Once the lease has expired, the live one takes over with nobody having left.
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

// WITH BOTH ALIVE, NOTHING DOUBLES.
//
// A session's clients are fed by the SAME `dtach` master and receive the same
// chunk milliseconds apart. With the scribe renewing, no other one comes close
// to finding the lease expired.
func TestTwoLiveConnectionsDoNotDoubleLog(t *testing.T) {
	now := withClock(t)
	dir := t.TempDir()

	app, _, _, releaseApp := acquireSessionLog(dir, "sam", "s")
	defer releaseApp()
	web, _, _, releaseWeb := acquireSessionLog(dir, "sam", "s")
	defer releaseWeb()

	// Ten chunks, both receiving the same one, 5 ms apart.
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

// LEAVING POLITELY RELEASES THE LEASE AT ONCE.
//
// Waiting for the lease to expire would drop the first chunk after a tab closes
// into a hole of a second and a half, for no reason at all: whoever leaves knows
// they are leaving.
func TestExitReleasesLeaseImmediately(t *testing.T) {
	withClock(t)
	dir := t.TempDir()

	first, _, _, releaseFirst := acquireSessionLog(dir, "sam", "s")
	second, _, _, releaseSecond := acquireSessionLog(dir, "sam", "s")
	defer releaseSecond()

	_, _ = first.Write([]byte("A"))
	releaseFirst()
	// The clock is stopped on purpose: with no waiting, the second one already writes.
	_, _ = second.Write([]byte("B"))

	content, _ := os.ReadFile(sessionLogPath(dir, "sam", "s"))
	if string(content) != "AB" {
		t.Errorf("log = %q, wanted \"AB\" — the succession waited for the lease to expire", string(content))
	}
}

// THE CLEAR DTACH SENDS ON ATTACH DOES NOT GO INTO THE LOG.
//
// `dtach` writes `ESC[H ESC[J` onto the new client's screen when it attaches —
// the sequence is literally inside the binary. Addressed to a new screen it is
// correct; recorded into the session log it is poison, because the app replays
// the log to rebuild the screen and faithfully reproduces "erase everything".
//
// Measured on the "Panel" session: the file ended on exactly those six bytes,
// without the repaint that follows in the other fifteen occurrences — and the
// owner saw a black screen with the whole content intact one scroll above.
func TestDtachAttachClearStaysOutOfLog(t *testing.T) {
	withClock(t)
	dir := t.TempDir()

	w, _, _, release := acquireSessionLog(dir, "sam", "Panel")
	defer release()

	// dtach's first chunk: the clear, glued to the start of the real output.
	n, err := w.Write(append([]byte("\x1b[H\x1b[J"), []byte("hey")...))
	if err != nil {
		t.Fatalf("write failed: %v", err)
	}
	if n != 9 {
		t.Errorf("reported %d bytes; whoever writes into the tee must not be able to tell we trimmed", n)
	}
	// After the first chunk, a real clear from the PROGRAM does go through.
	_, _ = w.Write([]byte("\x1b[H\x1b[J|its"))

	content, _ := os.ReadFile(sessionLogPath(dir, "sam", "Panel"))
	if got := string(content); got != "hey\x1b[H\x1b[J|its" {
		t.Errorf("log = %q", got)
	}
}

// A clear ALONE in the first chunk does not go in either — and that is the real
// case: dtach writes only the clear, and the program does not repaint because
// the size did not change.
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

// DTACH'S GOODBYE DOES NOT GO INTO THE LOG EITHER.
//
// On detach, `dtach` writes `ESC[999H \r\n [detached] \r\n`. The
// `ESC[999H` throws the cursor onto the last line and the `\n` there SCROLLS THE
// WHOLE SCREEN up. On the screen of whoever is leaving it is a useful message;
// in the session log it is worse than the attach clear, because it does not only
// erase, it scrolls.
//
// Measured on the "Panel" session: the app's own engine, fed with the log,
// returned 52 blank lines and `[detached]` on line 51. The owner's report, again
// and again: "the screen goes dark, but when you scroll the page the text appears".
func TestDtachFarewellStaysOutOfLog(t *testing.T) {
	withClock(t)
	dir := t.TempDir()

	w, _, _, release := acquireSessionLog(dir, "sam", "Panel")
	defer release()

	_, _ = w.Write([]byte("what the program painted"))
	// The farewell, exactly as dtach sends it: all in one chunk.
	n, err := w.Write([]byte("\x1b[999H\r\n[detached]\r\n\x1b[?25h"))
	if err != nil {
		t.Fatalf("write failed: %v", err)
	}
	if n != 26 {
		t.Errorf("reported %d bytes; whoever writes into the tee must not be able to tell we trimmed", n)
	}
	// And nothing after it goes through.
	_, _ = w.Write([]byte("rest of the goodbye"))

	content, _ := os.ReadFile(sessionLogPath(dir, "sam", "Panel"))
	if got := string(content); got != "what the program painted" {
		t.Errorf("log = %q — the pipe's goodbye got into the program's record", got)
	}
}

// The chunk carrying the goodbye may carry REAL output before it, and that stays.
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

// THE SLICE SERVED TO THE APP DOES NOT END IN DTACH'S NOISE.
//
// The logs ALREADY RECORDED still have the attach clear and the goodbye inside
// them — the owner needs the history and will delete none of it, so fixing the
// past cannot be destructive. The file stays intact; what is trimmed is what GOES OUT.
func TestServedTailDoesNotEndInDtachNoise(t *testing.T) {
	// Exactly the tail measured on the "Panel" session: two clears and the goodbye.
	tail := "\x1b[H\x1b[J\x1b[H\x1b[J\x1b[999H\r\n[detached]\r\n\x1b[?25h"
	got := string(trimTrailingDtachNoise([]byte("what the program painted" + tail)))
	if got != "what the program painted" {
		t.Errorf("slice = %q", got)
	}
}

// An OLD `[detached]`, with real output after it, is legitimate history and has
// to stay: trimming the middle would change what the person saw.
func TestOldFarewellMidLogIsNotTrimmed(t *testing.T) {
	log := "before\x1b[999H\r\n[detached]\r\n" + strings.Repeat("real output after ", 5)
	got := string(trimTrailingDtachNoise([]byte(log)))
	if got != log {
		t.Errorf("trimmed the middle of the log; slice = %q", got)
	}
}
