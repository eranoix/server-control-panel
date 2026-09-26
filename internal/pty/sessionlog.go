// sessionlog.go — tees the pty output into one log file per session.
//
// The session engine keeps no screen at all. The replacement: TEE every byte of
// pty output — which already passes in full through the proxy's PTY→WS pump —
// into <DataDir>/users/<user>/session-logs/<name>.log. ONE source covers the 4
// consumers that capture-pane serves today: scrollback on attach, preview,
// waitClaudeReady (Jira spawn) and the Jira watcher. Backup = a snapshot of the
// log; restore = a replay.
//
// SAFETY INVARIANT: the tee is ABSOLUTELY best-effort. No log I/O failure may
// break the user's terminal — Write swallows every error and always reports
// success to the caller. It writes RAW bytes (colours/escapes included) so the
// replay matches the live attach.
//
// The tee was wired in before any consumer needed it, so the log would already
// be populated by the time they migrated onto it.
package pty

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// maxSessionLogBytes: per-log ceiling before rotating. 8 MiB holds plenty of
// scrollback (tens of thousands of lines) without bloating the disk. Past that
// it rotates to <log>.1 (keeping one previous generation) and starts over.
const maxSessionLogBytes = 8 << 20

// sessionLogPath is the path of the pty log for the user's session `name`. The
// single source — the backend's LogPath points here.
func sessionLogPath(dataDir, user, name string) string {
	return filepath.Join(dataDir, "users", safeSessionName(user), "session-logs", safeSessionName(name)+".log")
}

// sessionLogWriter is an io.WriteCloser that appends to the session log and
// rotates by size. Thread-safe. An f==nil (failed to open) degrades to a no-op —
// the terminal keeps working without a log.
type sessionLogWriter struct {
	mu   sync.Mutex
	path string
	f    *os.File
	size int64
}

// openSessionLog opens (append, creating dirs) the session log. It NEVER returns
// nil: if it cannot open one, it returns a no-op writer (f==nil) — the tee turns
// inert instead of taking HostShell down.
func openSessionLog(dataDir, user, name string) *sessionLogWriter {
	return abreEscritor(sessionLogPath(dataDir, user, name))
}

// abreEscritor is openSessionLog by PATH. It exists because the rendered history
// (`historico.go`) is another file with the same needs: append, rotation by size
// and failing silently.
func abreEscritor(caminho string) *sessionLogWriter {
	w := &sessionLogWriter{path: caminho}
	if err := os.MkdirAll(filepath.Dir(w.path), 0o700); err != nil {
		return w // no-op
	}
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return w // no-op
	}
	w.f = f
	if fi, err := f.Stat(); err == nil {
		w.size = fi.Size()
	}
	return w
}

// Write appends p to the log. Best-effort: it always reports (len(p), nil) — a
// log error never propagates into the pty pump. Rotates if it passes the ceiling.
func (w *sessionLogWriter) Write(p []byte) (int, error) {
	if w == nil {
		return len(p), nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return len(p), nil
	}
	if w.size+int64(len(p)) > maxSessionLogBytes {
		w.rotateLocked()
	}
	if n, err := w.f.Write(p); err == nil {
		w.size += int64(n)
	}
	return len(p), nil
}

// descartaUltimos undoes the last n bytes already written.
//
// It exists for one reason only: `dtach`'s chatter can arrive SPLIT across two
// chunks, and by the time the filter recognises it straddling the boundary, its
// beginning is already in the file. Without undoing, the filter would stay a
// rule about the chunk — which is exactly what left `ESC[999H` and
// `[detached]` recorded in the log. See [escritorDaSessao.Write].
//
// The file is opened with O_APPEND, so truncating is enough: the next write
// lands at the new end again. Best-effort like the rest of the tee — failing
// here must not take down anyone's terminal.
func (w *sessionLogWriter) descartaUltimos(n int) {
	if w == nil || n <= 0 {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	// size < n only happens if a rotation slipped in halfway; the bytes are then
	// in the previous generation and undoing here would cut the wrong file.
	if w.f == nil || w.size < int64(n) {
		return
	}
	alvo := w.size - int64(n)
	if err := w.f.Truncate(alvo); err != nil {
		return
	}
	w.size = alvo
}

// rotateLocked renames the current log to <log>.1 (overwriting the previous
// generation) and reopens a fresh file. Called with w.mu held. Failures are
// swallowed — at worst the log keeps growing, which beats breaking the pty.
func (w *sessionLogWriter) rotateLocked() {
	if w.f == nil {
		return
	}
	_ = w.f.Close()
	_ = os.Rename(w.path, w.path+".1")
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		w.f = nil
		w.size = 0
		return
	}
	w.f = f
	w.size = 0
}

