package pty

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

func readHistory(t *testing.T, dir, user, name string) []string {
	t.Helper()
	d, err := os.ReadFile(sessionHistPath(dir, user, name))
	if err != nil {
		return nil
	}
	var lines []string
	for _, l := range strings.Split(stripANSI(string(d)), "\n") {
		l = strings.TrimRight(l, " \r")
		if l != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

func TestHistoryKeepsWhatScrollsOffScreen(t *testing.T) {
	dir := t.TempDir()
	screen := newSessionScreen(dir, "u", "s")
	screen.resize(40, 10)

	for i := 1; i <= 30; i++ {
		screen.feed([]byte(fmt.Sprintf("L%02d\r\n", i)))
	}
	screen.closeOnce()

	lines := readHistory(t, dir, "u", "s")
	if len(lines) < 18 || len(lines) > 21 {
		t.Fatalf("history with %d lines; expected ~20 (30 written, 10-line screen)", len(lines))
	}
	for i, l := range lines {
		if want := fmt.Sprintf("L%02d", i+1); l != want {
			t.Fatalf("history line %d = %q; wanted %q (wrong order or content)", i, l, want)
		}
	}
}

func TestHistoryDoesNotDuplicateRepaintedOutput(t *testing.T) {
	dir := t.TempDir()
	screen := newSessionScreen(dir, "u", "s")
	screen.resize(40, 10)

	for decoded := 1; decoded <= 3; decoded++ {
		for i := 1; i <= 6; i++ {
			screen.feed([]byte(fmt.Sprintf("frame %d line %d\r\n", decoded, i)))
		}
		if decoded < 3 {
			screen.feed([]byte("\x1b[6A"))
		}
	}
	for i := 0; i < 20; i++ {
		screen.feed([]byte("\r\n"))
	}
	screen.closeOnce()

	lines := readHistory(t, dir, "u", "s")
	counts := map[string]int{}
	for _, l := range lines {
		counts[l]++
	}
	for i := 1; i <= 6; i++ {
		target := fmt.Sprintf("frame 3 line %d", i)
		if n := counts[target]; n != 1 {
			t.Errorf("%q appears %d time(s) in the history; wanted exactly 1", target, n)
		}
	}
	for _, deadLine := range []string{"frame 1 line 1", "frame 2 line 1"} {
		if n := counts[deadLine]; n != 0 {
			t.Errorf("%q appears %d time(s); a frame repainted over is not history", deadLine, n)
		}
	}
}

func TestHistoryHandlesRuneSplitAcrossBlocks(t *testing.T) {
	dir := t.TempDir()
	screen := newSessionScreen(dir, "u", "s")
	screen.resize(40, 4)

	text := []byte("naïve über Straße año\r\n")
	for _, b := range text {
		screen.feed([]byte{b})
	}
	for i := 0; i < 8; i++ {
		screen.feed([]byte("\r\n"))
	}
	screen.closeOnce()

	lines := readHistory(t, dir, "u", "s")
	found := false
	for _, l := range lines {
		if l == "naïve über Straße año" {
			found = true
		}
	}
	if !found {
		t.Errorf("the multibyte text did not survive the chunk boundary; history = %q", lines)
	}
}

func TestHistoryIgnoresAlternateScreen(t *testing.T) {
	dir := t.TempDir()
	screen := newSessionScreen(dir, "u", "s")
	screen.resize(40, 6)

	screen.feed([]byte("before vim\r\n"))
	screen.feed([]byte("\x1b[?1049h"))
	for i := 0; i < 30; i++ {
		screen.feed([]byte(fmt.Sprintf("scratch %d\r\n", i)))
	}
	screen.feed([]byte("\x1b[?1049l"))
	for i := 0; i < 10; i++ {
		screen.feed([]byte("\r\n"))
	}
	screen.closeOnce()

	for _, l := range readHistory(t, dir, "u", "s") {
		if strings.HasPrefix(l, "scratch") {
			t.Errorf("the history kept %q, which is alternate-screen scratch", l)
			break
		}
	}
}

func TestHistoryCutsAtWholeLine(t *testing.T) {
	if i := nextLineIndex([]byte("mid of a line\r\nwhole\r\n")); i != 15 {
		t.Errorf("next-line index = %d; wanted 15", i)
	}
	if i := nextLineIndex([]byte("no break at all")); i != -1 {
		t.Errorf("with no break, should return -1; returned %d", i)
	}
}
