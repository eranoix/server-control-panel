package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"sync"
	"time"

	"golang.org/x/crypto/scrypt"
)

// Store is an encrypted key-value vault backed by a JSON file on disk.
type Store struct {
	mu         sync.RWMutex
	path       string
	passphrase []byte // kept so we can re-derive the key after a salt rotation
	key        []byte
	salt       []byte // random per-vault (legacy vaults: sha256(passphrase)[:16])
	data       map[string]string
	// lastMod/lastSize stamp the file at the last load OR save. They serve
	// ReloadIfChanged: without them, detecting an external write would cost a key
	// derivation (scrypt N=32768) on every lookup.
	lastMod  time.Time
	lastSize int64
	// needsResalt is set when Open() found a legacy vault (no Salt field
	// in the file). Next save() will mint a fresh random salt + re-encrypt
	// with the new key, then clear the flag. Transparent migration —
	// callers don't need to know.
	needsResalt bool
}

type fileFormat struct {
	Salt       string `json:"salt,omitempty"` // hex; empty = legacy (sha256(passphrase)[:16])
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

// deriveKey runs scrypt with the canonical params. Centralised so
// Open / Save / Rotate all stay in sync.
func deriveKey(passphrase, salt []byte) ([]byte, error) {
	return scrypt.Key(passphrase, salt, 32768, 8, 1, 32)
}

// legacySalt is the deterministic salt used by vps-manager < 2026-06-09.
// We keep deriving it for back-compat read of existing vaults. New writes
// always use a random 16-byte salt persisted alongside the ciphertext.
func legacySalt(passphrase []byte) []byte {
	sum := sha256.Sum256(passphrase)
	out := make([]byte, 16)
	copy(out, sum[:16])
	return out
}

// Open loads (or creates) an encrypted store at path using the passphrase
// to derive an AES-256-GCM key via scrypt.
//
// Salt strategy: new vaults get a random 16-byte salt stored in the JSON.
// Legacy vaults (no `salt` field) are read with the deterministic salt,
// then transparently re-encrypted on the next save() with a random salt
// — defeating the per-passphrase rainbow table attack that was possible
// before. The migration is silent and idempotent.
func Open(path string, passphrase string) (*Store, error) {
	pp := []byte(passphrase)
	s := &Store{
		path:       path,
		passphrase: pp,
		data:       make(map[string]string),
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// fresh vault: mint salt now so the first save() writes it.
			s.salt = make([]byte, 16)
			if _, err := rand.Read(s.salt); err != nil {
				return nil, err
			}
			key, err := deriveKey(pp, s.salt)
			if err != nil {
				return nil, err
			}
			s.key = key
			s.stamp()
			return s, nil
		}
		return nil, err
	}
	if len(raw) == 0 {
		// rare: file exists but empty. Same as fresh.
		s.salt = make([]byte, 16)
		if _, err := rand.Read(s.salt); err != nil {
			return nil, err
		}
		key, err := deriveKey(pp, s.salt)
		if err != nil {
			return nil, err
		}
		s.key = key
		s.stamp()
		return s, nil
	}

	var ff fileFormat
	if err := json.Unmarshal(raw, &ff); err != nil {
		return nil, err
	}

	// Salt: persisted field wins; absence = legacy → schedule re-salt
	// at next save.
	if ff.Salt != "" {
		s.salt, err = hex.DecodeString(ff.Salt)
		if err != nil {
			return nil, err
		}
	} else {
		s.salt = legacySalt(pp)
		s.needsResalt = true
	}
	s.key, err = deriveKey(pp, s.salt)
	if err != nil {
		return nil, err
	}

	nonce, err := hex.DecodeString(ff.Nonce)
	if err != nil {
		return nil, err
	}
	ct, err := hex.DecodeString(ff.Ciphertext)
	if err != nil {
		return nil, err
	}

	block, err := aes.NewCipher(s.key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	pt, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(pt, &s.data); err != nil {
		return nil, err
	}
	s.stamp()
	return s, nil
}

// Get returns the value for a key and whether it was found.
func (s *Store) Get(key string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.data[key]
	return v, ok
}

// Set stores a value and persists the vault.
func (s *Store) Set(key, value string) error {
	s.mu.Lock()
	s.data[key] = value
	s.mu.Unlock()
	return s.save()
}

// Delete removes a key and persists the vault.
func (s *Store) Delete(key string) error {
	s.mu.Lock()
	delete(s.data, key)
	s.mu.Unlock()
	return s.save()
}

// List returns the sorted list of keys.
func (s *Store) List() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	keys := make([]string, 0, len(s.data))
	for k := range s.data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Export returns a copy of all values.
func (s *Store) Export() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]string, len(s.data))
	for k, v := range s.data {
		out[k] = v
	}
	return out
}