// ansiRE matches the most common escape sequences (CSI/SGR + OSC) for the
// scrollback's plain-text mode. It is not a full VT parser — it covers colours,
// cursor and OSC (titles/OSC 52) well enough for "copy everything" to read well.
var ansiRE = regexp.MustCompile(
	"\x1b\\[[0-9;?]*[ -/]*[@-~]" + // CSI (SGR, cursor, etc.)
		"|\x1b\\][^\x07\x1b]*(?:\x07|\x1b\\\\)" + // OSC … BEL/ST
		"|\x1b[()][A-Za-z0-9]" + // charset designation
		"|[\x00-\x08\x0b\x0c\x0e-\x1f]") // other controls (keeps \t \n \r)

func stripANSI(s string) string { return ansiRE.ReplaceAllString(s, "") }

// readLogRotated returns the session log's content including the PREVIOUS
// generation (.1) ahead of the current one. Needed because rotation (8 MiB)
// moves the history into <log>.1: without this, right after a rotation the
// tail/replay/preview/backup come back nearly empty, AND alt-screen detection
// misses an ENTER (\x1b[?1049h) left in the previous generation (→ replaying
// garbage into a TUI). Best-effort: only the current one if .1 is missing.
func readLogRotated(path string) []byte {
	var buf []byte
	if prev, err := os.ReadFile(path + ".1"); err == nil {
		buf = prev
	}
	if cur, err := os.ReadFile(path); err == nil {
		buf = append(buf, cur...)
	}
	return buf
}

// tailSessionLog returns the last `lines` lines of the session's pty log — the
// stand-in for capture-pane under the dtach backend. escapes=true keeps colours
// (to prime the live display); escapes=false strips ANSI (plain text). "" on any
// failure (a missing log means the session never had a client attached;
// best-effort). It slices the tail BEFORE stripping ANSI (stripANSI over the
// whole 8 MiB blob was O(n) on the scrollback's hot path).
func tailSessionLog(dataDir, user, name string, lines int, escapes bool) string {
	if lines <= 0 || lines > 50000 {
		lines = 5000
	}
	data := readLogRotated(sessionLogPath(dataDir, user, name))
	if len(data) == 0 {
		return ""
	}
	rows := strings.Split(string(data), "\n")
	if len(rows) > lines {
		rows = rows[len(rows)-lines:]
	}
	s := strings.Join(rows, "\n")
	if !escapes {
		s = stripANSI(s)
	}
	return s
}

// maxAttachReplayBytes: ceiling on the history replayed on a FRESH attach to
// prime a normal shell's screen. 128 KiB = plenty of scrollback without costing
// bandwidth (it happens once, at attach). TUI sessions replay nothing (see
// attachReplay).
const maxAttachReplayBytes = 128 << 10

// altScreenEnter/Leave: the sequences that switch to the alternate buffer
// (alt-screen). A TUI app (claude, vim, htop) ENTERS the alt-screen and stays
// there; a normal shell (bash) never does. Used to decide whether an attach
// should replay the history.
var (
	altScreenEnter = [][]byte{[]byte("\x1b[?1049h"), []byte("\x1b[?1047h"), []byte("\x1b[?47h")}
	altScreenLeave = [][]byte{[]byte("\x1b[?1049l"), []byte("\x1b[?1047l"), []byte("\x1b[?47l")}
)

