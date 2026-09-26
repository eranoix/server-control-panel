package pty

// quadro.go — WHAT EACH CLIENT SEES, COMPOSED FOR IT.
//
// ## The problem this solves
//
// `dtach` is a pipe: the PTY has ONE size and every client receives the same
// bytes. That is why the session sat at the SMALLEST among those attached — and
// the app on the phone, at 53 columns, shrank the 120-column desktop's session.
// Earlier work made that correct and reversible; it did not make it acceptable.
//
// The way out is the classic multiplexer one, and it only became possible once
// the server had a SCREEN (`historico.go`): with a live emulator per session,
// the server can COMPOSE what each client should see instead of relaying the
// same stream to everyone.
//
// ## The decision that keeps this small
//
// The frame is composed as TERMINAL BYTES — absolute positioning and serialised
// lines. The client writes those bytes into its own emulator exactly as it
// writes the PTY's, so THERE IS NO NEW PROTOCOL: not in the panel, not in the
// app, not on the recovery screen. A per-client frame that demanded a new
// decoder in every client would be one feature with three implementations to
// maintain; this way there is one, and it lives on the side that has the screen.
//
// ## Why a per-line diff, and not the whole screen
//
// A 53x45 crop repainted in full costs a few KiB; at 10 frames per second that
// is tens of KiB/s on the phone, which is exactly what the data-saving work
// exists to prevent. Comparing line by line, typing costs one line and the rest
// of the time costs nothing.
//
// ## And scrolling is handled as scrolling, not as a repaint
//
// When the session scrolls k lines, nearly every line changes place — a naive
// diff would repaint the whole screen. Instead the compositor tells the client's
// terminal to SCROLL k lines (which pushes ITS content into ITS own scrollback,
// for free) and only then compares what is left. That is what keeps the history
// scrollable on the client without sending anything twice.

import (
	"bytes"
	"strconv"

	"server-control-panel/internal/pty/vt10x"
)

// quadroDoCliente holds what this client already has on screen and composes what
// is missing. It is not thread-safe: each connection has its own, and only the
// goroutine serving that connection touches it.
type quadroDoCliente struct {
	cols, rows int
	// desloc: which SESSION column the crop starts at. It exists so a person can
	// reach what is to the right in a window narrower than the session; without
	// it, the right half would be unreachable.
	desloc int
	// ancora: the first SESSION line visible in this window. It persists between
	// frames — see the block in [atualiza].
	ancora int
	// base: the lines the client already has, serialised and cropped. nil means
	// "I do not know what it has" — and then the line is sent.
	base [][]byte
	// primeiro: nothing has been sent yet, so the whole frame goes out.
	primeiro bool
}

func novoQuadroDoCliente(cols, rows int) *quadroDoCliente {
	return &quadroDoCliente{cols: cols, rows: rows, primeiro: true}
}

// redimensiona adjusts the client's window. It discards what we knew: the
// coordinates have changed, and sending a diff against a base of another size
// would write a line in the wrong place.
func (q *quadroDoCliente) redimensiona(cols, rows int) {
	if cols == q.cols && rows == q.rows {
		return
	}
	q.cols, q.rows = cols, rows
	q.base = nil
	q.primeiro = true
}

// desloca pans the crop horizontally, clamped to the session's bounds.
func (q *quadroDoCliente) desloca(para, colsDaSessao int) {
	if para < 0 {
		para = 0
	}
	if max := colsDaSessao - q.cols; para > max {
		if max < 0 {
			max = 0
		}
		para = max
	}
	if para == q.desloc {
		return
	}
	q.desloc = para
	q.base = nil
	q.primeiro = true
}

