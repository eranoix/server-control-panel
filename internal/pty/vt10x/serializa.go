package vt10x

// serializa.go — a vps-manager ADDITION. See `vpsm.go`.
//
// Converts a line of glyphs back into terminal bytes: the text with the
// minimal SGR sequences needed to reproduce the colours and attributes.
//
// ## Why this exists, instead of keeping the original bytes
//
// The original bytes of a session with Claude Code are not history: they are the
// record of a program that REDRAWS. Replaying them on a new grid makes
// `ESC[nA` saturate at the top of the SCREEN — it does not reach the scrollback —
// and the previous copy stays there, below the new one. That is the duplication the owner reported.
//
// The line that LEFT the screen, on the other hand, is already the result: the program
// has finished working on it. Serialized back, it is append-only text, which
// any terminal reproduces with no ambiguity at all.

import (
	"bytes"
	"strconv"
)

// EmBytes serialises a line of glyphs into terminal bytes, ending in `\r\n`.
// Trailing spaces are dropped: the grid is rectangular, the history does not
// have to be.
func EmBytes(line []Glyph) []byte {
	done := len(line)
	for done > 0 {
		g := line[done-1]
		if g.Char != ' ' && g.Char != 0 || g.BG != DefaultBG {
			break
		}
		done--
	}
	var buf bytes.Buffer
	curFG, curBG, curMode := DefaultFG, DefaultBG, int16(0)
	for i := 0; i < done; i++ {
		g := line[i]
		if g.FG != curFG || g.BG != curBG || g.Mode != curMode {
			buf.Write(sgr(g.FG, g.BG, g.Mode))
			curFG, curBG, curMode = g.FG, g.BG, g.Mode
		}
		c := g.Char
		if c == 0 {
			c = ' '
		}
		buf.WriteRune(c)
	}
	if curFG != DefaultFG || curBG != DefaultBG || curMode != 0 {
		buf.WriteString("\x1b[0m")
	}
	buf.WriteString("\r\n")
	return buf.Bytes()
}

// CropToBytes serialises only the columns [de, de+quantas) of the line — the
// crop a client narrower than the session is able to show.
//
// Cropping instead of shrinking the session is the choice this file exists to
// make possible: the program keeps painting at the wider client's width, and the
// narrower one sees a piece of it. Shrinking the session to fit the smallest is
// what made the phone shrink the desktop.
func CropToBytes(line []Glyph, de, count int) []byte {
	if de < 0 {
		de = 0
	}
	if de >= len(line) || count <= 0 {
		return []byte{}
	}
	upTo := de + count
	if upTo > len(line) {
		upTo = len(line)
	}
	return withoutNewline(EmBytes(line[de:upTo]))
}

// withoutNewline strips the \r\n from the end: what positions the line is the caller,
// with absolute addressing. Leaving the break in would scroll the screen on every line.
func withoutNewline(b []byte) []byte {
	return bytes.TrimSuffix(b, []byte("\r\n"))
}

// sgr builds the sequence that takes the terminal from the clean state to the
// requested one. It always starts with a reset: that is shorter than computing
// the exact difference and it cannot diverge from the real state of the reader's
// terminal.
func sgr(fg, bg Color, mode int16) []byte {
	var buf bytes.Buffer
	buf.WriteString("\x1b[0")
	if mode&attrBold != 0 {
		buf.WriteString(";1")
	}
	if mode&attrItalic != 0 {
		buf.WriteString(";3")
	}
	if mode&attrUnderline != 0 {
		buf.WriteString(";4")
	}
	if mode&attrBlink != 0 {
		buf.WriteString(";5")
	}
	if mode&attrReverse != 0 {
		buf.WriteString(";7")
	}
	writeColor(&buf, fg, true)
	writeColor(&buf, bg, false)
	buf.WriteString("m")
	return buf.Bytes()
}

func writeColor(buf *bytes.Buffer, c Color, foreground bool) {
	if (foreground && c == DefaultFG) || (!foreground && c == DefaultBG) {
		return
	}
	if c >= 1<<24 { // other special colours (cursor): they have no SGR of their own
		return
	}
	base := 48
	if foreground {
		base = 38
	}
	buf.WriteString(";")
	buf.WriteString(strconv.Itoa(base))
	buf.WriteString(";5;")
	buf.WriteString(strconv.FormatUint(uint64(c), 10))
}
