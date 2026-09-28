package vt10x

func isControlCode(c rune) bool {
	return c < 0x20 || c == 0177
}

func (t *State) parse(c rune) {
	t.logf("%q", string(c))
	if isControlCode(c) {
		if t.handleControlCodes(c) || t.cur.Attr.Mode&attrGfx == 0 {
			return
		}
	}

	if t.mode&ModeWrap != 0 && t.cur.State&cursorWrapNext != 0 {
		t.lines[t.cur.Y][t.cur.X].Mode |= attrWrap
		t.newline(true)
	}

	if t.mode&ModeInsert != 0 && t.cur.X+1 < t.cols {
		t.logln("insert mode not implemented")
	}

	t.setChar(c, &t.cur.Attr, t.cur.X, t.cur.Y)
	if t.cur.X+1 < t.cols {
		t.moveTo(t.cur.X+1, t.cur.Y)
	} else {
		t.cur.State |= cursorWrapNext
	}
}

func (t *State) parseEsc(c rune) {
	if t.handleControlCodes(c) {
		return
	}
	next := t.parse
	t.logf("%q", string(c))
	switch c {
	case '[':
		next = t.parseEscCSI
	case '#':
		next = t.parseEscTest
	case 'P',
		'_',
		'^',
		']',
		'k':
		t.str.reset()
		t.str.typ = c
		next = t.parseEscStr
	case '(':
		next = t.parseEscAltCharset
	case ')',
		'*',
		'+':
	case 'D':
		if t.cur.Y == t.bottom {
			t.scrollUp(t.top, 1)
		} else {
			t.moveTo(t.cur.X, t.cur.Y+1)
		}
	case 'E':
		t.newline(true)
	case 'H':
		t.tabs[t.cur.X] = true
	case 'M':
		if t.cur.Y == t.top {
			t.scrollDown(t.top, 1)
		} else {
			t.moveTo(t.cur.X, t.cur.Y-1)
		}
	case 'Z':
	case 'c':
		t.reset()
	case '=':
		t.mode |= ModeAppKeypad
	case '>':
		t.mode &^= ModeAppKeypad
	case '7':
		t.saveCursor()
	case '8':
		t.restoreCursor()
	case '\\':
	default:
		t.logf("unknown ESC sequence '%c'\n", c)
	}
	t.state = next
}

func (t *State) parseEscCSI(c rune) {
	if t.handleControlCodes(c) {
		return
	}
	t.logf("%q", string(c))
	if t.csi.put(byte(c)) {
		t.state = t.parse
		t.handleCSI()
	}
}

func (t *State) parseEscStr(c rune) {
	t.logf("%q", string(c))
	switch c {
	case '\033':
		t.state = t.parseEscStrEnd
	case '\a':
		t.state = t.parse
		t.handleSTR()
	default:
		t.str.put(c)
	}
}

func (t *State) parseEscStrEnd(c rune) {
	if t.handleControlCodes(c) {
		return
	}
	t.logf("%q", string(c))
	t.state = t.parse
	if c == '\\' {
		t.handleSTR()
	}
}

func (t *State) parseEscAltCharset(c rune) {
	if t.handleControlCodes(c) {
		return
	}
	t.logf("%q", string(c))
	switch c {
	case '0':
		t.cur.Attr.Mode |= attrGfx
	case 'B':
		t.cur.Attr.Mode &^= attrGfx
	case 'A',
		'<',
		'5',
		'C',
		'K':
	default:
		t.logf("unknown alt. charset '%c'\n", c)
	}
	t.state = t.parse
}

func (t *State) parseEscTest(c rune) {
	if t.handleControlCodes(c) {
		return
	}
	if c == '8' {
		for y := 0; y < t.rows; y++ {
			for x := 0; x < t.cols; x++ {
				t.setChar('E', &t.cur.Attr, x, y)
			}
		}
	}
	t.state = t.parse
}

func (t *State) handleControlCodes(c rune) bool {
	if !isControlCode(c) {
		return false
	}
	switch c {
	case '\t':
		t.putTab(true)
	case '\b':
		t.moveTo(t.cur.X-1, t.cur.Y)
	case '\r':
		t.moveTo(0, t.cur.Y)
	case '\f', '\v', '\n':
		t.newline(t.mode&ModeCRLF != 0)
	case '\a':
	case 033:
		t.csi.reset()
		t.state = t.parseEsc
	case 016, 017:
	case 032, 030:
		t.csi.reset()
	case 005, 000, 021, 023, 0177:
	default:
		return false
	}
	return true
}
