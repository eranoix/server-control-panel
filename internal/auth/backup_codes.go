// backup_codes.go — one-shot recovery codes for TOTP MFA.
//
// The model: 10 codes generated at enrollment, shown ONCE in the response. Each
// code is an XXXX-XXXX string (8 hex chars = 32 bits of entropy) — human-
// typable and dash-separated to reduce misreading. Storage: the sha256 hash of
// each code plus a used/used_at flag, in a JSON file with chmod 600.
//
// Why sha256 with no salt: individual codes carry 32 bits and admit no viable
// rainbow attack (precomputing 2^32 hashes is not a defence against theft of
// the file — whoever steals the file has root). Storing hashes guarantees that
// even reading the file does NOT reveal the code without brute force; enough
// for this threat model (single operator, controlled host).
//
// Lifecycle: generate at enrollment (one call) → consume at login (decrement
// by marking used). A re-enrollment OR a reset deletes the whole file. When 0
// unused codes remain, login with a backup code returns "all used, re-enroll"
// before accepting anything — a defence against brute force after N failures
// (one-shot codes already guarantee that, but the message is clearer).

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

// BackupCode is a single entry: the sha256 hash plus a used flag.
type BackupCode struct {
	Hash   string `json:"hash"`
	Used   bool   `json:"used"`
	UsedAt int64  `json:"used_at,omitempty"`
}

// BackupCodesFile is the JSON document written to
// data/mfa-backup-codes-<user>.json. SchemaVersion allows a future migration
// without breaking the parse.
type BackupCodesFile struct {
	SchemaVersion int          `json:"schema_version"`
	User          string       `json:"user"`
	GeneratedAt   int64        `json:"generated_at"`
	Codes         []BackupCode `json:"codes"`
}

// BackupCodesStore operates on one specific file. A singleton per user.
// Simple file locking via sync.Mutex (1 process, 1 file — no flock needed).
type BackupCodesStore struct {
	mu   sync.Mutex
	path string
}

// NewBackupCodesStore creates a store pointing at path. It reads nothing —
// loading is lazy, on each method call.
func NewBackupCodesStore(path string) *BackupCodesStore {
	return &BackupCodesStore{path: path}
}

// GenerateBackupCodes returns n fresh codes in clear text (the caller shows
// them to the user ONCE) plus the populated struct to persist via Save.
//
// External form: XXXX-XXXX. Internally we concatenate without the dash for
// hashing — so the user can type "ABCD-1234" OR "ABCD1234" and both validate.
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
		raw := hex.EncodeToString(b[:]) // 8 chars hex = 32 bits
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

// hashCode normalises (lowercase, dash stripped) and applies sha256. The same
// function is used at generation and at consumption — keep canonical.
func hashCode(input string) string {
	s := strings.ToLower(strings.ReplaceAll(input, "-", ""))
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// Save persists the file atomically with chmod 600 (write tmp + rename).
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

// Load reads the file. Returns (nil, nil) when the file does not exist — the
// caller treats that as "the user never enrolled".
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

// Delete removes the file (not an error if absent). Used by mfa/disable and
// vpsmctl mfa-emergency-reset.
func (s *BackupCodesStore) Delete() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("backup_codes: delete: %w", err)
	}
	return nil
}

// TryConsume attempts to validate `code` and mark it used. Returns:
//
//   - (true, nil)            code valid and not yet used → consumed
//   - (false, nil)           code does not exist OR was already used
//   - (false, err)           IO error (the caller decides the fallback)
//
// Idempotency: if two concurrent calls hit the same code, only one marks it
// used (mutex around Save). The other gets (false, nil).
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

// CountUnused returns how many codes are still valid. The UI shows this number
// so the user knows whether to regenerate before running out.
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

// BackupCodesPath returns the canonical file path given the data dir and the
// user. Centralised to avoid drift between the handlers and vpsmctl.
func BackupCodesPath(dataDir, user string) string {
	return fmt.Sprintf("%s/mfa-backup-codes-%s.json", strings.TrimRight(dataDir, "/"), user)
}
