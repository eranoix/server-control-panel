package pty

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

const maxSessionLogBytes = 8 << 20

func sessionLogPath(dataDir, user, name string) string {
	return filepath.Join(dataDir, "users", safeSessionName(user), "session-logs", safeSessionName(name)+".log")
}

type sessionLogWriter struct {
	mu   sync.Mutex
	path string
	f    *os.File
	size int64
}

func openSessionLog(dataDir, user, name string) *sessionLogWriter {
	return openWriter(sessionLogPath(dataDir, user, name))
}

func openWriter(path string) *sessionLogWriter {
	w := &sessionLogWriter{path: path}
	if err := os.MkdirAll(filepath.Dir(w.path), 0o700); err != nil {
		return w
	}
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return w
	}
	w.f = f
	if fi, err := f.Stat(); err == nil {
		w.size = fi.Size()
	}
	return w
}

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

func (w *sessionLogWriter) dropLast(n int) {
	if w == nil || n <= 0 {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil || w.size < int64(n) {
		return
	}
	target := w.size - int64(n)
	if err := w.f.Truncate(target); err != nil {
		return
	}
	w.size = target
}

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

var ansiRE = regexp.MustCompile(
	"\x1b\\[[0-9;?]*[ -/]*[@-~]" +
		"|\x1b\\][^\x07\x1b]*(?:\x07|\x1b\\\\)" +
		"|\x1b[()][A-Za-z0-9]" +
		"|[\x00-\x08\x0b\x0c\x0e-\x1f]")

func stripANSI(s string) string { return ansiRE.ReplaceAllString(s, "") }

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

const maxAttachReplayBytes = 128 << 10

var (
	altScreenEnter = [][]byte{[]byte("\x1b[?1049h"), []byte("\x1b[?1047h"), []byte("\x1b[?47h")}
	altScreenLeave = [][]byte{[]byte("\x1b[?1049l"), []byte("\x1b[?1047l"), []byte("\x1b[?47l")}
)

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

const repaintLimit = 20

func logTail(data []byte) []byte {
	if len(data) > maxAttachReplayBytes {
		return data[len(data)-maxAttachReplayBytes:]
	}
	return data
}

func isRepaintStream(data []byte) bool {
	total := 0
	for i := 0; i+1 < len(data); {
		if data[i] != 0x1B || data[i+1] != '[' {
			i++
			continue
		}
		j := i + 2
		value, digits := 0, 0
		for j < len(data) && data[j] >= '0' && data[j] <= '9' {
			if value < 1000 {
				value = value*10 + int(data[j]-'0')
			}
			digits++
			j++
		}
		if j < len(data) && data[j] == 'A' {
			lines := value
			if digits == 0 || value == 0 {
				lines = 1
			}
			if lines >= 2 {
				total++
				if total >= repaintLimit {
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

func attachReplay(dataDir, user, name string) []byte {
	data := readLogRotated(sessionLogPath(dataDir, user, name))
	if len(data) == 0 {
		return nil
	}
	if sessionInAltScreen(data) {
		return nil
	}
	if isRepaintStream(logTail(data)) {
		return nil
	}
	if len(data) > maxAttachReplayBytes {
		data = data[len(data)-maxAttachReplayBytes:]
		if i := bytes.IndexByte(data, '\n'); i >= 0 && i+1 < len(data) {
			data = data[i+1:]
		}
	}
	return trimTrailingDtachNoise(stripMouseReports(data))
}

func stripMouseReports(data []byte) []byte {
	if !bytes.Contains(data, []byte("\x1b[<")) && !bytes.Contains(data, []byte("\x1b[M")) {
		return data
	}
	out := make([]byte, 0, len(data))
	for i := 0; i < len(data); {
		if n := mouseReportLen(data[i:]); n > 0 {
			i += n
			continue
		}
		out = append(out, data[i])
		i++
	}
	return out
}

func mouseReportLen(b []byte) int {
	if len(b) < 3 || b[0] != 0x1b || b[1] != '[' {
		return 0
	}
	if b[2] != '<' {
		return 0
	}
	for i := 3; i < len(b); i++ {
		c := b[i]
		if c == 'M' || c == 'm' {
			return i + 1
		}
		if (c < '0' || c > '9') && c != ';' {
			return 0
		}
	}
	return len(b)
}

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

const maxRawLogTailBytes = 2 * maxSessionLogBytes

func rawLogTail(dataDir, user, name string, maxBytes int) (data []byte, total int) {
	if maxBytes <= 0 || maxBytes > maxRawLogTailBytes {
		maxBytes = maxRawLogTailBytes
	}
	cut, total := readTail(sessionLogPath(dataDir, user, name), maxBytes)
	if total == 0 {
		return nil, 0
	}
	if len(cut) < total {
		if i := bytes.IndexByte(cut, '\n'); i >= 0 && i+1 < len(cut) {
			cut = cut[i+1:]
		}
	}
	return trimTrailingDtachNoise(cut), total
}

func readTail(path string, maxBytes int) ([]byte, int) {
	tam := func(p string) int64 {
		fi, err := os.Stat(p)
		if err != nil {
			return 0
		}
		return fi.Size()
	}
	prevSize, curSize := tam(path+".1"), tam(path)
	total := int(prevSize + curSize)
	if total == 0 {
		return nil, 0
	}
	if maxBytes > total {
		maxBytes = total
	}
	start := int64(total - maxBytes)
	buf := make([]byte, 0, maxBytes)

	leDe := func(p string, since int64, amount int64) {
		if amount <= 0 {
			return
		}
		f, err := os.Open(p)
		if err != nil {
			return
		}
		defer f.Close()
		part := make([]byte, amount)
		n, err := f.ReadAt(part, since)
		if n > 0 {
			buf = append(buf, part[:n]...)
		}
		_ = err
	}

	if start < prevSize {
		leDe(path+".1", start, prevSize-start)
		leDe(path, 0, curSize)
	} else {
		leDe(path, start-prevSize, curSize-(start-prevSize))
	}
	return buf, total
}

func trimTrailingDtachNoise(b []byte) []byte {
	for {
		before := len(b)

		if i := bytes.LastIndex(b, dtachExitText); i >= 0 && len(b)-i <= 32 {
			cut := i
			if j := bytes.LastIndex(b[:i], dtachExitMark); j >= 0 && i-j <= 16 {
				cut = j
			}
			b = b[:cut]
		}
		b = bytes.TrimSuffix(b, dtachAttachClear)

		if len(b) == before {
			return b
		}
	}
}
