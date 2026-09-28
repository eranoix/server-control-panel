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

type Session struct {
	JTI       string `json:"jti"`
	User      string `json:"user"`
	IP        string `json:"ip"`
	UserAgent string `json:"ua"`
	IssuedAt  int64  `json:"issued_at"`
	LastSeen  int64  `json:"last_seen"`
	ExpiresAt int64  `json:"expires_at"`
	Revoked   bool   `json:"revoked,omitempty"`
}

type Store struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	path     string
	dirty    bool
	stop     chan struct{}
}

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
		return nil
	}
	now := time.Now().Unix()
	for _, sess := range arr {
		if sess.ExpiresAt > 0 && sess.ExpiresAt < now {
			continue
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

func (s *Store) HasTombstone(jti string) bool {
	if jti == "" {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	sess, ok := s.sessions[jti]
	return ok && sess.Revoked
}

func (s *Store) Has(jti string) bool {
	if jti == "" {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.sessions[jti]
	return ok
}

func (s *Store) Revoke(jti string) string {
	if jti == "" {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[jti]
	if !ok {
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
