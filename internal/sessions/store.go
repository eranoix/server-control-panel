// Package sessions tracks active JWT sessions so the user can list and revoke
// them individually (e.g. "log me out of that other browser"). Sessions are
// keyed by the JWT's `jti` claim and persisted to disk under data/sessions.json
// so a service restart doesn't kick everyone out.
package sessions

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Session is a single live token grant.
type Session struct {
	JTI       string `json:"jti"`               // JWT ID — primary key
	User      string `json:"user"`              // subject
	IP        string `json:"ip"`                // first-seen IP
	UserAgent string `json:"ua"`                // first-seen UA (truncated)
	IssuedAt  int64  `json:"issued_at"`         // unix seconds
	LastSeen  int64  `json:"last_seen"`         // unix seconds — refreshed on every authenticated request
	ExpiresAt int64  `json:"expires_at"`        // unix seconds — matches JWT exp
	Revoked   bool   `json:"revoked,omitempty"` // tombstone: kept around so the JTI can't be auto-re-added by migration
}

// Store is a concurrent map of active sessions with disk persistence.
type Store struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	path     string // data/sessions.json
	dirty    bool   // set on every mutation; cleared by background flusher
	stop     chan struct{}
}

// Open loads sessions.json (or starts empty if missing) and spawns a flusher
// that persists changes every 5 seconds — keeps writes off the hot path.
func Open(path string) (*Store, error) {
	s := &Store{
		sessions: make(map[string]*Session),
		path:     path,
		stop:     make(chan struct{}),
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	go s.flusher()
	return s, nil
}

// Close sinaliza o flusher pra parar e faz flush final.
func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	select {
	case <-s.stop:
	default:
		close(s.stop)
	}
	return s.save()
}

func (s *Store) load() error {
	b, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	var arr []*Session
	if err := json.Unmarshal(b, &arr); err != nil {
		// Tolerate a corrupt file: start fresh rather than refuse to boot.
		// The penalty is "everyone has to log in again", not "service down".
		return nil
	}
	now := time.Now().Unix()
	for _, sess := range arr {
		if sess.ExpiresAt > 0 && sess.ExpiresAt < now {
			continue // forget expired sessions
		}
		s.sessions[sess.JTI] = sess
	}
	return nil
}

func (s *Store) flusher() {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-t.C:
			s.mu.Lock()
			needSave := s.dirty
			s.dirty = false
			s.mu.Unlock()
			if needSave {
				_ = s.save()
			}
		}
	}
}

// save writes atomically: .new + rename + fsync(dir). Same pattern as config.
func (s *Store) save() error {
	s.mu.RLock()
	arr := make([]*Session, 0, len(s.sessions))
	for _, sess := range s.sessions {
		arr = append(arr, sess)
	}
	s.mu.RUnlock()
	b, err := json.MarshalIndent(arr, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".new"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	_ = f.Close()
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if df, err := os.Open(filepath.Dir(s.path)); err == nil {
		_ = df.Sync()
		_ = df.Close()
	}
	return nil
}

// Add registers a brand-new session. If the JTI already exists (e.g. as a
// revoked tombstone) the existing entry is preserved — we never undo a revoke.
func (s *Store) Add(sess Session) {
	if sess.JTI == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.sessions[sess.JTI]; exists {
		return
	}
	s.sessions[sess.JTI] = &sess
	s.dirty = true
}

// Touch marks the session as recently active. Returns true only if the
// session exists and is NOT revoked. Returning false has two meanings:
//   - JTI unknown — caller may auto-add (migration) via Add() and try again.
//   - JTI known but revoked — caller must reject the request (401).
//
// Use HasTombstone to disambiguate.
func (s *Store) Touch(jti string) bool {
	if jti == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[jti]
	if !ok || sess.Revoked {
		return false
	}
	sess.LastSeen = time.Now().Unix()
	s.dirty = true
	return true
}

// HasTombstone reports whether the JTI was explicitly revoked. Used by the
// middleware to refuse migration of a token that was killed on purpose.
func (s *Store) HasTombstone(jti string) bool {
	if jti == "" {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	sess, ok := s.sessions[jti]
	return ok && sess.Revoked
}

// Has reports whether a session with that jti exists (live OR tombstone).
func (s *Store) Has(jti string) bool {
	if jti == "" {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.sessions[jti]
	return ok
}

// Revoke marks a session as revoked (tombstone). Returns the user it
// belonged to, or "" if not found.
func (s *Store) Revoke(jti string) string {
	if jti == "" {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[jti]
	if !ok {
		// Create a tombstone anyway so the JTI can't be auto-added later
		// (defends against revoke-before-migration races).
		s.sessions[jti] = &Session{JTI: jti, Revoked: true, LastSeen: time.Now().Unix()}
		s.dirty = true
		return ""
	}
	if sess.Revoked {
		return sess.User
	}
	sess.Revoked = true
	s.dirty = true
	return sess.User
}

// RevokeAllExcept tombstones every LIVE session for `user` except the one
// whose jti matches `keep`. Returns the number revoked.
func (s *Store) RevokeAllExcept(user, keep string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for jti, sess := range s.sessions {
		if sess.User == user && jti != keep && !sess.Revoked {
			sess.Revoked = true
			n++
		}
	}
	if n > 0 {
		s.dirty = true
	}
	return n
}

// ListForUser returns the user's LIVE sessions (no tombstones), newest first.
func (s *Store) ListForUser(user string) []Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Session, 0)
	for _, sess := range s.sessions {
		if sess.User == user && !sess.Revoked {
			out = append(out, *sess)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].IssuedAt > out[j].IssuedAt })
	return out
}

// PruneExpired removes sessions whose JWT has expired — INCLUDING tombstones,
// because once the token can no longer pass JWT signature validation by exp,
// keeping the tombstone is pointless. Called periodically.
func (s *Store) PruneExpired() int {
	now := time.Now().Unix()
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for jti, sess := range s.sessions {
		if sess.ExpiresAt > 0 && sess.ExpiresAt < now {
			delete(s.sessions, jti)
			n++
		}
	}
	if n > 0 {
		s.dirty = true
	}
	return n
}
