package pty

import (
	"bytes"
	"io"
	"sync"
	"time"
)

// THE SESSION LOG HAS EXACTLY ONE SCRIBE.
//
// ## The defect, and why the first fix was not enough
//
// `HostShell` opened one `sessionLogWriter` PER CONNECTION, all of them
// appending to the same file. Every attached client is a `dtach -a` of its own
// receiving the WHOLE PTY stream — so with the app and the web panel open on
// the same session, every byte from the remote program was recorded twice.
//
// The first attempt shared the WRITER between the connections. It fixed nothing,
// and the reason is obvious once seen: sharing the file prevents two
// descriptors, not two WRITES. Both connections went on calling `Write` with
// the same thing. The proof is in the session's raw bytes:
//
//	ESC[?25l ESC[2D ESC[6B \r ESC[11A … Recombobulating… ESC[39m \r
//	ESC[?25l ESC[2D ESC[6B \r ESC[11A … Recombobulating… ESC[39m \r   <- identical
//	\r\n  x22                                                         <- the TUI emitted 11
//
// No TUI emits the same sequence twice in a row. And the doubled blank lines are
// what blanks the app's screen: eleven scrolls become twenty-two, the whole
// content leaves the active screen for the scrollback, and whoever opens the
// session sees black — with everything intact one scroll above. That was the
// owner's report: *"I opened the window and it was all black. I scrolled down
// and it came back into view"*.
//
// Only the app suffers: it REBUILDS the screen by replaying that log into its
// own libghostty-vt. The web panel never reads the log, which is why "on the web
// it is fine" — the asymmetry the owner reported is the defect's own signature.
//
// ## The fix: one scribe, by LEASE — not by position
//
// The first version elected the scribe by POSITION in the list (first to arrive)
// and promoted the next one when it left. That sounds sufficient and is not: the
// promotion only happens when a connection LEAVES, and a connection can stop
// delivering bytes without ever leaving — its `HostShell` hung, the `dtach`
// client dead, the network cut without a FIN. Then the scribe is a zombie,
// nobody writes, and **the session log simply stops**.
//
// And it does not stop in harmless silence: it stops IN THE MIDDLE. Measured on
// the "Vpsm" session — the log ended on exactly these six bytes:
//
//	ESC[H ESC[J     (go to the top, erase the whole screen)
//
// The other fifteen occurrences of that same sequence in the file are followed,
// on the very next byte, by the full repaint. The last one had nothing after it.
// The app replays the log to rebuild the screen, so it faithfully reproduced
// "erase everything" and stopped there: **a black screen, with all the content
// intact one scroll above**. The web panel was spared because it does not read
// the log.
//
// So the scribe becomes a LEASE rather than an office. Whoever writes renews;
// whoever arrives only takes over if the lease has expired. A living scribe never
// loses the post (it renews on every chunk, and the chunks arrive together for
// everyone); a scribe that stopped is replaced in [leaseValidity],
// without depending on its courtesy to leave.
//
// The price is explicit: on a handover, up to one lease window of output is
// lost. It is little, it is rare, and it is incomparably better than the log
// stopping forever — which is what used to happen.
//
// It cannot be solved by deduplicating bytes: the two streams are identical BY
// DEFINITION, so any "this already came through" heuristic would erase
// legitimate repetition — a double `\r\n` the program really emitted, a progress
// bar repainting the same. The only correct answer is not to write twice.
var (
	sessionLogsMu sync.Mutex
	sessionLogs   = map[string]*sharedLog{}
)

type sharedLog struct {
	w *sessionLogWriter
	// Attached connections, in arrival order. The first one is the scribe.
	conns []int64
	next  int64

	// The size each connection asked for, and what is actually on the PTY. See
	// `tamanho_da_sessao.go`: the PTY sits at the SMALLEST among the clients,
	// which is the only rule that converges when there is more than one.
	sizes       map[int64]clientSize
	appliedCols uint16
	appliedRows uint16
	// How to PUT the session's effective size on each connection — its pty and,
	// for whoever asked, the client's grid. See `tamanho_da_sessao.go`.
	appliers map[int64]func(uint16, uint16)

	// The write lease: who is recording now, and when it last recorded. See
	// [sessionWriter.Write].
	scribe    int64
	lastWrite time.Time
}

// sessionClock is replaceable in tests — the lease is a rule about TIME, and
// testing it with `time.Sleep` would trade an assertion for a bet.
var sessionClock = time.Now