// sessionInAltScreen reports whether the session is CURRENTLY in alt-screen: the
// last switch in the stream was an ENTER with no LEAVE after it. It scans the
// whole log (the enter of a TUI started hours ago may be far from the end) —
// cheap (a LastIndex over a few MB, rare, only at attach).
func sessionInAltScreen(data []byte) bool {
	lastEnter, lastLeave := -1, -1
	for _, p := range altScreenEnter {
		if i := bytes.LastIndex(data, p); i > lastEnter {
			lastEnter = i
		}
	}
	for _, p := range altScreenLeave {
		if i := bytes.LastIndex(data, p); i > lastLeave {
			lastLeave = i
		}
	}
	return lastEnter > lastLeave
}

// limiteRepintura: how many CUUs of TWO or more lines are enough to say the
// stream was produced by a differential renderer and not by a shell.
//
// Calibrated by counting, across the 30 real session logs on this machine, over
// the SAME 128 KiB slice the replay uses:
//
//	shells        : 0, 0, 0, 0, 0, 3, 11        (worst case 11)
//	Claude Code   : 33, 52, 125, 396, ... 2448  (best case 33)
//
// The gap between 11 and 33 is empty; 20 sits in the middle of it. Erring on the
// "it is a shell" side is the cheap side: at worst the operator sees the history
// exactly as they always have.
const limiteRepintura = 20

// tailDoLog returns the same slice attachReplay replays. Classifying the WHOLE
// log would give a false positive for a session that went through a TUI hours
// ago and is a shell today — what matters is what is going to be replayed.
func tailDoLog(data []byte) []byte {
	if len(data) > maxAttachReplayBytes {
		return data[len(data)-maxAttachReplayBytes:]
	}
	return data
}

// fluxoERepintado reports whether these bytes came from a renderer that REDRAWS
// (Ink/Claude Code, `less`, anything that walks the cursor up to rewrite)
// instead of a shell, whose output is append-only.
//
// **Why this has to exist alongside sessionInAltScreen.** The alt-screen guard
// covers `vim`/`htop`, which ENTER the alternate buffer. Claude Code does not:
// it repaints in the normal buffer. So it was classified as "an ordinary shell"
// and earned a replay — and replaying a redrawn stream is destructive for two
// independent reasons, both measured on the device:
//
//  1. The text is already WRAPPED at the width the PTY had when it was
//     produced. The "Aplicativo" log held frames recorded at 24, 49, 67, 77
//     and 113 columns. Reproduced on a grid of another width, every frame comes
//     out squeezed — and NO terminal can undo it: the breaks are the program's
//     `\r\n`, not terminal wraps, and reflow only rejoins lines the terminal
//     wrapped itself (it is the universal rule; see VTE's doc/rewrap.txt).
//
//  2. The tail does not hold ONE frame, it holds dozens. Ink repaints by walking
//     the cursor up, which only works relative to its LIVE position. On an attach
//     the cursor starts on an empty grid and the `ESC[nA` saturates at the first
//     line of the screen, never reaching the scrollback: the previous frame stays,
//     the new one is painted below it, and the same conversation shows up twice.
//
// What the operator wants to see — the current frame, at their width — arrives
// right afterwards from the repaint-wobble, correct and exactly once.
//
// It counts only `ESC [ n A` with n >= 2. `n == 1` (or omitted, or 0, which by
// ECMA-48 both mean 1) is left out on purpose: that is what readline emits to
// redraw a two-line prompt, and counting it would classify a plain shell as a TUI.
func fluxoERepintado(data []byte) bool {
	total := 0
	for i := 0; i+1 < len(data); {
		if data[i] != 0x1B || data[i+1] != '[' {
			i++
			continue
		}
		j := i + 2
		valor, digitos := 0, 0
		for j < len(data) && data[j] >= '0' && data[j] <= '9' {
			if valor < 1000 { // saturates: an absurd parameter does not become an overflow
				valor = valor*10 + int(data[j]-'0')
			}
			digitos++
			j++
		}
		if j < len(data) && data[j] == 'A' {
			linhas := valor
			if digitos == 0 || valor == 0 {
				linhas = 1
			}
			if linhas >= 2 {
				total++
				if total >= limiteRepintura {
					return true
				}
			}
			i = j + 1
			continue
		}
		i++
	}
	return false
}

