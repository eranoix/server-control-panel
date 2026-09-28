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

const DeviceTrustTTL = 14 * 24 * time.Hour

type TrustedDevice struct {
	Hash      string `json:"hash"`
	Label     string `json:"label,omitempty"`
	IP        string `json:"ip,omitempty"`
	CreatedAt int64  `json:"created_at"`
	ExpiresAt int64  `json:"expires_at"`
	LastSeen  int64  `json:"last_seen"`
}

type TrustedDevicesFile struct {
	SchemaVersion int             `json:"schema_version"`
	User          string          `json:"user"`
	Devices       []TrustedDevice `json:"devices"`
}

type TrustedDevicesStore struct {
	mu   sync.Mutex
	path string
}

func NewTrustedDevicesStore(path string) *TrustedDevicesStore {
	return &TrustedDevicesStore{path: path}
}

func TrustedDevicesPath(dataDir, user string) string {
	return fmt.Sprintf("%s/trusted-devices-%s.json", strings.TrimRight(dataDir, "/"), user)
}

func hashSecret(secret string) string {
	h := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(h[:])
}

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

func gcExpired(devices []TrustedDevice, now int64) []TrustedDevice {
	kept := devices[:0]
	for _, d := range devices {
		if d.ExpiresAt > now {
			kept = append(kept, d)
		}
	}
	return kept
}

func (s *TrustedDevicesStore) Mint(user, label, ip string) (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("trusted_devices: entropy: %w", err)
	}
	secret := hex.EncodeToString(raw[:])

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
	before := len(file.Devices)
	file.Devices = gcExpired(file.Devices, now)
	if idx < 0 {
		if len(file.Devices) != before {
			_ = s.save(file)
		}
		return false, nil
	}
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

func (s *TrustedDevicesStore) RevokeAll() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("trusted_devices: revoke all: %w", err)
	}
	return nil
}

func (s *TrustedDevicesStore) List() ([]TrustedDevice, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	file, err := s.load()
	if err != nil || file == nil {
		return nil, err
	}
	return gcExpired(file.Devices, time.Now().Unix()), nil
}
