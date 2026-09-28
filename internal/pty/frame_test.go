package pty

import (
	"bytes"
	"strings"
	"testing"

	"server-control-panel/internal/pty/vt10x"
)

func screenWithText(cols int, lines ...string) [][]vt10x.Glyph {
	out := make([][]vt10x.Glyph, 0, len(lines))
	for _, l := range lines {
		line := make([]vt10x.Glyph, cols)
		r := []rune(l)
		for i := 0; i < cols; i++ {
			c := ' '
			if i < len(r) {
				c = r[i]
			}
			line[i] = vt10x.Glyph{Char: c, FG: vt10x.DefaultFG, BG: vt10x.DefaultBG}
		}
		out = append(out, line)
	}
	return out
}

func withoutEscapes(b []byte) string { return stripANSI(string(b)) }

func TestFrameFirstIsFull(t *testing.T) {
	q := newClientFrame(20, 3)
	output := q.update(screenWithText(40, "first", "second", "third"), vt10x.Cursor{X: 0, Y: 0}, true)
	text := withoutEscapes(output)
	for _, target := range []string{"first", "second", "third"} {
		if !strings.Contains(text, target) {
			t.Errorf("the first frame did not bring %q: %q", target, text)
		}
	}
	if !bytes.Contains(output, []byte("\x1b[H\x1b[2J")) {
		t.Error("the first frame has to clear the client's screen before painting")
	}
}

func TestFrameSendsOnlyChangedLine(t *testing.T) {
	q := newClientFrame(20, 3)
	screen := screenWithText(40, "alpha", "beta", "gamma")
	q.update(screen, vt10x.Cursor{}, true)

	if s := q.update(screen, vt10x.Cursor{}, true); s != nil {
		t.Errorf("identical screen generated %d bytes; wanted nothing", len(s))
	}

	screen2 := screenWithText(40, "alpha", "BETA!", "gamma")
	output := q.update(screen2, vt10x.Cursor{}, true)
	text := withoutEscapes(output)
	if !strings.Contains(text, "BETA!") {
		t.Errorf("the line that changed was not sent: %q", text)
	}
	if strings.Contains(text, "alpha") || strings.Contains(text, "gamma") {
		t.Errorf("sent a line that did not change: %q", text)
	}
	if !bytes.Contains(output, []byte("\x1b[2;1H")) {
		t.Error("the line has to go with absolute addressing for line 2")
	}
}

func TestFrameScrollDoesNotRepaintWholeScreen(t *testing.T) {
	q := newClientFrame(20, 4)
	q.update(screenWithText(40, "one", "two", "three", "four"), vt10x.Cursor{}, true)

	scroll := q.scrolled(1)
	if !bytes.Contains(scroll, []byte("\x1b[4;1H")) || !bytes.Contains(scroll, []byte("\r\n")) {
		t.Fatalf("the scroll has to go to the last line and break: %q", scroll)
	}

	output := q.update(screenWithText(40, "two", "three", "four", "five"), vt10x.Cursor{}, true)
	text := withoutEscapes(output)
	if !strings.Contains(text, "five") {
		t.Errorf("the new line was not sent: %q", text)
	}
	for _, old := range []string{"two", "three", "four"} {
		if strings.Contains(text, old) {
			t.Errorf("repainted %q, which the client already had after scrolling: %q", old, text)
		}
	}
}

func TestFrameCropsAndShifts(t *testing.T) {
	q := newClientFrame(10, 1)
	screen := screenWithText(40, "0123456789ABCDEFGHIJ")

	esq := withoutEscapes(q.update(screen, vt10x.Cursor{}, true))
	if !strings.Contains(esq, "0123456789") || strings.Contains(esq, "ABCDEF") {
		t.Errorf("the left crop came out wrong: %q", esq)
	}

	q.shift(10, 40)
	dir := withoutEscapes(q.update(screen, vt10x.Cursor{}, true))
	if !strings.Contains(dir, "ABCDEFGHIJ") {
		t.Errorf("the pan did not reach the right side: %q", dir)
	}
}