// attachReplay returns the RAW bytes to replay on a FRESH attach to prime the
// screen (the stand-in for the capture-pane dtach does not have). Only for
// sessions in the NORMAL buffer (a shell): replaying the raw tail repaints the
// visible history. In ALT-SCREEN (a TUI) it returns nil — there the repaint comes
// from the size "wobble" (the app redraws the current frame); replaying a partial
// frame would only dirty the screen. Best-effort: nil on any failure. Do NOT call
// it on a reconnect (attach=1), so it does not duplicate what the xterm has.
func attachReplay(dataDir, user, name string) []byte {
	// Include the previous generation (.1): otherwise, right after a rotation, the
	// alt-screen ENTER of a TUI started hours ago sits only in .1 and the detection
	// below would falsely say "normal" → replaying a partial frame (garbage) into a
	// TUI session.
	data := readLogRotated(sessionLogPath(dataDir, user, name))
	if len(data) == 0 {
		return nil
	}
	if sessionInAltScreen(data) {
		return nil
	}
	if fluxoERepintado(tailDoLog(data)) {
		return nil
	}
	if len(data) > maxAttachReplayBytes {
		data = data[len(data)-maxAttachReplayBytes:]
		// start at the NEXT line so we never cut in the middle of an escape sequence.
		if i := bytes.IndexByte(data, '\n'); i >= 0 && i+1 < len(data) {
			data = data[i+1:]
		}
	}
	// And trim `dtach`'s message off the END, for the same reason `rawLogTail`
	// trims it: if the slice ends in "clear the screen" or in the goodbye, the
	// client rebuilds everything correctly and then erases it — a black screen
	// with the content one scroll above. It was missing here; this path is less
	// used now that the panel primes itself, but it is still live for clients
	// that do not prime and for when the primer's fetch fails.
	return semRuidoDoDtachNoFim(semRelatorioDeMouse(data))
}

// semRelatorioDeMouse strips from the replay the mouse reports that ended up
// recorded in the log.
//
// WHY THEY ARE THERE. The log records only the pty's OUTPUT. A mouse report is
// INPUT — it lands in the log because a shell in cooked mode with ECHO on echoes
// back what it received. The source has dried up (the gesture only emits mouse
// bytes when the program on the other side asked for tracking), but the history
// has not: real sessions hold thousands of those fragments.
//
// WHY FILTER RATHER THAN KEEP A FAITHFUL RECORD. The replay is not a file, it is
// a screen: it exists so a person can find again what they were looking at. A
// replayed mouse report draws nothing useful — it becomes a stray `<35;80;24M`
// in the middle of the text, which was exactly the "crazy text in the terminal"
// report. The log on disk stays intact; whoever wants the faithful record reads
// the file.
//
// The two shapes that turn up:
//
//	SGR (1006) : ESC [ < 35 ; 80 ; 24 (M|m)   — the current one
//	X10        : ESC [ M followed by 3 bytes   — the old one
//
// THE 128 KiB CUT CAN LAND IN THE MIDDLE of one of those sequences, and that is
// why the scanner only consumes while it still has bytes: a sequence truncated
// at the end disappears whole (it is half a sequence, it draws nothing) and never
// pushes the index past the end — the same trap TestFluxoERepintado_SequenciaTruncadaNaoEstoura
// records for the classifier.
func semRelatorioDeMouse(data []byte) []byte {
	if !bytes.Contains(data, []byte("\x1b[<")) && !bytes.Contains(data, []byte("\x1b[M")) {
		return data // common path: nothing to do, nothing to copy
	}
	out := make([]byte, 0, len(data))
	for i := 0; i < len(data); {
		if n := tamanhoDoRelatorioDeMouse(data[i:]); n > 0 {
			i += n
			continue
		}
		out = append(out, data[i])
		i++
	}
	return out
}