// Rotate re-encrypts the vault in-place with a NEW passphrase. Atomic:
// it writes to path+".rotating" and renames. If it fails halfway, the
// original path is left intact (the old key still works).
//
// IMPORTANT: the caller must discard this Store after Rotate and open a
// new one with Open(path, newPassphrase) — the internal derived key went stale.
// Returning the new Store would mean moving key derivation in here; the
// current design prefers the caller to Reload.
//
// Use case: the passphrase leaked in a paste/log; the admin runs
// `vpsmctl secrets rotate` (to be added in cmd/vpsmctl).
func (s *Store) Rotate(newPassphrase string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Rotation also mints a fresh random salt (not derived from the new
	// passphrase). Two passphrases that happened to collide on the legacy
	// sha256-truncated salt would have shared a derived key — random salt
	// kills that class of issue once and for all.
	newSalt := make([]byte, 16)
	if _, err := rand.Read(newSalt); err != nil {
		return err
	}
	newPP := []byte(newPassphrase)
	newKey, err := deriveKey(newPP, newSalt)
	if err != nil {
		return err
	}

	pt, err := json.Marshal(s.data)
	if err != nil {
		return err
	}
	block, err := aes.NewCipher(newKey)
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	ct := gcm.Seal(nil, nonce, pt, nil)

	ff := fileFormat{
		Salt:       hex.EncodeToString(newSalt),
		Nonce:      hex.EncodeToString(nonce),
		Ciphertext: hex.EncodeToString(ct),
	}
	raw, err := json.Marshal(ff)
	if err != nil {
		return err
	}
	tmp := s.path + ".rotating"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	// Verify the new file decrypts cleanly with the new key BEFORE we
	// swap it in. Without this verification step, a bug in the rotation
	// code (or a disk corruption mid-write) would leave the operator
	// locked out of their own vault — the old passphrase no longer
	// works, and the new file is unreadable.
	if err := verifyDecrypt(tmp, newKey); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rotate: post-write decrypt verify failed: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	// Promote key/salt/passphrase in memory to keep the next save() from
	// corrupting the vault.
	s.key = newKey
	s.salt = newSalt
	s.passphrase = newPP
	s.needsResalt = false
	return nil
}

// verifyDecrypt re-reads a vault file from disk and confirms gcm.Open
// succeeds with the supplied key. Used by Rotate as a fail-safe
// before swapping the new ciphertext into place.
func verifyDecrypt(path string, key []byte) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var ff fileFormat
	if err := json.Unmarshal(raw, &ff); err != nil {
		return err
	}
	nonce, err := hex.DecodeString(ff.Nonce)
	if err != nil {
		return err
	}
	ct, err := hex.DecodeString(ff.Ciphertext)
	if err != nil {
		return err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	if _, err := gcm.Open(nil, nonce, ct, nil); err != nil {
		return err
	}
	return nil
}

// save encrypts the current map and writes it to disk with 0600 perms.
// Atomic via tmp+rename so a crash mid-write leaves the previous vault
// intact. Also migrates legacy vaults to a random salt on first write.
func (s *Store) save() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// One-time migration: legacy vault loaded with deterministic salt
	// gets a fresh random salt + key derivation now. Once written, the
	// `salt` field in the file makes this idempotent.
	if s.needsResalt {
		newSalt := make([]byte, 16)
		if _, err := rand.Read(newSalt); err != nil {
			return err
		}
		newKey, err := deriveKey(s.passphrase, newSalt)
		if err != nil {
			return err
		}
		s.salt = newSalt
		s.key = newKey
		s.needsResalt = false
	}

	pt, err := json.Marshal(s.data)
	if err != nil {
		return err
	}

	block, err := aes.NewCipher(s.key)
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	ct := gcm.Seal(nil, nonce, pt, nil)

	ff := fileFormat{
		Salt:       hex.EncodeToString(s.salt),
		Nonce:      hex.EncodeToString(nonce),
		Ciphertext: hex.EncodeToString(ct),
	}
	raw, err := json.Marshal(ff)
	if err != nil {
		return err
	}
	// Atomic write: tmp file + rename. Prevents the read-before-rename
	// gap where a crash leaves a half-written vault.
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return err
	}
	// Re-stamp: without this, our OWN write would look like an external change and
	// the next ReloadIfChanged would pay a scrypt for nothing.
	s.stamp()
	return nil
}

