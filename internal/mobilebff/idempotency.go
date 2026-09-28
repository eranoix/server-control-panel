package mobilebff

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Idempotency struct {
	mu      sync.Mutex
	file    string
	entries map[string]idempotentEntry
	now     func() time.Time
}

type idempotentEntry struct {
	Body   string `json:"body"`
	Status int    `json:"status"`
	At     int64  `json:"when_ms"`
}

const (
	idempotencyTTL = 24 * time.Hour

	idempotencyCap = 10_000
)

func NewIdempotency(dataDir string) *Idempotency {
	i := &Idempotency{
		file:    filepath.Join(dataDir, "mobile-idempotency.json"),
		entries: map[string]idempotentEntry{},
		now:     time.Now,
	}
	i.load()
	return i
}

func (i *Idempotency) Recall(key string) (body string, status int, ok bool) {
	if i == nil || key == "" {
		return "", 0, false
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	e, exists := i.entries[key]
	if !exists {
		return "", 0, false
	}
	if i.now().UnixMilli()-e.At > idempotencyTTL.Milliseconds() {
		delete(i.entries, key)
		return "", 0, false
	}
	return e.Body, e.Status, true
}

func (i *Idempotency) Remember(key, body string, status int) {
	if i == nil || key == "" {
		return
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	i.entries[key] = idempotentEntry{
		Body:   body,
		Status: status,
		At:     i.now().UnixMilli(),
	}
	i.prune()
	i.save()
}

func (i *Idempotency) prune() {
	limit := i.now().UnixMilli() - idempotencyTTL.Milliseconds()
	for k, e := range i.entries {
		if e.At < limit {
			delete(i.entries, k)
		}
	}
	for len(i.entries) > idempotencyCap {
		oldest, when := "", int64(1)<<62
		for k, e := range i.entries {
			if e.At < when {
				oldest, when = k, e.At
			}
		}
		if oldest == "" {
			return
		}
		delete(i.entries, oldest)
	}
}

func (i *Idempotency) load() {
	b, err := os.ReadFile(i.file)
	if err != nil {
		return
	}
	var loaded map[string]idempotentEntry
	if json.Unmarshal(b, &loaded) != nil {
		return
	}
	i.entries = loaded
}

func (i *Idempotency) save() {
	b, err := json.Marshal(i.entries)
	if err != nil {
		return
	}
	tmp := i.file + ".tmp"
	if os.WriteFile(tmp, b, 0o600) != nil {
		return
	}
	_ = os.Rename(tmp, i.file)
}

func persistFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o600)
}
