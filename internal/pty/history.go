package pty

// history.go — THE SESSION'S TRUE HISTORY, WRITTEN AS IT HAPPENS.
//
// ## What was still wrong after the recorder
//
// With the recorder (`recorder.go`) the log stopped having holes, and with the
// primer the panel loaded history again when the session was opened on another
// computer. But what the primer loads is the RAW BYTES, and in a program that
// redraws those are not history — they are the record of a drawing in progress.
//
// Replayed onto a new grid, those bytes duplicate. The reason is mechanical and
// has no fix on the reader's side: Ink (Claude Code) repaints by walking the
// cursor up with `ESC[nA`, and the CUU saturates at the first line of the SCREEN
// — it never reaches the scrollback. In a replay the cursor starts on an empty
// grid, the previous frame has already scrolled up and STAYS; the new one is
// painted below it. The same conversation shows up twice, three times, many
// times. That is the "the text is duplicated" of the report.
//
// ## The way out: whoever watched the session happen needs no replay
//
// The recorder is in the stream the whole time, and the session's size is known.
// So the server can keep an EMULATOR fed live: every `ESC[nA` lands exactly
// where the program meant it to, because the grid is the same one the program is
// looking at.
//
// And what matters is not its screen — it is what LEAVES it. A line that has
// scrolled off is finished: the program will not touch it again. Serialised back
// (`vt10x.EmBytes`), it is append-only text, which any terminal reproduces
// without ambiguity. The `<session>.hist` file is the sum of those lines: the
// history the person saw, once each.
//
// The CURRENT screen deliberately stays out of the file — it reaches the client
// through the attach repaint, painted by the program itself, which is what knows
// how to draw the whole of it. That way there is no overlap between what the
// primer writes and what the program paints next.
//
// ## Isolation: this is an observer, never a middleman
//
// The emulator does NOT sit between the PTY and the log. The recorder writes to
// the log first and only then feeds the screen. A panic in here (it is
// third-party code, patched) must not take the process down or stop anyone's
// terminal: the goroutine has a `recover`, and failing means switching off that
// session's history — never the session.

import (
	"bytes"
	"log"
	"sync"

	"server-control-panel/internal/pty/vt10x"
)

// defaultCols/defaultRows: the size the screen is born at, before the first
// client states its own. It is not a guess: it is the size `dtach` itself uses
// when nobody has spoken, so the screen starts out agreeing with the PTY.
const (
	defaultCols = 80
	defaultRows = 24
)

// sessionScreen is the emulator that follows a session and pours what leaves the
// screen into the history file.
type sessionScreen struct {
	mu   sync.Mutex
	vt   *vt10x.State
	file *sessionLogWriter
	rest []byte // bytes of a rune split at the block boundary
	dead bool   // a panic switched this screen off
	nome string

	// Whoever wants to know the screen changed — the connections in frame mode
	// (`frame.go`). `rolou` is how many lines left during the chunk: scrolling is
	// handled as scrolling, not as a repaint.
	subscribersMu sync.Mutex
	subscribers   map[int64]func(scrolled int)
	nextSubID     int64
	// scrolledInBlock counts, WITHIN one alimenta, how many lines left.
	scrolledInBlock int
	// The notice `alimenta` left for `flushNotice` to fire outside the lock.
	pendingNotice    int
	hasPendingNotice bool
}

// assina registers whoever wants to be told the screen changed. It returns how
// to cancel — call that exactly once.
func (t *sessionScreen) subscribe(fn func(scrolled int)) func() {
	if t == nil || fn == nil {
		return func() {}
	}
	t.subscribersMu.Lock()
	if t.subscribers == nil {
		t.subscribers = map[int64]func(int){}
	}
	t.nextSubID++
	id := t.nextSubID
	t.subscribers[id] = fn
	t.subscribersMu.Unlock()
	return func() {
		t.subscribersMu.Lock()
		delete(t.subscribers, id)
		t.subscribersMu.Unlock()
	}
}

