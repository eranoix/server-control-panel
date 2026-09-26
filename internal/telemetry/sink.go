package telemetry

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var (
	ErrTooLong  = errors.New("telemetry: record above 4000 bytes")
	ErrEmbedded = errors.New("telemetry: record contains a line break")
	ErrEmpty    = errors.New("telemetry: empty record")
)

// maxRecord sits below the page size: write(2)'s practical atomicity disappears
// above that. A telemetry event is small by nature — the closed schema has
// 6 fields.
const maxRecord = 4000

// Sink writes events as append-only JSONL, one file per day
// (`<dir>/YYYY-MM-DD.jsonl`).
//
// No fsync per event — a decision recorded with its cost made explicit: the
// maximum loss in a crash is the page cache, and the measurement here is
// relative (which screen gets used most), not accounting. An fsync per event
// would cost latency and SSD wear to buy a guarantee nobody here needs.
type Sink struct {
	mu  sync.Mutex
	day string
	f   *os.File
	dir string
	now func() time.Time
}

// NewSink creates the directory if it does not exist — `data/telemetry/` does
// not exist in the runtime yet — and returns the sink ready. It does not open a
// file: the first Write is what decides the day.
func NewSink(dir string) (*Sink, error) {
	if err := os.MkdirAll(dir, 0750); err != nil {
		return nil, err
	}
	return &Sink{dir: dir, now: time.Now}, nil
}

// Write writes one record (WITHOUT a trailing '\n').
//
// O_APPEND makes the pair (seek to end + write) atomic across processes; the
// mutex prevents interleaving between goroutines of the same process. The two
// together, not one alone.
//
// The line break goes in the SAME write(2) as the record, in a buffer of its own.
// Two separate writes would hand back the interleaving window that O_APPEND
// exists to close; and an `append(rec,'\n')` would write into the caller's array
// whenever there is spare capacity — which is exactly the handler's json.Marshal.
func (s *Sink) Write(rec []byte) error {
	if len(rec) == 0 {
		return ErrEmpty
	}
	if len(rec) > maxRecord {
		return ErrTooLong
	}
	if bytes.IndexByte(rec, '\n') >= 0 {
		return ErrEmbedded
	}

	linha := make([]byte, len(rec)+1)
	copy(linha, rec)
	linha[len(rec)] = '\n'

	s.mu.Lock()
	defer s.mu.Unlock()

	d := s.now().Format("2006-01-02")
	if d != s.day || s.f == nil {
		if s.f != nil {
			s.f.Close()
		}
		f, err := os.OpenFile(filepath.Join(s.dir, d+".jsonl"),
			os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0640)
		if err != nil {
			s.f, s.day = nil, ""
			return err
		}
		s.f, s.day = f, d
	}
	_, err := s.f.Write(linha)
	return err
}

// Close closes the open file. It is idempotent, and a later Write reopens it.
func (s *Sink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.f == nil {
		return nil
	}
	err := s.f.Close()
	s.f, s.day = nil, ""
	return err
}

// ReadStats is the result of reading one telemetry file.
type ReadStats struct{ Valid, Invalid int }

// maxLinha is the reader's ceiling: a valid record fits in maxRecord, and the
// slack exists only so a corrupted line is COUNTED instead of blowing the scanner.
const maxLinha = 64 << 10

// ReadDay returns the valid records and COUNTS the invalid ones.
//
// A crash in the middle of a write leaves a partial line; the report says how
// many there were, instead of dying or pretending they do not exist. An error is
// returned only for I/O failure — a bad line is never a read error, it is a statistic.
//
// `fn` may be nil when only the counts matter.
func ReadDay(path string, fn func(map[string]any)) (ReadStats, error) {
	var st ReadStats
	f, err := os.Open(path)
	if err != nil {
		return st, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), maxLinha)
	for sc.Scan() {
		linha := bytes.TrimSpace(sc.Bytes())
		if len(linha) == 0 {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(linha, &m); err != nil {
			st.Invalid++
			continue
		}
		st.Valid++
		if fn != nil {
			fn(m)
		}
	}
	// A line longer than maxLinha makes the scanner stop with bufio.ErrTooLong.
	// That is content corruption, not I/O failure: it counts as invalid and the
	// read ends — reported, never silent.
	if err := sc.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			st.Invalid++
			return st, nil
		}
		return st, err
	}
	return st, nil
}
