// mobile_sessions.go — MobileRefreshStore: per-user, persisted, rotating,
// revocable mobile refresh-token records. Modeled directly on
// TrustedDevicesStore (trusted_devices.go): file-per-user JSON, sha256 hash
// at rest, atomic writes, one mutex per store, gc-expired-on-touch.
//
// Why rotation (not a static long-lived refresh token): every successful
// Rotate mints a BRAND NEW token and immediately invalidates the old one —
// single active token per device record. A stolen-then-used-by-an-attacker
// refresh token silently breaks the legitimate device's NEXT refresh attempt
// (old token no longer matches anything), which is the detectable signal a
// rotating scheme buys over a static bearer token.
//
// Token shape: "<username>.<64 hex chars>" (256 bits of entropy in the
// suffix). The username prefix is NOT secret — a device that already holds
// a valid refresh token already knows who it's logged in as, so putting the
// username in cleartext leaks nothing new. It exists so that the public,
// session-less POST /api/mobile/v1/auth/refresh endpoint (no cookie, no
// Authorization header) can resolve WHICH
// per-user file to open (via ParseMobileRefreshUsername) before it can even
// attempt to verify the opaque suffix's hash — the same reason a bcrypt
// login form needs a username before it can check a password. Only the hash
// of the FULL token (prefix + suffix) is ever persisted.
//
// TTL: 30 days, SLIDING — every successful Rotate extends ExpiresAt another
// 30 days from "now" (a documented product default:
// "pick a documented default, easily changed" — see MobileRefreshTTL). A
// phone used at least once a month never needs a fresh password+MFA login; a
// genuinely abandoned/lost device's session dies naturally within 30 days of
// its last use without depending on the user to actively revoke it. Still
// fully revocable per-record (Revoke) or per-user (RevokeAll) for the
// device-management UI on top of this store.

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

// MobileRefreshTTL is the sliding validity window of a mobile refresh
// token — see file docstring for the reasoning behind 30 days.
const MobileRefreshTTL = 30 * 24 * time.Hour

// MobileSession is one device's refresh-token record. Hash is sha256 of the
// full "<username>.<suffix>" token — never the token itself.
type MobileSession struct {
	ID          string `json:"id"`
	Hash        string `json:"hash"`
	DeviceLabel string `json:"device_label,omitempty"`
	CreatedAt   int64  `json:"created_at"`
	ExpiresAt   int64  `json:"expires_at"`
	LastSeen    int64  `json:"last_seen"`
}

// mobileSessionsFile is the document persisted at
// data/mobile-sessions-<user>.json. SchemaVersion allows future migration,
// mirroring TrustedDevicesFile.
type mobileSessionsFile struct {
	SchemaVersion int             `json:"schema_version"`
	User          string          `json:"user"`
	Sessions      []MobileSession `json:"sessions"`
}

// MobileRefreshStore operates on one user's file (singleton per user, same
// shape as TrustedDevicesStore).
type MobileRefreshStore struct {
	mu   sync.Mutex
	path string
}

// NewMobileRefreshStore creates a store pointed at path. No lazy validation —
// load happens per-method, mirroring NewTrustedDevicesStore.
func NewMobileRefreshStore(path string) *MobileRefreshStore {
	return &MobileRefreshStore{path: path}
}

// MobileRefreshStorePath returns the canonical per-user file path, mirroring
// TrustedDevicesPath — centralized so handlers and any future CLI agree.
func MobileRefreshStorePath(dataDir, user string) string {
	return fmt.Sprintf("%s/mobile-sessions-%s.json", strings.TrimRight(dataDir, "/"), user)
}

// ParseMobileRefreshUsername extracts the username prefix of a raw refresh
// token WITHOUT validating anything else — the caller still MUST call
// Rotate/Revoke on that user's store to confirm the opaque suffix's hash
// matches an unexpired, live record. An empty or malformed token (no ".",
// empty prefix, or empty suffix) always returns ok=false.
func ParseMobileRefreshUsername(token string) (username string, ok bool) {
	i := strings.IndexByte(token, '.')
	if i <= 0 || i == len(token)-1 {
		return "", false
	}
	return token[:i], true
}

// mintMobileRefreshToken builds a brand-new "<username>.<hex>" token with
// 256 bits of entropy in the suffix.
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

// load reads the file (no lock — caller already holds s.mu). (nil,nil) if
// absent.
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

// save persists atomically (chmod 0600, tmp+rename) — caller already holds
// s.mu. Removes the file entirely once it has no sessions left, same as
// TrustedDevicesStore.save.
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

// Mint creates a brand-new device record and returns the raw refresh token
// (caller sends it to the client once; only its hash is ever stored).
// deviceLabel is display-only metadata (e.g. trimmed User-Agent).
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

// Rotate validates oldToken (must match an unexpired record's hash) and, on
// success, atomically replaces that record's hash with a BRAND NEW token's
// hash, extends ExpiresAt by another MobileRefreshTTL from now (sliding),
// and returns the new token. oldToken stops working the instant this
// returns — a second Rotate call with the same oldToken always fails
// (ok=false), whether it was ever valid or not. Malformed tokens, tokens for
// a user with no store file, expired records, and already-rotated-away
// records are indistinguishable from the caller's perspective (ok=false) —
// it never "almost works" (mirrors the pairing-ticket replay guarantee).
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
			_ = s.save(file) // best-effort GC; a failure does not affect the verdict
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

// Revoke removes the record matching token's hash (if any). Idempotent —
// revoking twice, or revoking a token that never existed, is not an error.
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

// RevokeAll deletes every mobile session of this user (= removes the file).
// Used on password change, same posture as TrustedDevicesStore.RevokeAll.
func (s *MobileRefreshStore) RevokeAll() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("mobile_sessions: revoke all: %w", err)
	}
	return nil
}

// List returns the non-expired sessions of this store's user (for
// the device-management UI). Absent file -> empty slice.
func (s *MobileRefreshStore) List() ([]MobileSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	file, err := s.load()
	if err != nil || file == nil {
		return nil, err
	}
	return gcExpiredSessions(file.Sessions, time.Now().Unix()), nil
}