// tamanhoDoRelatorioDeMouse returns how many bytes at the start of b make up a
// mouse report, or 0 if there is none there. A sequence truncated at the end of
// the buffer counts in full: what is left of it draws nothing.
func tamanhoDoRelatorioDeMouse(b []byte) int {
	if len(b) < 3 || b[0] != 0x1b || b[1] != '[' {
		return 0
	}
	// ── WHY X10 WENT AWAY FROM HERE ──────────────────────────────────────
	//
	// There used to be a branch for the X10 mouse report (`ESC [ M` + 3 bytes of
	// coordinates). It is indistinguishable from `CSI M`, which in ECMA-48 is DL
	// (Delete Line) — an everyday editor sequence. The branch ate the sequence
	// AND THE THREE BYTES AFTER IT, which in a DL are content.
	//
	// Both sides are unlikely, but not equally: X10 only shows up if some program
	// asks for tracking mode 9, which practically nothing has asked for in
	// decades (even the old software uses 1000); DL comes out of any editor.
	// Measured across the 28 logs on this machine: ZERO occurrences of `ESC[M`.
	//
	// So the branch goes. The worst case became "a fossil X10 report shows up in
	// the replay" instead of "three bytes of text vanish" — and the first is
	// reversible by eye, the second is not.
	// SGR (1006): ESC [ < digits and ';' up to an 'M' or 'm'.
	if b[2] != '<' {
		return 0
	}
	for i := 3; i < len(b); i++ {
		c := b[i]
		if c == 'M' || c == 'm' {
			return i + 1
		}
		if (c < '0' || c > '9') && c != ';' {
			return 0 // it was not a mouse report — let it through whole
		}
	}
	return len(b) // truncado no fim: some
}

// Close closes the file (idempotent). Errors are swallowed.
func (w *sessionLogWriter) Close() error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f != nil {
		_ = w.f.Close()
		w.f = nil
	}
	return nil
}

// maxRawLogTailBytes: ceiling on the raw slice the app may ask for at once. The
// log's two generations add up to at most 16 MiB (maxSessionLogBytes × 2), so
// this ceiling is the WHOLE log — it exists to put an explicit limit on the
// request, not to cut off history that exists.
const maxRawLogTailBytes = 2 * maxSessionLogBytes

// rawLogTail returns the last maxBytes of the session's RAW log (escapes and all)
// plus the total size available, for a caller that wants to know whether it got
// the whole log.
//
// ## Why RAW, and why this is not the same as tailSessionLog
//
// tailSessionLog cuts by LINES and optionally strips the escapes. Both of those
// destroy the output of a program that redraws, and the measurement is direct:
// in the real "Aplicativo" session log (6.2 MB), the last 5,000 lines of plain
// text hold 511 non-empty lines and 150 distinct ones — almost all of them
// spinner frames. The spinner occupies ONE cell rewritten hundreds of times;
// without the escapes each rewrite becomes a line, and the text comes out
// shredded ("✢i…", "*lg", "Ml"). There is no fixing it on the text side: the
// information that would say where each piece goes is exactly what was removed.
//
// What does know how to assemble this is a terminal emulator, and the app has
// one (libghostty-vt). So the server stops trying to render and starts handing
// over the raw material: the same bytes the PTY wrote, in the order it wrote
// them. Replayed, they give exactly the screen the terminal would give — the
// same log yields 5,058 readable lines instead of 511 shredded ones.
//
// ## The cut at the start
//
// Cutting by byte can land in the middle of an escape sequence, and half a
// sequence is visible garbage (the rest of it is printed as text). So it advances
// past the first line break: an escape sequence never crosses a "\n", so from
// there on the stream is intact by construction.
func rawLogTail(dataDir, user, name string, maxBytes int) (data []byte, total int) {
	if maxBytes <= 0 || maxBytes > maxRawLogTailBytes {
		maxBytes = maxRawLogTailBytes
	}
	corte, total := lerCauda(sessionLogPath(dataDir, user, name), maxBytes)
	if total == 0 {
		return nil, 0
	}
	if len(corte) < total {
		// It cut: advance past the first line break.
		if i := bytes.IndexByte(corte, '\n'); i >= 0 && i+1 < len(corte) {
			corte = corte[i+1:]
		}
	}
	return semRuidoDoDtachNoFim(corte), total
}

