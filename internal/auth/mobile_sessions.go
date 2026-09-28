package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

const MobileRefreshTTL = 30 * 24 * time.Hour

type MobileSession struct {
	ID          string `json:"id"`
	Hash        string `json:"hash"`
	DeviceLabel string `json:"device_label,omitempty"`
	CreatedAt   int64  `json:"created_at"`
	ExpiresAt   int64  `json:"expires_at"`
	LastSeen    int64  `json:"last_seen"`
}

type mobileSessionsFile struct {
	SchemaVersion int             `json:"schema_version"`
	User          string          `json:"user"`
	Sessions      []MobileSession `json:"sessions"`
}

type MobileRefreshStore struct {
	mu   sync.Mutex
	path string
}

func NewMobileRefreshStore(path string) *MobileRefreshStore {
	return &MobileRefreshStore{path: path}
}

func MobileRefreshStorePath(dataDir, user string) string {
	return fmt.Sprintf("%s/mobile-sessions-%s.json", strings.TrimRight(dataDir, "/"), user)
}

func ParseMobileRefreshUsername(token string) (username string, ok bool) {
	i := strings.IndexByte(token, '.')
	if i <= 0 || i == len(token)-1 {
		return "", false
	}
	return token[:i], true
}

func mintMobileRefreshToken(username string) (token string, err error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("mobile_sessions: entropy: %w", err)
	}
	return username + "." + hex.EncodeToString(raw[:]), nil
}

func hashMobileToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

func (s *MobileRefreshStore) load() (*mobileSessionsFile, error) {
	b, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("mobile_sessions: read: %w", err)
	}
	var f mobileSessionsFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("mobile_sessions: parse: %w", err)
	}
	return &f, nil
}

func (s *MobileRefreshStore) save(file *mobileSessionsFile) error {
	if len(file.Sessions) == 0 {
		if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("mobile_sessions: remove empty: %w", err)
		}
		return nil
	}
	b, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("mobile_sessions: marshal: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0600); err != nil {
		return fmt.Errorf("mobile_sessions: write tmp: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("mobile_sessions: rename: %w", err)
	}
	return nil
}

func gcExpiredSessions(sessions []MobileSession, now int64) []MobileSession {
	kept := sessions[:0]
	for _, sess := range sessions {
		if sess.ExpiresAt > now {
			kept = append(kept, sess)
		}
	}
	return kept
}

func (s *MobileRefreshStore) Mint(username, deviceLabel string) (refreshToken string, err error) {
	token, err := mintMobileRefreshToken(username)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	file, err := s.load()
	if err != nil {
		return "", err
	}
	if file == nil {
		file = &mobileSessionsFile{SchemaVersion: 1, User: username}
	}
	now := time.Now().Unix()
	file.Sessions = gcExpiredSessions(file.Sessions, now)
	var idb [8]byte
	_, _ = rand.Read(idb[:])
	file.Sessions = append(file.Sessions, MobileSession{
		ID:          hex.EncodeToString(idb[:]),
		Hash:        hashMobileToken(token),
		DeviceLabel: deviceLabel,
		CreatedAt:   now,
		ExpiresAt:   now + int64(MobileRefreshTTL.Seconds()),
		LastSeen:    now,
	})
	if err := s.save(file); err != nil {
		return "", err
	}
	return token, nil
}

func (s *MobileRefreshStore) Rotate(oldToken string) (newToken, username string, ok bool, err error) {
	username, parsed := ParseMobileRefreshUsername(oldToken)
	if !parsed {
		return "", "", false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	file, err := s.load()
	if err != nil {
		return "", "", false, err
	}
	if file == nil {
		return "", "", false, nil
	}
	now := time.Now().Unix()
	target := hashMobileToken(oldToken)
	found := false
	for i := range file.Sessions {
		if file.Sessions[i].Hash == target && file.Sessions[i].ExpiresAt > now {
			found = true
			break
		}
	}
	before := len(file.Sessions)
	file.Sessions = gcExpiredSessions(file.Sessions, now)
	if !found {
		if len(file.Sessions) != before {
			_ = s.save(file)
		}
		return "", "", false, nil
	}
	newTok, err := mintMobileRefreshToken(username)
	if err != nil {
		return "", "", false, err
	}
	for i := range file.Sessions {
		if file.Sessions[i].Hash == target {
			file.Sessions[i].Hash = hashMobileToken(newTok)
			file.Sessions[i].ExpiresAt = now + int64(MobileRefreshTTL.Seconds())
			file.Sessions[i].LastSeen = now
			break
		}
	}
	if err := s.save(file); err != nil {
		return "", "", false, err
	}
	return newTok, username, true, nil
}

func (s *MobileRefreshStore) Revoke(token string) error {
	if token == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	file, err := s.load()
	if err != nil || file == nil {
		return err
	}
	target := hashMobileToken(token)
	kept := file.Sessions[:0]
	for _, sess := range file.Sessions {
		if sess.Hash != target {
			kept = append(kept, sess)
		}
	}
	file.Sessions = kept
	return s.save(file)
}

func (s *MobileRefreshStore) RevokeAll() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("mobile_sessions: revoke all: %w", err)
	}
	return nil
}

func (s *MobileRefreshStore) List() ([]MobileSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	file, err := s.load()
	if err != nil || file == nil {
		return nil, err
	}
	return gcExpiredSessions(file.Sessions, time.Now().Unix()), nil
}
