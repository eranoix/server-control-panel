// trusted_devices.go — "trusted devices" for skipping the 2nd factor (TOTP)
// on recognized logins, for a fixed window (14 days).
//
// Model: at login, if the user ticks "keep this device
// signed in" AFTER validating password + TOTP, the server issues a random
// OPAQUE SECRET (32 bytes → hex), stores only its sha256 server-side, and
// returns the secret in an HttpOnly cookie. On future logins from the same browser,
// if the cookie matches a non-expired hash, the 2nd factor is waived — the PASSWORD
// is still always required. This is 2FA trust, not an eternal session.
//
// Threat model: a stolen trust cookie = 2FA bypass for ≤14 days STILL
// requiring the password. Mitigated by HttpOnly+Secure+SameSite (on the cookie),
// hash at rest (here), binding to the user (one file per user), hard
// expiry (ExpiresAt) and revocability (Revoke/RevokeAll). The Google/GitHub pattern.
//
// Storage: mirrors backup_codes.go — one JSON file per user
// (trusted-devices-<user>.json), chmod 0600, atomic Save (tmp+rename),
// sync.Mutex (1 process, 1 file). It does NOT touch config.json (avoids cfgMu).
//
// Why sha256 WITHOUT normalization (≠ backup_codes' hashCode): the secret is
// random 256-bit hex, not a human-typable XXXX-XXXX code. There is
// no dash and no case to normalize; applying lowercase/strip would erode
// entropy for nothing. Hashing it directly keeps all 256 bits.

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

// DeviceTrustTTL is the window in which a trusted device waives the 2nd
// factor. 14 days.
const DeviceTrustTTL = 14 * 24 * time.Hour

// TrustedDevice is a single entry: the hash of the opaque secret plus metadata
// for the UI and auditing. The hash is sha256 of the (hex) secret, never the
// secret in the clear.
type TrustedDevice struct {
	Hash      string `json:"hash"`
	Label     string `json:"label,omitempty"` // User-Agent summary (display only)
	IP        string `json:"ip,omitempty"`
	CreatedAt int64  `json:"created_at"`
	ExpiresAt int64  `json:"expires_at"`
	LastSeen  int64  `json:"last_seen"`
}

// TrustedDevicesFile is the JSON document written to
// data/trusted-devices-<user>.json. SchemaVersion allows a future migration.
type TrustedDevicesFile struct {
	SchemaVersion int             `json:"schema_version"`
	User          string          `json:"user"`
	Devices       []TrustedDevice `json:"devices"`
}

// TrustedDevicesStore operates on one specific file (a singleton per user).
type TrustedDevicesStore struct {
	mu   sync.Mutex
	path string
}

// NewTrustedDevicesStore creates a store pointing at path. It reads nothing —
// loading is lazy, on each method call.
func NewTrustedDevicesStore(path string) *TrustedDevicesStore {
	return &TrustedDevicesStore{path: path}
}

// TrustedDevicesPath returns the canonical file path given the data dir and
// the user. Centralised to avoid drift between the handlers and vpsmctl.
func TrustedDevicesPath(dataDir, user string) string {
	return fmt.Sprintf("%s/trusted-devices-%s.json", strings.TrimRight(dataDir, "/"), user)
}

// hashSecret applies sha256 directly to the opaque (hex) secret. NO
// normalisation: the secret is already lowercase and dash-free (see the file
// doc). Mint and IsTrusted use this same function — keep canonical.
func hashSecret(secret string) string {
	h := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(h[:])
}

// load reads the file (no lock — the caller already holds s.mu). (nil,nil) when absent.
func (s *TrustedDevicesStore) load() (*TrustedDevicesFile, error) {
	b, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("trusted_devices: read: %w", err)
	}
	var f TrustedDevicesFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("trusted_devices: parse: %w", err)
	}
	return &f, nil
}

// save persists atomically (chmod 0600, tmp+rename) — the caller already holds s.mu.
// If the file would be left with no devices, it removes the file (no empty leftovers).
func (s *TrustedDevicesStore) save(file *TrustedDevicesFile) error {
	if len(file.Devices) == 0 {
		if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("trusted_devices: remove empty: %w", err)
		}
		return nil
	}
	b, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("trusted_devices: marshal: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0600); err != nil {
		return fmt.Errorf("trusted_devices: write tmp: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("trusted_devices: rename: %w", err)
	}
	return nil
}

