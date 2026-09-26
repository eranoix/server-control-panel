package vt10x

// vpsm.go — WHAT WE ADDED TO THE VENDORED EMULATOR.
//
// The rest of this directory is `github.com/hinshun/vt10x` (MIT, see LICENSE),
// copied unchanged except for the patch marked in `scrollUp` (state.go). Only
// the CORE files came across: the original package also ships a terminal wired
// to a real pty, which is of no use here — our pty is a different one.
//
// ## Why vendor instead of depend
//
// What the server needs does not exist in the public API of any emulator that
// works in Go today: the lines that LEAVE the screen. An ordinary emulator
// discards them — they are the problem of whatever terminal holds the
// scrollback. Here they are the product: they are the session's true history.
//
// (`charmbracelet/x/vt` has scrollback in its API and was the first choice. It
// does not render — measured: fed "ola mundo" it returns an empty screen, which
// matches the warning the package itself carries in its own documentation.)
//
// The patch is ten lines with a single entry point. Preferring that to a fork
// maintained elsewhere, or to an emulator written from scratch, is the
// smallest-surface choice — and it stays visible here instead of hidden in a go.mod.

import (
	"bytes"
	"io"
	"unicode"
)

// Novo returns a screen emulator ready to receive pty bytes. It does what the
// original package's `newTerminal` did, without dragging along the pty-attached
// terminal that came with it.
func Novo(cols, rows int) *State {
	t := newState(io.Discard)
	t.numlock = true
	t.state = t.parse
	t.cur.Attr.FG = DefaultFG
	t.cur.Attr.BG = DefaultBG
	t.resize(cols, rows)
	t.reset()
	return t
}

// AoRolarParaFora registers who receives the lines that leave through the top of
// the screen — the session's history. It receives them in the order they left.
//
// The callback is invoked with the emulator's lock HELD: whoever receives it
// must copy what it needs and get out. Serialising in there is cheap (it is
// text); doing network I/O would not be.
func (t *State) AoRolarParaFora(fn func(linhas [][]Glyph)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.aoRolarParaFora = fn
}

// Write feeds the emulator. The original package exposed this through the
// `terminal` type, which did not come across; this is the same implementation.
//
// IT RETURNS LESS THAN len(p) when the chunk ends in the middle of a UTF-8
// sequence — and that is no detail: the recorder hands over 32 KiB chunks taken
// from whatever `read()` returned, and a multibyte character straddles that
// boundary all the time. The caller has to carry the remainder into the next
// chunk, otherwise every boundary becomes a wrong character in the history.
func (t *State) Write(p []byte) (int, error) {
	var escritos int
	r := bytes.NewReader(p)
	t.lock()
	defer t.unlock()
	for {
		c, sz, err := r.ReadRune()
		if err != nil {
			if err == io.EOF {
				break
			}
			return escritos, err
		}
		escritos += sz
		if c == unicode.ReplacementChar && sz == 1 {
			if r.Len() == 0 {
				// Not enough bytes for the whole rune: return without consuming it.
				return escritos - 1, nil
			}
			continue // a genuinely invalid sequence: skip it
		}
		t.put(c)
	}
	return escritos, nil
}

// Redimensiona fits the emulator's grid to the session's effective size.
func (t *State) Redimensiona(cols, rows int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.resize(cols, rows)
}

// TelaAtual returns a copy of the VISIBLE lines of the screen.
//
// The history (`AoRolarParaFora`) covers what has already LEFT; this covers what
// is still in view. An ordinary shell redraws nothing when a new client attaches
// — `bash` only repaints the prompt line — so without this snapshot the last
// full screen would have no way of reaching whoever has just opened the session.
func (t *State) TelaAtual() [][]Glyph {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([][]Glyph, 0, t.rows)
	for y := 0; y < t.rows && y < len(t.lines); y++ {
		linha := make([]Glyph, len(t.lines[y]))
		copy(linha, t.lines[y])
		out = append(out, linha)
	}
	return out
}

// CursorAtual and CursorVisivel: the frame compositor needs to put the cursor in
// the right place of the client's window, and the original package only exposes
// that without a lock (its `Cursor()` reads `t.cur` directly).
func (t *State) CursorAtual() Cursor {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cur
}

func (t *State) CursorVisivel() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.mode&ModeHide == 0
}

// Tamanho returns the screen's grid — the SESSION's grid, which is what each
// client's crop is computed against.
func (t *State) Tamanho() (cols, rows int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cols, t.rows
}

// EmAltScreen reports whether the alternate screen is in use (vim, htop). While
// it is on, what scrolls is not session history — it is a full-screen program's
// scratch, and recording it into the history would fill the file with junk.
func (t *State) EmAltScreen() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.mode&ModeAltScreen != 0
}