// notifySubscribers fires OUTSIDE the screen's lock: whoever receives it will read
// the screen next, and reading while holding the writer's lock is how you invent
// a deadlock.
func (t *sessionScreen) notifySubscribers(scrolled int) {
	t.subscribersMu.Lock()
	fns := make([]func(int), 0, len(t.subscribers))
	for _, f := range t.subscribers {
		fns = append(fns, f)
	}
	t.subscribersMu.Unlock()
	for _, f := range fns {
		f(scrolled)
	}
}

// screenAndCursor returns a copy of the visible screen, the cursor and whether it is
// visible — what the frame compositor needs in order to draw.
func (t *sessionScreen) screenAndCursor() ([][]vt10x.Glyph, vt10x.Cursor, bool) {
	if t == nil {
		return nil, vt10x.Cursor{}, false
	}
	t.mu.Lock()
	dead := t.dead
	t.mu.Unlock()
	if dead {
		return nil, vt10x.Cursor{}, false
	}
	return t.vt.CurrentScreen(), t.vt.LockedCursor(), t.vt.LockedCursorVisible()
}

// tamanho returns the server screen's grid — the SESSION's grid.
func (t *sessionScreen) size() (cols, rows int) {
	if t == nil {
		return 0, 0
	}
	return t.vt.LockedSize()
}

func newSessionScreen(dataDir, user, name string) *sessionScreen {
	t := &sessionScreen{
		vt:   vt10x.New(defaultCols, defaultRows),
		file: openWriter(sessionHistPath(dataDir, user, name)),
		nome: name,
	}
	t.vt.OnScrollOut(func(lines [][]vt10x.Glyph) {
		// Called with the emulator's lock held: serialising is cheap (it is text)
		// and the writer is absolutely best-effort, like the rest of the tee.
		for _, l := range lines {
			_, _ = t.file.Write(vt10x.EmBytes(l))
		}
		t.scrolledInBlock += len(lines)
	})
	return t
}

// alimenta hands the emulator the same bytes that went into the log.
//
// It carries over the partial rune left from the previous chunk: the recorder
// delivers whatever `read()` returned, and a multibyte character straddles that
// boundary all the time. Without this, every boundary would become a wrong
// character in the history.
func (t *sessionScreen) feed(p []byte) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.dead {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			t.dead = true
			log.Printf("[pty] history of session %q turned off by a panic in the emulator: %v", t.nome, r)
		}
	}()
	dados := p
	if len(t.rest) > 0 {
		dados = append(append(make([]byte, 0, len(t.rest)+len(p)), t.rest...), p...)
		t.rest = nil
	}
	t.scrolledInBlock = 0
	n, err := t.vt.Write(dados)
	scrolled := t.scrolledInBlock
	if err == nil && n < len(dados) {
		// A rune split at the end: keep it for the next chunk. The ceiling stops a
		// binary stream (which never completes a rune) growing this without limit.
		if leftover := dados[n:]; len(leftover) <= 8 {
			t.rest = append([]byte(nil), leftover...)
		}
	}
	// Notifying goes OUTSIDE the lock — see [notifySubscribers]. The recover's
	// defer above has already run by the time this function returns, so the
	// notice does not leave here on a goroutine; it leaves on the way out, with
	// the lock released by the defer.
	t.pendingNotice = scrolled
	t.hasPendingNotice = true
}

// flushNotice releases the notice `alimenta` left pending. Separate because
// `alimenta` holds the lock until it returns (the recover needs it) and
// notifying while holding it would invite a deadlock with whoever is about to
// READ the screen.
func (t *sessionScreen) flushNotice() {
	if t == nil {
		return
	}
	t.mu.Lock()
	has, scrolled := t.hasPendingNotice, t.pendingNotice
	t.hasPendingNotice, t.pendingNotice = false, 0
	t.mu.Unlock()
	if has {
		t.notifySubscribers(scrolled)
	}
}

// redimensiona puts the server's screen at the session's EFFECTIVE size — the
// same one the program is looking at. That is what makes `ESC[nA` land in the
// right place and, in consequence, the history come out without repeated copies.
func (t *sessionScreen) resize(cols, rows uint16) {
	if t == nil || cols < 2 || rows < 1 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.dead {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			t.dead = true
			log.Printf("[pty] history of session %q turned off by a panic in the resize: %v", t.nome, r)
		}
	}()
	t.vt.Resize(int(cols), int(rows))
}

