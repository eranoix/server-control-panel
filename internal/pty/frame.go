package pty

import (
	"bytes"
	"strconv"

	"server-control-panel/internal/pty/vt10x"
)

type clientFrame struct {
	cols, rows int
	offset     int
	anchor     int
	base       [][]byte
	first      bool
}

func newClientFrame(cols, rows int) *clientFrame {
	return &clientFrame{cols: cols, rows: rows, first: true}
}

func (q *clientFrame) resize(cols, rows int) {
	if cols == q.cols && rows == q.rows {
		return
	}
	q.cols, q.rows = cols, rows
	q.base = nil
	q.first = true
}

func (q *clientFrame) shift(stop, sessionCols int) {
	if stop < 0 {
		stop = 0
	}
	if max := sessionCols - q.cols; stop > max {
		if max < 0 {
			max = 0
		}
		stop = max
	}
	if stop == q.offset {
		return
	}
	q.offset = stop
	q.base = nil
	q.first = true
}

func (q *clientFrame) scrolled(k int) []byte {
	if k <= 0 || q.first {
		return nil
	}
	if k >= q.rows {
		q.base = nil
		q.first = true
		return nil
	}
	var buf bytes.Buffer
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
	for len(q.base) < q.rows {
		q.base = append(q.base, nil)
	}
	return buf.Bytes()
}

func (q *clientFrame) update(screen [][]vt10x.Glyph, cur vt10x.Cursor, cursorVisible bool) []byte {
	if q.cols < 2 || q.rows < 1 {
		return nil
	}
	var buf bytes.Buffer
	if q.first {
		buf.WriteString("\x1b[H\x1b[2J")
		q.base = make([][]byte, q.rows)
		q.first = false
	}
	if cur.Y < q.anchor {
		q.anchor = cur.Y
	}
	if cur.Y >= q.anchor+q.rows {
		q.anchor = cur.Y - q.rows + 1
	}
	if limit := len(screen) - q.rows; q.anchor > limit {
		q.anchor = limit
	}
	if q.anchor < 0 {
		q.anchor = 0
	}
	top := q.anchor
	for y := 0; y < q.rows; y++ {
		var line []byte
		if origin := top + y; origin < len(screen) {
			line = vt10x.CropToBytes(screen[origin], q.offset, q.cols)
		}
		if y < len(q.base) && q.base[y] != nil && bytes.Equal(q.base[y], line) {
			continue
		}
		buf.WriteString("\x1b[")
		buf.WriteString(strconv.Itoa(y + 1))
		buf.WriteString(";1H")
		buf.Write(line)
		buf.WriteString("\x1b[K")
		if y < len(q.base) {
			q.base[y] = append([]byte(nil), line...)
		}
	}
	if buf.Len() == 0 {
		return nil
	}
	l := cur.Y - top + 1
	c := cur.X - q.offset + 1
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
	if cursorVisible {
		buf.WriteString("\x1b[?25h")
	} else {
		buf.WriteString("\x1b[?25l")
	}
	return buf.Bytes()
}
