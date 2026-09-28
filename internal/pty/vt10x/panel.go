package vt10x

import (
	"bytes"
	"io"
	"unicode"
)

func New(cols, rows int) *State {
	t := newState(io.Discard)
	t.numlock = true
	t.state = t.parse
	t.cur.Attr.FG = DefaultFG
	t.cur.Attr.BG = DefaultBG
	t.resize(cols, rows)
	t.reset()
	return t
}

func (t *State) OnScrollOut(fn func(lines [][]Glyph)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.onScrollOut = fn
}

func (t *State) Write(p []byte) (int, error) {
	var written int
	r := bytes.NewReader(p)
	t.lock()
	defer t.unlock()
	for {
		c, sz, err := r.ReadRune()
		if err != nil {
			if err == io.EOF {
				break
			}
			return written, err
		}
		written += sz
		if c == unicode.ReplacementChar && sz == 1 {
			if r.Len() == 0 {
				return written - 1, nil
			}
			continue
		}
		t.put(c)
	}
	return written, nil
}

func (t *State) Resize(cols, rows int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.resize(cols, rows)
}

func (t *State) CurrentScreen() [][]Glyph {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([][]Glyph, 0, t.rows)
	for y := 0; y < t.rows && y < len(t.lines); y++ {
		line := make([]Glyph, len(t.lines[y]))
		copy(line, t.lines[y])
		out = append(out, line)
	}
	return out
}

func (t *State) LockedCursor() Cursor {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cur
}

func (t *State) LockedCursorVisible() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.mode&ModeHide == 0
}

func (t *State) LockedSize() (cols, rows int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cols, t.rows
}

func (t *State) InAltScreen() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.mode&ModeAltScreen != 0
}
