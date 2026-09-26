package whatsapp

// registry.go — allocates TCP ports from the 3100-3199 range for per-profile
// WAHA containers, persisting the allocation in
// /var/lib/vpsm-whatsapp/port-registry.json.
//
// The range cap (100 profiles) is the design's practical ceiling — beyond
// that the RAM taken by WAHA Core containers becomes prohibitive (~150 MB
// each). Once we hit the limit, move to WAHA Plus (one container, N sessions).
//
// API:
//   - Alloc(user) assigns a new port or returns the existing one (idempotent).
//   - Lookup(user) reads without allocating — used on the hot path (webhook routing).
//   - Release(user) frees the port when the profile is decommissioned.
//
// Concurrency: an internal mutex covers the map plus the atomic JSON flush.
// Concurrent Allocs never return the same port; a Release during a Lookup is
// safe (it returns false instead).

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

const (
	// PortRangeMin/PortRangeMax bound the port pool. Each container exposes the
	// WAHA API through network_mode: host, so the port is globally unique on
	// the host.
	PortRangeMin = 3100
	PortRangeMax = 3199

	registryFile = "port-registry.json"
)

// ErrPortRangeExhausted: 100 profiles allocated — no room left until one is freed.
var ErrPortRangeExhausted = errors.New("whatsapp: port range exhausted (3100-3199)")

// Entry is one allocation persisted to disk.
type Entry struct {
	User string `json:"user"`
	Port int    `json:"port"`
}

// Registry records who is on which port. Persisted in
// <root>/port-registry.json (an atomic write via rename).
type Registry struct {
	mu      sync.Mutex
	root    string
	path    string
	entries map[string]int // user → port
}

// NewRegistry opens or creates the registry under root (normally
// /var/lib/vpsm-whatsapp). It creates root with 0o755 if needed.
func NewRegistry(root string) (*Registry, error) {
	if root == "" {
		return nil, errors.New("whatsapp: registry root required")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("whatsapp registry mkdir: %w", err)
	}
	r := &Registry{
		root:    root,
		path:    filepath.Join(root, registryFile),
		entries: map[string]int{},
	}
	if err := r.load(); err != nil {
		return nil, err
	}
	return r, nil
}

// load reads the JSON from disk — ENOENT is fine (an empty registry).
func (r *Registry) load() error {
	data, err := os.ReadFile(r.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("whatsapp registry read: %w", err)
	}
	var raw struct {
		Entries []Entry `json:"entries"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("whatsapp registry parse: %w", err)
	}
	for _, e := range raw.Entries {
		if e.User == "" || e.Port < PortRangeMin || e.Port > PortRangeMax {
			continue // entradas corrompidas viram lixo, ignoradas
		}
		r.entries[e.User] = e.Port
	}
	return nil
}

// flush writes the state to disk atomically (tmp + rename). The caller holds mu.
func (r *Registry) flush() error {
	out := make([]Entry, 0, len(r.entries))
	for u, p := range r.entries {
		out = append(out, Entry{User: u, Port: p})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Port < out[j].Port })
	body, err := json.MarshalIndent(map[string]any{"entries": out}, "", "  ")
	if err != nil {
		return err
	}
	tmp := r.path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, r.path)
}

// Alloc returns the port assigned to user, creating a new one when necessary.
// Idempotent: repeated calls return the same port.
//
// Strategy: take the lowest free port in the range. It is deterministic
// (which makes inspection and migration easier) and keeps the ports dense.
func (r *Registry) Alloc(user string) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if user == "" {
		return 0, errors.New("whatsapp registry: empty user")
	}
	if p, ok := r.entries[user]; ok {
		return p, nil
	}
	used := make(map[int]struct{}, len(r.entries))
	for _, p := range r.entries {
		used[p] = struct{}{}
	}
	for p := PortRangeMin; p <= PortRangeMax; p++ {
		if _, taken := used[p]; taken {
			continue
		}
		r.entries[user] = p
		if err := r.flush(); err != nil {
			delete(r.entries, user)
			return 0, err
		}
		return p, nil
	}
	return 0, ErrPortRangeExhausted
}

// Lookup returns user's port without allocating. ok=false when none was ever allocated.
func (r *Registry) Lookup(user string) (int, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.entries[user]
	return p, ok
}

// Release returns user's port to the pool. A no-op when user was not registered.
func (r *Registry) Release(user string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.entries[user]; !ok {
		return nil
	}
	old := r.entries[user]
	delete(r.entries, user)
	if err := r.flush(); err != nil {
		r.entries[user] = old
		return err
	}
	return nil
}

// List returns an ordered snapshot of the entries (read-only).
func (r *Registry) List() []Entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Entry, 0, len(r.entries))
	for u, p := range r.entries {
		out = append(out, Entry{User: u, Port: p})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Port < out[j].Port })
	return out
}