// sessionWriter is what every connection receives. Only the scribe really writes.
//
// The diversion happens at WRITE time, not at handout time, on purpose: who
// decides changes when a connection leaves, and a decision frozen at attach time
// would leave the session with no log as soon as the first tab closed.
type sessionWriter struct {
	shared *sharedLog
	id     int64

	// Whether this connection's first chunk has yet to go through — see
	// [dtachAttachClear].
	firstBlock bool
	// The first bytes, held while they may still be the attach-time clear
	// arriving split.
	start []byte
	// The last [tailBytes] this connection wrote, so a `dtach` literal that
	// straddles a chunk boundary can still be recognised.
	tail []byte

	// Whether `dtach`'s goodbye has already shown up — see [despedidaDoDtach].
	saidFarewell bool
}

// dtachAttachClear is what `dtach` writes onto the NEW client's screen
// when it attaches: "go to the top, erase everything".
//
// The sequence is literally inside the binary (a `grep` over /usr/bin/dtach
// finds it), and it is not the PROGRAM's output — it is the multiplexer telling
// that client "start with a clean screen", because the client has just been born
// and does not know what was there before.
//
// Addressed to a new screen, it is correct. Recorded into the SESSION LOG it is
// poison: the log is the record of what the program painted, and the app replays
// it to rebuild the screen. It faithfully reproduced "erase everything" and
// stopped there.
//
// Measured on the "Vpsm" session: the file ended on exactly those six bytes. The
// other fifteen occurrences were followed by the full repaint — and those are
// precisely the attaches where the size CHANGED, because then the SIGWINCH from
// `-r winch` produces a real repaint. When the size does not change, a
// differential renderer writes no bytes at all, and all that stays recorded is
// the "erase everything": a black screen in the app, with the whole content
// intact one scroll above. The web panel never suffered because it does not read
// the log.
//
// That is why the cut is on the PREFIX of each connection's first chunk, and not
// a sweep of the stream: that is exactly where `dtach` writes, and the only
// place. If a program happened to begin its output with that same sequence at
// the instant of the attach, the price would be the replay keeping one extra old
// frame — nothing next to the guaranteed black screen the cut prevents.
var dtachAttachClear = []byte("\x1b[H\x1b[J")

// despedidaDoDtach is what `dtach` writes when the client DETACHES:
//
//	ESC[999H \r\n [detached] \r\n ESC[?25h
//
// `ESC[999H` throws the cursor to the last line (the terminal saturates at the
// last one that exists), writes `[detached]`, and the `\n` ON THE LAST LINE
// scrolls the whole screen up. Both literals live inside the `dtach` binary.
//
// On the screen of whoever is leaving, that is a useful message. In the SESSION
// LOG it is the same class of poison as the attach-time clear, and worse: it does
// not only erase, it SCROLLS. The app rebuilds the screen by replaying the log
// and reproduced this faithfully — a dark screen with the text one scroll above,
// which was exactly the owner's report, more than once.
//
// Measured on the "Vpsm" session: the app's own engine, fed with the log, came
// back with 52 blank lines and `[detached]` on line 51.
//
// ## The principle, which is bigger than these two sequences
//
// The session log is the record of what the PROGRAM painted. `dtach` is the
// pipe, and the pipe talks to ONE client — "start clean", "you left". Those
// phrases are true for that screen and lies for the record. Anything new the
// multiplexer starts saying to the client joins this same list.
var (
	dtachExitMark = []byte("\x1b[999H")
	dtachExitText = []byte("[detached]")
)

// leaseValidity: how long the scribe keeps the post without writing
// anything.
//
// The number comes from the gap between the two scales involved. A session's
// clients are fed by the SAME `dtach` master and receive the same chunk
// milliseconds apart — so while the scribe is alive, no other one comes close to
// finding the lease expired. A dead scribe, on the other hand, has to be replaced
// quickly, because every second with nobody recording is a hole in the middle of
// the log that the app will replay later.
//
// A second and a half sits three orders of magnitude above the skew between
// clients and is still short for the hole.
const leaseValidity = 1500 * time.Millisecond

// tailBytes: how much of what it has already written this connection
// remembers, in order to recognise a `dtach` literal that arrives SPLIT across
// two chunks.
//
// The PTY pump delivers whatever `read()` returned, and the goodbye
// (`ESC[999H \r\n [detached] \r\n`) is 20 bytes: nothing guarantees it fits in
// a single chunk. While the filter looked at one chunk at a time, a split
// literal went through whole — and the replay reproduced "go to the last line
// and scroll", which is the dark screen with the text one scroll above. Across
// the logs on this machine there were 32 `ESC[999H` and 24 `[detached]` recorded
// that way; 13 and 23 of them in the app's session, which is exactly who reported it.
//
// 48 bytes cover the whole goodbye with room to spare for the `ESC[999H` that
// precedes it.
const tailBytes = 48

