package pty

import (
	"bytes"
	"log"
	"sync"

	"server-control-panel/internal/pty/vt10x"
)

const (
	defaultCols = 80
	defaultRows = 24
)

type sessionScreen struct {
	mu   sync.Mutex
	vt   *vt10x.State
	file *sessionLogWriter
	rest []byte
	dead bool
	name string

	subscribersMu    sync.Mutex
	subscribers      map[int64]func(scrolled int)
	nextSubID        int64
	scrolledInBlock  int
	pendingNotice    int
	hasPendingNotice bool
}

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
		name: name,
	}
	t.vt.OnScrollOut(func(lines [][]vt10x.Glyph) {
		for _, l := range lines {
			_, _ = t.file.Write(vt10x.LineBytes(l))
		}
		t.scrolledInBlock += len(lines)
	})
	return t
}

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
			log.Printf("[pty] history of session %q turned off by a panic in the emulator: %v", t.name, r)
		}
	}()
	data := p
	if len(t.rest) > 0 {
		data = append(append(make([]byte, 0, len(t.rest)+len(p)), t.rest...), p...)
		t.rest = nil
	}
	t.scrolledInBlock = 0
	n, err := t.vt.Write(data)
	scrolled := t.scrolledInBlock
	if err == nil && n < len(data) {
		if leftover := data[n:]; len(leftover) <= 8 {
			t.rest = append([]byte(nil), leftover...)
		}
	}
	t.pendingNotice = scrolled
	t.hasPendingNotice = true
}

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
			log.Printf("[pty] history of session %q turned off by a panic in the resize: %v", t.name, r)
		}
	}()
	t.vt.Resize(int(cols), int(rows))
}

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
	if t.vt.InAltScreen() {
		return nil
	}
	lines := t.vt.CurrentScreen()
	done := len(lines)
	for done > 0 && len(bytes.TrimSpace(stripANSIBytes(vt10x.LineBytes(lines[done-1])))) == 0 {
		done--
	}
	var buf bytes.Buffer
	for _, l := range lines[:done] {
		buf.Write(vt10x.LineBytes(l))
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

func SessionHistory(user, name string, maxBytes int) ([]byte, int) {
	if maxBytes <= 0 || maxBytes > maxRawLogTailBytes {
		maxBytes = maxRawLogTailBytes
	}
	dd := activeDD()
	cut, total := readTail(sessionHistPath(dd, user, name), maxBytes)
	if len(cut) < total {
		if i := nextLineIndex(cut); i >= 0 {
			cut = cut[i:]
		}
	}
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

func sessionHistPath(dataDir, user, name string) string {
	return sessionLogPath(dataDir, user, name) + ".hist"
}