// stamp records the file's mtime+size. Called with the lock already held (save)
// or on a freshly built Store (Open), where nobody else can see it.
func (s *Store) stamp() {
	if fi, err := os.Stat(s.path); err == nil {
		s.lastMod, s.lastSize = fi.ModTime(), fi.Size()
	}
}

// ReloadIfChanged re-reads the vault when the file changed outside this process.
//
// 🔴 Why it exists: Get() reads an IN-MEMORY map loaded exactly once at
// Open. Any secret written by another process — `vpsmctl secrets set`,
// `bin/pve-credencial --apply`, a restore script — stayed invisible to the
// panel until the next restart. Measured: the revocation drill
// recreated the node's token and the panel kept saying "revoked" indefinitely,
// with the key already back in the vault and on the hypervisor.
//
// The comparison is by mtime+size, and not by content, because re-reading costs a
// scrypt derivation (N=32768) — expensive on purpose. Callers can do it on
// every tick with no weight: in the common case it is one os.Stat.
//
// Returns true when a reload happened.
func (s *Store) ReloadIfChanged() (bool, error) {
	fi, err := os.Stat(s.path)
	if err != nil {
		return false, err
	}
	s.mu.RLock()
	igual := fi.ModTime().Equal(s.lastMod) && fi.Size() == s.lastSize
	pp := string(s.passphrase)
	s.mu.RUnlock()
	if igual {
		return false, nil
	}
	novo, err := Open(s.path, pp)
	if err != nil {
		return false, err
	}
	s.mu.Lock()
	s.data, s.salt, s.key, s.needsResalt = novo.data, novo.salt, novo.key, novo.needsResalt
	s.lastMod, s.lastSize = novo.lastMod, novo.lastSize
	s.mu.Unlock()
	return true, nil
}

// Handler returns an HTTP mux exposing list/get/set/delete endpoints.
func (s *Store) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/list", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"keys": s.List()})
	})

	mux.HandleFunc("/get", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		key := r.URL.Query().Get("key")
		v, ok := s.Get(key)
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"key": key, "value": v})
	})

	mux.HandleFunc("/set", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if body.Key == "" {
			http.Error(w, "key required", http.StatusBadRequest)
			return
		}
		if err := s.Set(body.Key, body.Value); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})

	mux.HandleFunc("/delete", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Key string `json:"key"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if body.Key == "" {
			http.Error(w, "key required", http.StatusBadRequest)
			return
		}
		if err := s.Delete(body.Key); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})

	return mux
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// ErrNotFound is returned when a key is missing. Kept for API clarity.
var ErrNotFound = errors.New("not found")
