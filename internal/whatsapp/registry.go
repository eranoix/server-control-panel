package whatsapp

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
	PortRangeMin = 3100
	PortRangeMax = 3199

	registryFile = "port-registry.json"
)

var ErrPortRangeExhausted = errors.New("whatsapp: port range exhausted (3100-3199)")

type Entry struct {
	User string `json:"user"`
	Port int    `json:"port"`
}

type Registry struct {
	mu      sync.Mutex
	root    string
	path    string
	entries map[string]int
}

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
			continue
		}
		r.entries[e.User] = e.Port
	}
	return nil
}

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

func (r *Registry) Lookup(user string) (int, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.entries[user]
	return p, ok
}

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
