package pty

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

type Ownership struct {
	mu     sync.RWMutex
	seq    uint64
	saveMu sync.Mutex
	saved  uint64
	path   string
	m      map[string]string
}

var ErrOwnedByOther = errors.New("pty: session owned by another profile")

func LoadOwnership(path string) (*Ownership, error) {
	o := &Ownership{path: path, m: map[string]string{}}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return o, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return o, nil
	}
	if err := json.Unmarshal(data, &o.m); err != nil {
		return nil, err
	}
	if o.m == nil {
		o.m = map[string]string{}
	}
	return o, nil
}

func (o *Ownership) Owner(name string) string {
	if o == nil || name == "" {
		return ""
	}
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.m[name]
}

func (o *Ownership) Claim(name, user string) error {
	if o == nil || name == "" || user == "" {
		return nil
	}
	o.mu.Lock()
	if cur, ok := o.m[name]; ok && cur != "" && cur != user {
		o.mu.Unlock()
		return ErrOwnedByOther
	}
	if o.m[name] == user {
		o.mu.Unlock()
		return nil
	}
	o.m[name] = user
	o.seq++
	snapshot, seq := cloneMap(o.m), o.seq
	o.mu.Unlock()
	return o.persist(snapshot, seq)
}

func (o *Ownership) Release(name string) error {
	if o == nil || name == "" {
		return nil
	}
	o.mu.Lock()
	if _, ok := o.m[name]; !ok {
		o.mu.Unlock()
		return nil
	}
	delete(o.m, name)
	o.seq++
	snapshot, seq := cloneMap(o.m), o.seq
	o.mu.Unlock()
	return o.persist(snapshot, seq)
}

const AudienceAll = "*"

func (o *Ownership) Assign(name, target string) error {
	if o == nil || name == "" || target == "" {
		return nil
	}
	o.mu.Lock()
	if o.m[name] == target {
		o.mu.Unlock()
		return nil
	}
	o.m[name] = target
	o.seq++
	snapshot, seq := cloneMap(o.m), o.seq
	o.mu.Unlock()
	return o.persist(snapshot, seq)
}

func (o *Ownership) Rename(old, new string) error {
	if o == nil || old == "" || new == "" || old == new {
		return nil
	}
	o.mu.Lock()
	owner, ok := o.m[old]
	if !ok {
		o.mu.Unlock()
		return nil
	}
	delete(o.m, old)
	o.m[new] = owner
	o.seq++
	snapshot, seq := cloneMap(o.m), o.seq
	o.mu.Unlock()
	return o.persist(snapshot, seq)
}

func cloneMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func (o *Ownership) VisibleTo(name, user string, primary bool) bool {
	if name == "" || user == "" {
		return false
	}
	owner := o.Owner(name)
	if owner == user {
		return true
	}
	if owner == AudienceAll {
		return true
	}
	if owner == "" && primary {
		return true
	}
	return false
}

func (o *Ownership) SessionsOf(user string) []string {
	if user == "" {
		return nil
	}
	o.mu.RLock()
	defer o.mu.RUnlock()
	out := make([]string, 0, 4)
	for name, owner := range o.m {
		if owner == AudienceAll {
			continue
		}
		if owner == user {
			out = append(out, name)
		}
	}
	return out
}

func (o *Ownership) persist(snapshot map[string]string, seq uint64) error {
	o.saveMu.Lock()
	defer o.saveMu.Unlock()
	if seq <= o.saved {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(o.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	tmp := o.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, o.path); err != nil {
		return err
	}
	o.saved = seq
	return nil
}
