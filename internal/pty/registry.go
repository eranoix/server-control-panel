package pty

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type SessionRecord struct {
	Name    string `json:"name"`
	Socket  string `json:"socket,omitempty"`
	PID     int    `json:"pid,omitempty"`
	Created int64  `json:"created"`
	Backend string `json:"backend"`
}

type Registry struct {
	mu     sync.RWMutex
	seq    uint64
	saveMu sync.Mutex
	saved  uint64
	path   string
	m      map[string]SessionRecord
}

func LoadRegistry(path string) (*Registry, error) {
	r := &Registry{path: path, m: map[string]SessionRecord{}}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return r, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return r, nil
	}
	if err := json.Unmarshal(data, &r.m); err != nil {
		return nil, err
	}
	if r.m == nil {
		r.m = map[string]SessionRecord{}
	}
	return r, nil
}

func (r *Registry) Get(name string) (SessionRecord, bool) {
	if r == nil || name == "" {
		return SessionRecord{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	rec, ok := r.m[name]
	return rec, ok
}

func (r *Registry) Has(name string) bool {
	_, ok := r.Get(name)
	return ok
}

func (r *Registry) Put(rec SessionRecord) error {
	if r == nil || rec.Name == "" {
		return nil
	}
	if rec.Created == 0 {
		rec.Created = time.Now().Unix()
	}
	r.mu.Lock()
	r.m[rec.Name] = rec
	r.seq++
	snapshot, seq := cloneRecords(r.m), r.seq
	r.mu.Unlock()
	return r.persist(snapshot, seq)
}

func (r *Registry) Delete(name string) error {
	if r == nil || name == "" {
		return nil
	}
	r.mu.Lock()
	if _, ok := r.m[name]; !ok {
		r.mu.Unlock()
		return nil
	}
	delete(r.m, name)
	r.seq++
	snapshot, seq := cloneRecords(r.m), r.seq
	r.mu.Unlock()
	return r.persist(snapshot, seq)
}

func (r *Registry) Rename(old, newName string) error {
	if r == nil || old == "" || newName == "" || old == newName {
		return nil
	}
	r.mu.Lock()
	rec, ok := r.m[old]
	if !ok {
		r.mu.Unlock()
		return nil
	}
	delete(r.m, old)
	rec.Name = newName
	r.m[newName] = rec
	r.seq++
	snapshot, seq := cloneRecords(r.m), r.seq
	r.mu.Unlock()
	return r.persist(snapshot, seq)
}

func (r *Registry) List() []SessionRecord {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]SessionRecord, 0, len(r.m))
	for _, rec := range r.m {
		out = append(out, rec)
	}
	return out
}

func cloneRecords(m map[string]SessionRecord) map[string]SessionRecord {
	out := make(map[string]SessionRecord, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func (r *Registry) persist(snapshot map[string]SessionRecord, seq uint64) error {
	r.saveMu.Lock()
	defer r.saveMu.Unlock()
	if seq <= r.saved {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(r.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	tmp := r.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, r.path); err != nil {
		return err
	}
	r.saved = seq
	return nil
}
