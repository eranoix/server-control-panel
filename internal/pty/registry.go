// registry.go — the session-registry sidecar: the tool-agnostic source of truth
// about "which sessions exist".
//
// The previous engine answered List/Has/Rename/Kill natively. dtach does NOT —
// it only
// keeps a process alive behind a socket, with no catalogue. This registry fills
// that gap: a map name → {socket, pid, created, backend} persisted in
// <DataDir>/session-registry.json (atomic write, the same pattern as
// ownership.go).
//
// FILE NAME: session-registry.json — deliberately NOT "sessions.json", which is
// already used by the `sessions` package (JWT login sessions, see api.go).
//
// The type is loaded at boot and persists. Per-session population
// (Put on create, Delete on kill) arrives with the dtachBackend.
// (The previous backend answered List/Has straight from its own engine, without
// depending on this; it no longer exists — the registry is the single source.)
package pty

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// SessionRecord is the registry's entry. socket/pid stay empty for sessions from
// the previous engine (which exposed no single per-session pid); they are filled
// in by the dtachBackend, where socket file + live pid == "the session exists".
type SessionRecord struct {
	Name    string `json:"name"`
	Socket  string `json:"socket,omitempty"`
	PID     int    `json:"pid,omitempty"`
	Created int64  `json:"created"`
	// Always "dtach" today. The field stays because it is written into the
	// records already on disk — dropping it would make the old JSON lose
	// information on the first re-read, for no gain at all.
	Backend string `json:"backend"`
}

// Registry is the persistent session catalogue. Its methods are safe for
// concurrent use; a nil receiver is tolerated on the read paths (Get/Has/List) so
// boot-time callers need not handle the "not loaded yet" state.
type Registry struct {
	mu     sync.RWMutex
	seq    uint64     // monotonic mutation version (under mu) — stamps every snapshot
	saveMu sync.Mutex // serializes the disk write WITHOUT holding mu (snapshot outside the lock)
	saved  uint64     // highest seq already persisted (under saveMu) — discards a stale snapshot
	path   string
	m      map[string]SessionRecord
}

// LoadRegistry opens (or initialises) the registry at path. A missing file = an
// empty map; malformed JSON is an error (fail loudly, do not lose data silently).
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

// Get returns name's record (ok=false if absent). Safe on a nil receiver.
func (r *Registry) Get(name string) (SessionRecord, bool) {
	if r == nil || name == "" {
		return SessionRecord{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	rec, ok := r.m[name]
	return rec, ok
}

// Has reports whether name is in the registry. Safe on a nil receiver.
func (r *Registry) Has(name string) bool {
	_, ok := r.Get(name)
	return ok
}

// Put inserts/updates the record. Created is filled in (with now) when it
// arrives zeroed. A nil receiver is a no-op (registry disabled).
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

// Delete removes name's record (idempotent). A nil receiver is a no-op.
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

// Rename re-keys the record from old→newName, preserving socket/pid/created and
// updating the Name field. It is how dtach gains rename-session (the display name
// decouples from the socket). A no-op if old does not exist; idempotent.
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

// List returns every record (a copy). No ordering guarantee.
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

// persist writes a snapshot atomically (write-temp + rename). It serialises
// through saveMu (separate from the in-memory mu), like ownership.persist. It
// orders by `seq`: an older snapshot is discarded when a newer one has already
// been persisted (otherwise two concurrent persists wrote out of order and an old
// Put could vanish from disk — breaking resolveSocket for a session renamed
// before a restart).
func (r *Registry) persist(snapshot map[string]SessionRecord, seq uint64) error {
	r.saveMu.Lock()
	defer r.saveMu.Unlock()
	if seq <= r.saved {
		return nil // a newer state is already on disk
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
