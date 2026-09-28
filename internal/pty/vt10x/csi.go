package vt10x

import (
	"fmt"
	"strconv"
	"strings"
)

type csiEscape struct {
	buf  []byte
	args []int
	mode byte
	priv bool
}

func (c *csiEscape) reset() {
	c.buf = c.buf[:0]
	c.args = c.args[:0]
	c.mode = 0
	c.priv = false
}

func (c *csiEscape) put(b byte) bool {
	c.buf = append(c.buf, b)
	if b >= 0x40 && b <= 0x7E || len(c.buf) >= 256 {
		c.parse()
		return true
	}
	return false
}

func (c *csiEscape) parse() {
	c.mode = c.buf[len(c.buf)-1]
	if len(c.buf) == 1 {
		return
	}
	s := string(c.buf)
	c.args = c.args[:0]
	if s[0] == '?' {
		c.priv = true
		s = s[1:]
	}
	s = s[:len(s)-1]
	ss := strings.Split(s, ";")
	for _, p := range ss {
		i, err := strconv.Atoi(p)
		if err != nil {
			break
		}
		c.args = append(c.args, i)
	}
}

func (c *csiEscape) arg(i, def int) int {
	if i >= len(c.args) || i < 0 {
		return def
	}
	return c.args[i]
}

func (c *csiEscape) maxarg(i, def int) int {
	return max(c.arg(i, def), def)
}

func (t *State) handleCSI() {
	c := &t.csi
	switch c.mode {
	default:
		goto unknown
	case '@':
		t.insertBlanks(c.arg(0, 1))
	case 'A':
		t.moveTo(t.cur.X, t.cur.Y-c.maxarg(0, 1))
	case 'B', 'e':
		t.moveTo(t.cur.X, t.cur.Y+c.maxarg(0, 1))
	case 'c':
		if c.arg(0, 0) == 0 {
		}
	case 'C', 'a':
		t.moveTo(t.cur.X+c.maxarg(0, 1), t.cur.Y)
	case 'D':
		t.moveTo(t.cur.X-c.maxarg(0, 1), t.cur.Y)
	case 'E':
		t.moveTo(0, t.cur.Y+c.arg(0, 1))
	case 'F':
		t.moveTo(0, t.cur.Y-c.arg(0, 1))
	case 'g':
		switch c.arg(0, 0) {
		case 0:
			t.tabs[t.cur.X] = false
		case 3:
			for i := range t.tabs {
				t.tabs[i] = false
			}
		default:
			goto unknown
		}
	case 'G', '`':
		t.moveTo(c.arg(0, 1)-1, t.cur.Y)
	case 'H', 'f':
		t.moveAbsTo(c.arg(1, 1)-1, c.arg(0, 1)-1)
	case 'I':
		n := c.arg(0, 1)
		for i := 0; i < n; i++ {
			t.putTab(true)
		}
	case 'J':
		switch c.arg(0, 0) {
		case 0:
			t.clear(t.cur.X, t.cur.Y, t.cols-1, t.cur.Y)
			if t.cur.Y < t.rows-1 {
				t.clear(0, t.cur.Y+1, t.cols-1, t.rows-1)
			}
		case 1:
			if t.cur.Y > 1 {
				t.clear(0, 0, t.cols-1, t.cur.Y-1)
			}
			t.clear(0, t.cur.Y, t.cur.X, t.cur.Y)
		case 2:
			t.clear(0, 0, t.cols-1, t.rows-1)
		default:
			goto unknown
		}
	case 'K':
		switch c.arg(0, 0) {
		case 0:
			t.clear(t.cur.X, t.cur.Y, t.cols-1, t.cur.Y)
		case 1:
			t.clear(0, t.cur.Y, t.cur.X, t.cur.Y)
		case 2:
			t.clear(0, t.cur.Y, t.cols-1, t.cur.Y)
		}
	case 'S':
		t.scrollUp(t.top, c.arg(0, 1))
	case 'T':
		t.scrollDown(t.top, c.arg(0, 1))
	case 'L':
		t.insertBlankLines(c.arg(0, 1))
	case 'l':
		t.setMode(c.priv, false, c.args)
	case 'M':
		t.deleteLines(c.arg(0, 1))
	case 'X':
		t.clear(t.cur.X, t.cur.Y, t.cur.X+c.arg(0, 1)-1, t.cur.Y)
	case 'P':
		t.deleteChars(c.arg(0, 1))
	case 'Z':
		n := c.arg(0, 1)
		for i := 0; i < n; i++ {
			t.putTab(false)
		}
	case 'd':
		t.moveAbsTo(t.cur.X, c.arg(0, 1)-1)
	case 'h':
		t.setMode(c.priv, true, c.args)
	case 'm':
		t.setAttr(c.args)
	case 'n':
		switch c.arg(0, 0) {
		case 5:
			t.w.Write([]byte("\033[0n"))
		case 6:
			t.w.Write([]byte(fmt.Sprintf("\033[%d;%dR", t.cur.Y+1, t.cur.X+1)))
		}
	case 'r':
		if c.priv {
			goto unknown
		} else {
			t.setScroll(c.arg(0, 1)-1, c.arg(1, t.rows)-1)
			t.moveAbsTo(0, 0)
		}
	case 's':
		t.saveCursor()
	case 'u':
		t.restoreCursor()
	}
	return
unknown:
	t.logf("unknown CSI sequence '%c'\n", c.mode)
}