// lerCauda returns the last maxBytes of the log (reaching into the previous
// generation when needed) and the total available, WITHOUT loading the whole log
// into memory.
//
// It matters more now than it used to: the panel primes the screen with this
// slice on every attach, and the permanent recorder makes the log grow even with
// nobody watching. Reading 16 MiB to return 2 MiB, on every tab opened, would be
// paying dearly for a cut `ReadAt` knows how to make directly.
func lerCauda(path string, maxBytes int) ([]byte, int) {
	tam := func(p string) int64 {
		fi, err := os.Stat(p)
		if err != nil {
			return 0
		}
		return fi.Size()
	}
	tamAnterior, tamAtual := tam(path+".1"), tam(path)
	total := int(tamAnterior + tamAtual)
	if total == 0 {
		return nil, 0
	}
	if maxBytes > total {
		maxBytes = total
	}
	// Where to start, in the continuous numbering "previous followed by current".
	inicio := int64(total - maxBytes)
	buf := make([]byte, 0, maxBytes)

	leDe := func(p string, desde int64, quanto int64) {
		if quanto <= 0 {
			return
		}
		f, err := os.Open(p)
		if err != nil {
			return
		}
		defer f.Close()
		parte := make([]byte, quanto)
		n, err := f.ReadAt(parte, desde)
		if n > 0 {
			buf = append(buf, parte[:n]...)
		}
		_ = err // short or EOF: return what we got, it is best-effort like the rest
	}

	if inicio < tamAnterior {
		leDe(path+".1", inicio, tamAnterior-inicio)
		leDe(path, 0, tamAtual)
	} else {
		leDe(path, inicio-tamAnterior, tamAtual-(inicio-tamAnterior))
	}
	return buf, total
}

// semRuidoDoDtachNoFim strips from the END of the slice what `dtach` says to the
// CLIENT, and nothing else.
//
// ## What it says, and why it spoils things
//
// Two phrases, both inside the binary:
//
//	ESC[H ESC[J                      on attach  — "start with a clean screen"
//	ESC[999H \r\n [detached] \r\n      on exit     — "you have detached"
//
// On a client's screen both are correct. In the record of what the PROGRAM
// painted both are a lie — and the second is worse, because the `ESC[999H`
// throws the cursor onto the last line and the `\n` there SCROLLS THE WHOLE
// SCREEN up.
//
// The app rebuilds the session by replaying this slice. When the noise is at the
// END, it rebuilds everything correctly and then erases and scrolls: a dark
// screen with the text one scroll above. That was the owner's report, three
// times over.
//
// ## Why only at the END, and why the file is not touched
//
// In the MIDDLE of the log the same noise is followed by the output that came
// after it, and the replay recomposes itself — removing it would change what the
// person saw. Only the noise at the end has nothing after it to redraw the screen.
//
// And the file is left intact on purpose. The owner needs the history and said
// he will never delete it; fixing his past by rewriting the record would trade a
// defect for a loss. You trim what goes out, not what is stored.
//
// The loop repeats because successive attaches and exits stack up: the "Vpsm"
// session's file once ended with two clears and one goodbye, in that order.
func semRuidoDoDtachNoFim(b []byte) []byte {
	for {
		antes := len(b)

		// The goodbye only counts if it is REALLY at the end: an old `[detached]`,
		// followed by real output, is legitimate history and stays.
		if i := bytes.LastIndex(b, textoDeSaidaDoDtach); i >= 0 && len(b)-i <= 32 {
			corte := i
			if j := bytes.LastIndex(b[:i], marcaDeSaidaDoDtach); j >= 0 && i-j <= 16 {
				corte = j
			}
			b = b[:corte]
		}
		b = bytes.TrimSuffix(b, limpezaDeAttachDoDtach)

		if len(b) == antes {
			return b
		}
	}
}