// gc removes expired entries in place. Returns the pruned slice.
func gcExpired(devices []TrustedDevice, now int64) []TrustedDevice {
	kept := devices[:0]
	for _, d := range devices {
		if d.ExpiresAt > now {
			kept = append(kept, d)
		}
	}
	return kept
}

// Mint generates a fresh opaque secret, persists its hash with a 14d expiry, and
// returns the secret in the CLEAR (the caller puts it in the cookie). Expired
// entries are pruned on Save. label/ip are only for the UI and auditing. A
// missing file is created.
func (s *TrustedDevicesStore) Mint(user, label, ip string) (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("trusted_devices: entropy: %w", err)
	}
	secret := hex.EncodeToString(raw[:]) // 64 chars hex = 256 bits

	s.mu.Lock()
	defer s.mu.Unlock()
	file, err := s.load()
	if err != nil {
		return "", err
	}
	if file == nil {
		file = &TrustedDevicesFile{SchemaVersion: 1, User: user}
	}
	now := time.Now().Unix()
	file.Devices = gcExpired(file.Devices, now)
	file.Devices = append(file.Devices, TrustedDevice{
		Hash:      hashSecret(secret),
		Label:     label,
		IP:        ip,
		CreatedAt: now,
		ExpiresAt: now + int64(DeviceTrustTTL.Seconds()),
		LastSeen:  now,
	})
	if err := s.save(file); err != nil {
		return "", err
	}
	return secret, nil
}

// IsTrusted reports whether the secret matches a non-expired device. Updates LastSeen.
//
//   - secret == ""          → (false, nil) WITHOUT reading the file (common path)
//   - file absent           → (false, nil)
//   - hash mismatch/expired → (false, nil)
//   - match and valid       → (true, nil)
//   - IO error              → (false, err)  caller treats it as untrusted
func (s *TrustedDevicesStore) IsTrusted(secret string) (bool, error) {
	if secret == "" {
		return false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	file, err := s.load()
	if err != nil {
		return false, err
	}
	if file == nil {
		return false, nil
	}
	now := time.Now().Unix()
	target := hashSecret(secret)
	idx := -1
	for i := range file.Devices {
		if file.Devices[i].Hash == target && file.Devices[i].ExpiresAt > now {
			idx = i
			break
		}
	}
	// Prune expired entries in passing (keeps the file lean with no dedicated job).
	before := len(file.Devices)
	file.Devices = gcExpired(file.Devices, now)
	if idx < 0 {
		if len(file.Devices) != before {
			_ = s.save(file) // best-effort GC; a failure does not affect the verdict
		}
		return false, nil
	}
	// idx was computed before the gc; recompute the target after the pruning.
	for i := range file.Devices {
		if file.Devices[i].Hash == target {
			file.Devices[i].LastSeen = now
			break
		}
	}
	if err := s.save(file); err != nil {
		return false, err
	}
	return true, nil
}

// Revoke removes the entry matching the secret (if there is one). Idempotent.
func (s *TrustedDevicesStore) Revoke(secret string) error {
	if secret == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	file, err := s.load()
	if err != nil || file == nil {
		return err
	}
	target := hashSecret(secret)
	kept := file.Devices[:0]
	for _, d := range file.Devices {
		if d.Hash != target {
			kept = append(kept, d)
		}
	}
	file.Devices = kept
	return s.save(file)
}

// RevokeAll deletes every trusted device of the user (= removes the file).
// Used on a password change and on MFA disable. Not an error if absent.
func (s *TrustedDevicesStore) RevokeAll() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("trusted_devices: revoke all: %w", err)
	}
	return nil
}

// List returns the non-expired devices (for the UI / management). A missing file → [].
func (s *TrustedDevicesStore) List() ([]TrustedDevice, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	file, err := s.load()
	if err != nil || file == nil {
		return nil, err
	}
	return gcExpired(file.Devices, time.Now().Unix()), nil
}