func TestFrameShiftStaysWithinBounds(t *testing.T) {
	q := newClientFrame(10, 1)
	q.shift(-5, 40)
	if q.offset != 0 {
		t.Errorf("a negative pan became %d; want 0", q.offset)
	}
	q.shift(999, 40)
	if q.offset != 30 {
		t.Errorf("pan past the end became %d; wanted 30 (40-10)", q.offset)
	}
	q.shift(999, 5)
	if q.offset != 0 {
		t.Errorf("with a session smaller than the window the pan has to be 0; became %d", q.offset)
	}
}

func TestFrameResizeRepaintsEverything(t *testing.T) {
	q := newClientFrame(20, 2)
	screen := screenWithText(40, "alpha", "beta")
	q.update(screen, vt10x.Cursor{}, true)
	q.resize(30, 2)
	output := withoutEscapes(q.update(screen, vt10x.Cursor{}, true))
	if !strings.Contains(output, "alpha") || !strings.Contains(output, "beta") {
		t.Errorf("after resizing, the frame has to come whole: %q", output)
	}
}

func TestFramePlacesCursorLast(t *testing.T) {
	q := newClientFrame(20, 3)
	output := q.update(screenWithText(40, "a", "b", "c"), vt10x.Cursor{X: 5, Y: 1}, true)
	i := bytes.LastIndex(output, []byte("\x1b[2;6H"))
	if i < 0 {
		t.Fatalf("did not find the cursor positioning: %q", output)
	}
	if bytes.Contains(output[i:], []byte(";1H")) {
		t.Error("a line came after the cursor — it has to be the last thing")
	}
}

func TestFrameCursorFollowsShift(t *testing.T) {
	q := newClientFrame(10, 1)
	q.shift(10, 40)
	output := q.update(screenWithText(40, "0123456789ABCDEFGHIJ"), vt10x.Cursor{X: 12, Y: 0}, true)
	if !bytes.Contains(output, []byte("\x1b[1;3H")) {
		t.Errorf("cursor at column 12 of the session, with the crop starting at 10, has to come out at 3: %q", output)
	}
}

func TestFrameVerticalCropTakesEnd(t *testing.T) {
	q := newClientFrame(20, 2)
	screen := screenWithText(40, "one", "two", "three", "four", "five")
	text := withoutEscapes(q.update(screen, vt10x.Cursor{X: 0, Y: 4}, true))
	if !strings.Contains(text, "four") || !strings.Contains(text, "five") {
		t.Errorf("the crop has to bring the LAST lines: %q", text)
	}
	if strings.Contains(text, "one") || strings.Contains(text, "two") {
		t.Errorf("the crop brought a line from the top: %q", text)
	}
}

func TestFrameCursorFollowsVerticalCrop(t *testing.T) {
	q := newClientFrame(20, 2)
	screen := screenWithText(40, "one", "two", "three", "four", "five")
	output := q.update(screen, vt10x.Cursor{X: 3, Y: 4}, true)
	if !bytes.Contains(output, []byte("\x1b[2;4H")) {
		t.Errorf("cursor on line 5 of the session, window of 2, has to come out on line 2: %q", output)
	}
}

func TestFrameWithEmptyBottomShowsTop(t *testing.T) {
	q := newClientFrame(20, 3)
	screen := screenWithText(40, "prompt$ echo hi", "hi", "", "", "", "", "", "", "", "")
	text := withoutEscapes(q.update(screen, vt10x.Cursor{X: 0, Y: 1}, true))
	if !strings.Contains(text, "prompt$ echo hi") || !strings.Contains(text, "hi") {
		t.Errorf("with the cursor at the top, the crop has to show the top: %q", text)
	}
}

func TestFrameAnchorDoesNotMoveNeedlessly(t *testing.T) {
	q := newClientFrame(20, 3)
	screen := screenWithText(40, "one", "two", "three", "four", "five")
	q.update(screen, vt10x.Cursor{Y: 4}, true)
	anchorBefore := q.anchor
	q.update(screen, vt10x.Cursor{Y: 3}, true)
	if q.anchor != anchorBefore {
		t.Errorf("the anchor moved (%d -> %d) while the cursor was still visible", anchorBefore, q.anchor)
	}
}