// rolou composes a scroll of k lines: it tells the client's terminal to scroll
// (its content goes into ITS own scrollback) and shifts the base by as much.
func (q *quadroDoCliente) rolou(k int) []byte {
	if k <= 0 || q.primeiro {
		return nil // on the first frame there is nothing to scroll: it comes whole
	}
	if k >= q.rows {
		// It scrolled further than the screen: nothing it had still holds.
		q.base = nil
		q.primeiro = true
		return nil
	}
	var buf bytes.Buffer
	// Go to the last line and break k times: that is how you scroll a terminal
	// without erasing anything — and it is what pushes the content into the
	// client's own scrollback.
	buf.WriteString("\x1b[")
	buf.WriteString(strconv.Itoa(q.rows))
	buf.WriteString(";1H")
	for i := 0; i < k; i++ {
		buf.WriteString("\r\n")
	}
	if len(q.base) > k {
		q.base = append(q.base[:0], q.base[k:]...)
	} else {
		q.base = nil
	}
	// The k lines at the bottom become unknown.
	for len(q.base) < q.rows {
		q.base = append(q.base, nil)
	}
	return buf.Bytes()
}

// atualiza composes the difference between the session's screen and what the
// client has. Returns nil when there is nothing to send.
func (q *quadroDoCliente) atualiza(tela [][]vt10x.Glyph, cur vt10x.Cursor, cursorVisivel bool) []byte {
	if q.cols < 2 || q.rows < 1 {
		return nil
	}
	var buf bytes.Buffer
	if q.primeiro {
		// The whole frame: clear and paint. It is the only moment when erasing the
		// client's screen is correct — it has just entered frame mode and whatever
		// was there corresponds to nothing.
		buf.WriteString("\x1b[H\x1b[2J")
		q.base = make([][]byte, q.rows)
		q.primeiro = false
	}
	// ── THE VERTICAL CROP FOLLOWS THE CURSOR ─────────────────────────────
	//
	// I got this wrong TWICE, and each mistake was caught by a different test —
	// which is the entire argument for having both:
	//
	//  1. Cropping from the TOP: the small window showed the beginning of the
	//     session and saw nothing of what was being typed. The browser caught it.
	//  2. Cropping from the BOTTOM: in a freshly opened shell the content is at the
	//     TOP (the screen has not filled yet), and the window saw empty lines. The
	//     end-to-end test in Go caught it.
	//
	// What holds in both cases is to follow the CURSOR: that is where the person is
	// working, by definition. The anchor only moves when the cursor would leave the
	// visible band — keeping it still is what avoids repainting the whole screen
	// on every new line.
	if cur.Y < q.ancora {
		q.ancora = cur.Y
	}
	if cur.Y >= q.ancora+q.rows {
		q.ancora = cur.Y - q.rows + 1
	}
	if limite := len(tela) - q.rows; q.ancora > limite {
		q.ancora = limite
	}
	if q.ancora < 0 {
		q.ancora = 0
	}
	topo := q.ancora
	for y := 0; y < q.rows; y++ {
		var linha []byte
		if origem := topo + y; origem < len(tela) {
			linha = vt10x.EmBytesRecorte(tela[origem], q.desloc, q.cols)
		}
		if y < len(q.base) && q.base[y] != nil && bytes.Equal(q.base[y], linha) {
			continue // the client already has this line
		}
		buf.WriteString("\x1b[")
		buf.WriteString(strconv.Itoa(y + 1))
		buf.WriteString(";1H")
		buf.Write(linha)
		buf.WriteString("\x1b[K") // erases the tail of the old line
		if y < len(q.base) {
			q.base[y] = append([]byte(nil), linha...)
		}
	}
	if buf.Len() == 0 {
		return nil
	}
	// The cursor last, otherwise it stays where the final line ended.
	l := cur.Y - topo + 1
	c := cur.X - q.desloc + 1
	if l > q.rows {
		l = q.rows
	}
	if l < 1 {
		l = 1
	}
	if c < 1 {
		c = 1
	}
	buf.WriteString("\x1b[")
	buf.WriteString(strconv.Itoa(l))
	buf.WriteString(";")
	buf.WriteString(strconv.Itoa(c))
	buf.WriteString("H")
	if cursorVisivel {
		buf.WriteString("\x1b[?25h")
	} else {
		buf.WriteString("\x1b[?25l")
	}
	return buf.Bytes()
}
