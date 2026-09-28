package vt10x

import (
	"bytes"
	"strconv"
)

func LineBytes(line []Glyph) []byte {
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

func CropToBytes(line []Glyph, from, count int) []byte {
	if from < 0 {
		from = 0
	}
	if from >= len(line) || count <= 0 {
		return []byte{}
	}
	upTo := from + count
	if upTo > len(line) {
		upTo = len(line)
	}
	return withoutNewline(LineBytes(line[from:upTo]))
}

func withoutNewline(b []byte) []byte {
	return bytes.TrimSuffix(b, []byte("\r\n"))
}

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
	if c >= 1<<24 {
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