// instantaneo serialises the VISIBLE lines of the screen, trimming the empty
// ones at the end.
//
// Why this is needed even with the history: the file only receives a line once
// it HAS SCROLLED off. What is still in view is not in there — and in an
// ordinary shell nothing repaints it when a new client attaches (`bash` redraws
// only the prompt line). Without this snapshot, whoever opens the session on
// another computer gets the whole history and loses exactly the last screen.
//
// Measured in the browser before it existed: the second PC saw the old lines and
// did NOT see the latest ones — a regression the primer introduced by asking for
// `replay=0`, because the raw chunk the server used to send covered that part.
func (t *sessionScreen) snapshot() []byte {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	dead := t.dead
	t.mu.Unlock()
	if dead {
		return nil
	}
	// On the alternate screen (vim, htop) the program is what redraws, in the
	// attach repaint: painting over it would be the duplication the wobble prevents.
	if t.vt.EmAltScreen() {
		return nil
	}
	lines := t.vt.CurrentScreen()
	fim := len(lines)
	for fim > 0 && len(bytes.TrimSpace(stripANSIBytes(vt10x.EmBytes(lines[fim-1])))) == 0 {
		fim--
	}
	var buf bytes.Buffer
	for _, l := range lines[:fim] {
		buf.Write(vt10x.EmBytes(l))
	}
	return buf.Bytes()
}

func stripANSIBytes(b []byte) []byte { return []byte(stripANSI(string(b))) }

func (t *sessionScreen) closeOnce() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	_ = t.file.Close()
}

// SessionHistory returns the last maxBytes of the rendered history — what the
// panel writes into the xterm when it opens the session. Append-only text: no
// replay, no repeated copies, already at the session's width.
func SessionHistory(user, name string, maxBytes int) ([]byte, int) {
	if maxBytes <= 0 || maxBytes > maxRawLogTailBytes {
		maxBytes = maxRawLogTailBytes
	}
	dd := activeDD()
	cut, total := readTail(sessionHistPath(dd, user, name), maxBytes)
	if len(cut) < total {
		// It cut in the middle of a line: start on the next one. Half a line at
		// the top of the history is just dirt.
		if i := nextLineIndex(cut); i >= 0 {
			cut = cut[i:]
		}
	}
	// And the LIVE SCREEN at the end: the file covers what left, the snapshot
	// covers what is still in view. Only for a stream that does NOT redraw — in a
	// program that repaints, the one that draws the current screen is the program
	// itself, in the attach repaint, and painting over it would duplicate.
	if screen := screenOf(dd, user, name); screen != nil && !recentStreamRepaints(dd, user, name) {
		if inst := screen.snapshot(); len(inst) > 0 {
			cut = append(cut, inst...)
			total += len(inst)
		}
	}
	if total == 0 {
		return nil, 0
	}
	return cut, total
}

// recentStreamRepaints reports whether the session's recent output came from a
// renderer that redraws. It uses the SAME calibrated classifier as
// `attachReplay` (see `repaintLimit`), over the log's tail — reading the
// whole log to answer this on every attach would cost more and be no more exact.
func recentStreamRepaints(dataDir, user, name string) bool {
	tail, _ := readTail(sessionLogPath(dataDir, user, name), maxAttachReplayBytes)
	return len(tail) > 0 && isRepaintStream(tail)
}

func nextLineIndex(b []byte) int {
	for i := 0; i+1 < len(b); i++ {
		if b[i] == '\r' && b[i+1] == '\n' {
			return i + 2
		}
	}
	return -1
}

// sessionHistPath is the path of the session's RENDERED history, sibling to the
// raw log. Separate files on purpose: one is the record of what went down the
// wire, the other is what the person saw.
func sessionHistPath(dataDir, user, name string) string {
	return sessionLogPath(dataDir, user, name) + ".hist"
}
