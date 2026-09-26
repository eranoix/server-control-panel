package pty

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

// Ownership is the tenant boundary for sessions (dtach).
//
// The legacy scheme encoded ownership in the session name itself
// ("vpsm-<user>-<tab>"); that was dropped so users can pick session
// names freely ("venice", "main", whatever). Ownership now lives in a
// sidecar JSON map[session_name]owner persisted at
// <DataDir>/session-ownership.json, written atomically on every mutation.
//
// "" as an owner means the session is unowned — surfaced to system admins
// (config.Primary or any user flagged Admin; see config.IsAdmin) only. A
// session that exists but has no entry here has the same semantics.
//
// All public methods are safe for concurrent use. A nil receiver is
// tolerated by the read paths (Owner, VisibleTo) so callers in startup
// code don't have to special-case the not-yet-loaded state.
type Ownership struct {
	mu     sync.RWMutex
	seq    uint64     // monotonic mutation version (under mu) — stamps every snapshot
	saveMu sync.Mutex // serializes the disk write WITHOUT blocking mu (snapshot outside the lock)
	saved  uint64     // highest seq already persisted (under saveMu) — discards a stale snapshot
	path   string
	m      map[string]string
}

// ErrOwnedByOther is returned by Claim when the session name is already
// in the registry under a different owner. Callers surface it as 404
// (the standard "I refuse to tell you that exists" leak-protector).
var ErrOwnedByOther = errors.New("pty: session owned by another profile")

// LoadOwnership opens (or initialises) the registry at path. A missing
// file is treated as an empty map; malformed JSON is an error so we
// fail loud rather than silently lose ownership data.
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

// Owner returns the registered owner of name, or "" if unowned. Safe on
// a nil receiver (returns "").
func (o *Ownership) Owner(name string) string {
	if o == nil || name == "" {
		return ""
	}
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.m[name]
}

// Claim assigns name to user. Idempotent when name is already owned by
// user. Returns ErrOwnedByOther if a different user already owns it.
// A nil receiver is a no-op (treated as "registry disabled").
//
// Pattern: mutate in memory under mu.Lock, release, then persist under saveMu.
// Disk I/O (~5-10ms) does not block other Claim/Release/VisibleTo calls. saveMu
// serialises the write — without it, two concurrent snapshots could revert one
// another on a rename.
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

// Release removes the ownership entry for name. The session itself
// is not touched. Idempotent; nil receiver is a no-op.
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

// AudienceAll is the sentinel owner value meaning "visible to everyone"
// A session assigned to "*" appears in every user's picker and is
// attachable by anyone, but it counts toward nobody's quota and only admins can
// destructively manage it (kill/rename/detach) — see OwnsSession. Picked
// "*" because safeSessionName strips it, so no real session name can collide.
const AudienceAll = "*"

// Assign force-sets the owner of name to target, overwriting any existing owner
// (unlike Claim, which refuses to steal). Session sharing uses this to reattribute a
// session's audience from the admin session manager: target is a username or the
// AudienceAll sentinel "*". The caller is responsible for the admin gate and for
// validating target — Assign trusts its inputs. Idempotent; nil receiver / empty
// args are no-ops. Same snapshot-outside-lock + persist pattern as Claim.
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

// Rename re-keys the ownership entry from old to new, preserving the owner.
// Used when a session is renamed so ownership follows the session. No-op if old
// has no entry (nothing to carry over — a future Claim will assign new).
// Idempotent; nil receiver is a no-op. Same lock/persist pattern as Claim.
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

// VisibleTo reports whether the session with name should appear in
// user's session list. The rule: user is the registered owner, OR the
// session is unowned AND user is the primary account. Nil receiver
// degrades to "visible to primary only".
func (o *Ownership) VisibleTo(name, user string, primary bool) bool {
	if name == "" || user == "" {
		return false
	}
	owner := o.Owner(name)
	if owner == user {
		return true
	}
	if owner == AudienceAll { // "Todos" — visible to and attachable by anyone.
		return true
	}
	if owner == "" && primary {
		return true
	}
	return false
}

// SessionsOf returns the session names registered for a user. Used to enforce
// the per-user quota in pty.go:ownedSessionCount.
func (o *Ownership) SessionsOf(user string) []string {
	if user == "" {
		return nil
	}
	o.mu.RLock()
	defer o.mu.RUnlock()
	out := make([]string, 0, 4)
	for name, owner := range o.m {
		if owner == AudienceAll { // "Todos" does not count against anyone's quota.
			continue
		}
		if owner == user {
			out = append(out, name)
		}
	}
	return out
}

// persist writes a snapshot to disk atomically (write-temp + rename).
// It serialises through saveMu (separate from the in-memory mu) — in-memory
// readers/writers do not block during the disk I/O.
//
// Versioned ordering: every snapshot carries the `seq` of the mutation that
// produced it. Under saveMu, if a NEWER snapshot has already been persisted
// (o.saved >= seq), we discard this write — otherwise two concurrent persists
// could write out of order and an old snapshot would overwrite the new one on
// disk (→ a session "losing" its owner after a restart). saveMu only serialised
// the writes, it did not order them.
func (o *Ownership) persist(snapshot map[string]string, seq uint64) error {
	o.saveMu.Lock()
	defer o.saveMu.Unlock()
	if seq <= o.saved {
		return nil // a newer state is already on disk
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
