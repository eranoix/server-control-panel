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

const maxRecord = 4000

type Sink struct {
	mu  sync.Mutex
	day string
	f   *os.File
	dir string
	now func() time.Time
}

func NewSink(dir string) (*Sink, error) {
	if err := os.MkdirAll(dir, 0750); err != nil {
		return nil, err
	}
	return &Sink{dir: dir, now: time.Now}, nil
}

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

	line := make([]byte, len(rec)+1)
	copy(line, rec)
	line[len(rec)] = '\n'

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
	_, err := s.f.Write(line)
	return err
}

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

type ReadStats struct{ Valid, Invalid int }

const maxLine = 64 << 10

func ReadDay(path string, fn func(map[string]any)) (ReadStats, error) {
	var st ReadStats
	f, err := os.Open(path)
	if err != nil {
		return st, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), maxLine)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(line, &m); err != nil {
			st.Invalid++
			continue
		}
		st.Valid++
		if fn != nil {
			fn(m)
		}
	}
	if err := sc.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			st.Invalid++
			return st, nil
		}
		return st, err
	}
	return st, nil
}