func (e *sessionWriter) Write(p []byte) (int, error) {
	n := len(p)
	// After the goodbye nothing more comes from the program — only the rest of
	// `dtach`'s farewell. Once it has started, everything from this connection goes.
	if e.saidFarewell {
		return n, nil
	}

	// ── The attach-time clear, which can also arrive split ───────────────
	// While the start of this connection may still be `dtach`'s "erase the screen",
	// the bytes wait: there are at most 6 of them, and this is the only moment where
	// holding them is correct (nobody is reading the log in that millisecond).
	if e.firstBlock {
		e.start = append(e.start, p...)
		if len(e.start) < len(dtachAttachClear) {
			if bytes.HasPrefix(dtachAttachClear, e.start) {
				return n, nil // ainda pode ser a limpeza: espera o resto
			}
		}
		e.firstBlock = false
		p = bytes.TrimPrefix(e.start, dtachAttachClear)
		e.start = nil
	}

	// ── The goodbye, recognised ACROSS the boundary ──────────────────────
	// The search runs over tail+p. When the match starts in the tail, part of the
	// literal HAS ALREADY BEEN WRITTEN: the writer undoes exactly those bytes. That
	// is what makes the filter a rule about the STREAM, and not about the chunk.
	combined := p
	if len(e.tail) > 0 {
		combined = append(append(make([]byte, 0, len(e.tail)+len(p)), e.tail...), p...)
	}
	if i := bytes.Index(combined, dtachExitText); i >= 0 {
		// It is only `dtach`'s goodbye if the `ESC[999H` comes right before — it is
		// what scrolls the screen, and it is what tells the multiplexer's farewell
		// apart from a program that happened to print the word. Without that
		// requirement, a `grep [detached]` in a session silenced its log until reconnect.
		cut := -1
		if j := bytes.LastIndex(combined[:i], dtachExitMark); j >= 0 && i-j <= 16 {
			cut = j
		}
		if cut >= 0 {
			e.saidFarewell = true
			alreadyWritten := len(e.tail) - cut // pode ser <= 0 se o corte cai em p
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
		// Report success: the PTY pump must not treat "I am not the one
		// recording" as a write error.
		return len(p), nil
	}
	if _, err := e.shared.w.Write(p); err != nil {
		return n, err
	}
	// Keep the tail of only what THIS connection actually wrote.
	if len(p) >= tailBytes {
		e.tail = append(e.tail[:0], p[len(p)-tailBytes:]...)
	} else {
		e.tail = append(e.tail, p...)
		if len(e.tail) > tailBytes {
			e.tail = append(e.tail[:0], e.tail[len(e.tail)-tailBytes:]...)
		}
	}
	// Report the ORIGINAL length: whoever writes into the tee must not find out
	// that we cut a prefix — as far as it is concerned, everything was written.
	return n, nil
}

// acquireSessionLog registers this connection and returns its writer plus the
// function that unregisters it — call that exactly once (`defer`).
//
// It NEVER returns nil, for the same reason `openSessionLog` does not: the
// terminal must not break because of the log.
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
			// Take this connection out of the minimum calculation. Without this, a
			// small client that closed would keep shrinking the session forever.
			//
			// And the result IS USED: leaving changes the session's effective size
			// for everyone who stayed, and someone has to apply it. Discarding these
			// return values was the defect — the session stayed stuck at the size of
			// whoever closed their tab, with nothing able to restore it.
			efCols, efRows, changed, appliers := shared.forgetSize(id)
			// And release the lease right away, instead of making the next chunk
			// wait for it to expire. Leaving politely has to be faster than dying
			// in silence.
			if shared.scribe == id {
				shared.scribe = 0
			}
			last := len(shared.conns) == 0
			if last {
				delete(sessionLogs, key)
			}
			sessionLogsMu.Unlock()
			// Close OUTSIDE the lock: `Close` takes the writer's mutex, and holding
			// both at once is how you invent a lock ordering for someone to violate
			// later.
			if last {
				_ = shared.w.Close()
			}
			// OUTSIDE the lock, for the usual reason: applying means writing to a
			// websocket and an ioctl.
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
