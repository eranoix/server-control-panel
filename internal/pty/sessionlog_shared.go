package pty

import (
	"bytes"
	"io"
	"sync"
	"time"
)

var (
	sessionLogsMu sync.Mutex
	sessionLogs   = map[string]*sharedLog{}
)

type sharedLog struct {
	w     *sessionLogWriter
	conns []int64
	next  int64

	sizes       map[int64]clientSize
	appliedCols uint16
	appliedRows uint16
	appliers    map[int64]func(uint16, uint16)

	scribe    int64
	lastWrite time.Time
}

var sessionClock = time.Now

type sessionWriter struct {
	shared *sharedLog
	id     int64

	firstBlock bool
	start      []byte
	tail       []byte

	saidFarewell bool
}

var dtachAttachClear = []byte("\x1b[H\x1b[J")

var (
	dtachExitMark = []byte("\x1b[999H")
	dtachExitText = []byte("[detached]")
)

const leaseValidity = 1500 * time.Millisecond

const tailBytes = 48

func (e *sessionWriter) Write(p []byte) (int, error) {
	n := len(p)
	if e.saidFarewell {
		return n, nil
	}

	if e.firstBlock {
		e.start = append(e.start, p...)
		if len(e.start) < len(dtachAttachClear) {
			if bytes.HasPrefix(dtachAttachClear, e.start) {
				return n, nil
			}
		}
		e.firstBlock = false
		p = bytes.TrimPrefix(e.start, dtachAttachClear)
		e.start = nil
	}

	combined := p
	if len(e.tail) > 0 {
		combined = append(append(make([]byte, 0, len(e.tail)+len(p)), e.tail...), p...)
	}
	if i := bytes.Index(combined, dtachExitText); i >= 0 {
		cut := -1
		if j := bytes.LastIndex(combined[:i], dtachExitMark); j >= 0 && i-j <= 16 {
			cut = j
		}
		if cut >= 0 {
			e.saidFarewell = true
			alreadyWritten := len(e.tail) - cut
			if alreadyWritten > 0 {
				e.shared.w.dropLast(alreadyWritten)
				p = nil
			} else {
				p = p[:cut-len(e.tail)]
			}
		}
	}

	if len(p) == 0 {
		return n, nil
	}
	now := sessionClock()

	sessionLogsMu.Lock()
	iAmScribe := e.shared.scribe == e.id
	leaseExpired := now.Sub(e.shared.lastWrite) >= leaseValidity
	if iAmScribe || e.shared.scribe == 0 || leaseExpired {
		e.shared.scribe = e.id
		e.shared.lastWrite = now
		iAmScribe = true
	}
	sessionLogsMu.Unlock()

	if !iAmScribe {
		return len(p), nil
	}
	if _, err := e.shared.w.Write(p); err != nil {
		return n, err
	}
	if len(p) >= tailBytes {
		e.tail = append(e.tail[:0], p[len(p)-tailBytes:]...)
	} else {
		e.tail = append(e.tail, p...)
		if len(e.tail) > tailBytes {
			e.tail = append(e.tail[:0], e.tail[len(e.tail)-tailBytes:]...)
		}
	}
	return n, nil
}

func acquireSessionLog(dataDir, user, name string) (io.Writer, *sharedLog, int64, func()) {
	key := sessionLogPath(dataDir, user, name)

	sessionLogsMu.Lock()
	shared, exists := sessionLogs[key]
	if !exists {
		shared = &sharedLog{w: openSessionLog(dataDir, user, name)}
		sessionLogs[key] = shared
	}
	shared.next++
	id := shared.next
	shared.conns = append(shared.conns, id)
	sessionLogsMu.Unlock()

	var once sync.Once
	release := func() {
		once.Do(func() {
			sessionLogsMu.Lock()
			for i, c := range shared.conns {
				if c == id {
					shared.conns = append(shared.conns[:i], shared.conns[i+1:]...)
					break
				}
			}
			efCols, efRows, changed, appliers := shared.forgetSize(id)
			if shared.scribe == id {
				shared.scribe = 0
			}
			last := len(shared.conns) == 0
			if last {
				delete(sessionLogs, key)
			}
			sessionLogsMu.Unlock()
			if last {
				_ = shared.w.Close()
			}
			if changed {
				for _, apply := range appliers {
					apply(efCols, efRows)
				}
			}
		})
	}
	return &sessionWriter{shared: shared, id: id, firstBlock: true},
		shared, id, release
}
