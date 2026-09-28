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

type BackupCode struct {
	Hash   string `json:"hash"`
	Used   bool   `json:"used"`
	UsedAt int64  `json:"used_at,omitempty"`
}

type BackupCodesFile struct {
	SchemaVersion int          `json:"schema_version"`
	User          string       `json:"user"`
	GeneratedAt   int64        `json:"generated_at"`
	Codes         []BackupCode `json:"codes"`
}

type BackupCodesStore struct {
	mu   sync.Mutex
	path string
}

func NewBackupCodesStore(path string) *BackupCodesStore {
	return &BackupCodesStore{path: path}
}

func GenerateBackupCodes(user string, n int) ([]string, *BackupCodesFile, error) {
	if n <= 0 {
		n = 10
	}
	codes := make([]string, 0, n)
	entries := make([]BackupCode, 0, n)
	for i := 0; i < n; i++ {
		var b [4]byte
		if _, err := rand.Read(b[:]); err != nil {
			return nil, nil, fmt.Errorf("backup_codes: entropy: %w", err)
		}
		raw := hex.EncodeToString(b[:])
		readable := strings.ToUpper(raw[:4] + "-" + raw[4:])
		codes = append(codes, readable)
		entries = append(entries, BackupCode{Hash: hashCode(raw), Used: false})
	}
	file := &BackupCodesFile{
		SchemaVersion: 1,
		User:          user,
		GeneratedAt:   time.Now().Unix(),
		Codes:         entries,
	}
	return codes, file, nil
}

func hashCode(input string) string {
	s := strings.ToLower(strings.ReplaceAll(input, "-", ""))
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func (s *BackupCodesStore) Save(file *BackupCodesFile) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("backup_codes: marshal: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0600); err != nil {
		return fmt.Errorf("backup_codes: write tmp: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("backup_codes: rename: %w", err)
	}
	return nil
}

func (s *BackupCodesStore) Load() (*BackupCodesFile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("backup_codes: read: %w", err)
	}
	var f BackupCodesFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("backup_codes: parse: %w", err)
	}
	return &f, nil
}

func (s *BackupCodesStore) Delete() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("backup_codes: delete: %w", err)
	}
	return nil
}

func (s *BackupCodesStore) TryConsume(code string) (bool, error) {
	file, err := s.Load()
	if err != nil {
		return false, err
	}
	if file == nil {
		return false, nil
	}
	target := hashCode(code)
	idx := -1
	for i := range file.Codes {
		if file.Codes[i].Hash == target && !file.Codes[i].Used {
			idx = i
			break
		}
	}
	if idx < 0 {
		return false, nil
	}
	file.Codes[idx].Used = true
	file.Codes[idx].UsedAt = time.Now().Unix()
	if err := s.Save(file); err != nil {
		return false, err
	}
	return true, nil
}

func (s *BackupCodesStore) CountUnused() (int, error) {
	file, err := s.Load()
	if err != nil {
		return 0, err
	}
	if file == nil {
		return 0, nil
	}
	count := 0
	for _, c := range file.Codes {
		if !c.Used {
			count++
		}
	}
	return count, nil
}

func BackupCodesPath(dataDir, user string) string {
	return fmt.Sprintf("%s/mfa-backup-codes-%s.json", strings.TrimRight(dataDir, "/"), user)
}
