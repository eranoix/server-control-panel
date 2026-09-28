package gameservers

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Sample struct {
	T       int64   `json:"t"`
	CPU     float64 `json:"cpu"`
	Mem     float64 `json:"mem"`
	Players int     `json:"players"`
	Up      bool    `json:"up"`
}

const (
	histInterval = 60 * time.Second
	histMax      = 1440
)

type history struct {
	mu    sync.RWMutex
	path  string
	data  map[string][]Sample
	dirty bool
}

func newHistory(dataDir string) *history {
	h := &history{
		path: filepath.Join(dataDir, "gameservers-history.json"),
		data: map[string][]Sample{},
	}
	if b, err := os.ReadFile(h.path); err == nil {
		_ = json.Unmarshal(b, &h.data)
	}
	return h
}

func (h *history) push(id string, s Sample) {
	h.mu.Lock()
	defer h.mu.Unlock()
	arr := append(h.data[id], s)
	if len(arr) > histMax {
		arr = arr[len(arr)-histMax:]
	}
	h.data[id] = arr
	h.dirty = true
}

func (h *history) get(id string, since int64) []Sample {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := []Sample{}
	for _, s := range h.data[id] {
		if s.T >= since {
			out = append(out, s)
		}
	}
	return out
}

func (h *history) flush() {
	h.mu.Lock()
	if !h.dirty {
		h.mu.Unlock()
		return
	}
	b, err := json.Marshal(h.data)
	h.dirty = false
	h.mu.Unlock()
	if err != nil {
		return
	}
	tmp := h.path + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil {
		_ = os.Rename(tmp, h.path)
	}
}

func (m *Manager) StartSampler(ctx context.Context) {
	go func() {
		tick := time.NewTicker(histInterval)
		flush := time.NewTicker(5 * time.Minute)
		defer tick.Stop()
		defer flush.Stop()
		for {
			select {
			case <-ctx.Done():
				m.hist.flush()
				return
			case <-flush.C:
				m.hist.flush()
			case <-tick.C:
				for _, s := range m.List() {
					st := m.Status(ctx, s)
					m.hist.push(s.ID, Sample{
						T:       time.Now().Unix(),
						CPU:     round2(st.CPUPerc),
						Mem:     round2(st.MemMB),
						Players: st.Players,
						Up:      st.State == "running",
					})
				}
			}
		}
	}()
}

func round2(f float64) float64 { return float64(int(f*100+0.5)) / 100 }

func (m *Manager) History(id string, hours int) []Sample {
	if hours <= 0 || hours > 24 {
		hours = 6
	}
	return m.hist.get(id, time.Now().Add(-time.Duration(hours)*time.Hour).Unix())
}
