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

type Store struct {
	mu          sync.RWMutex
	path        string
	passphrase  []byte
	key         []byte
	salt        []byte
	data        map[string]string
	lastMod     time.Time
	lastSize    int64
	needsResalt bool
}

type fileFormat struct {
	Salt       string `json:"salt,omitempty"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

func deriveKey(passphrase, salt []byte) ([]byte, error) {
	return scrypt.Key(passphrase, salt, 32768, 8, 1, 32)
}

func legacySalt(passphrase []byte) []byte {
	sum := sha256.Sum256(passphrase)
	out := make([]byte, 16)
	copy(out, sum[:16])
	return out
}

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

func (s *Store) Get(key string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.data[key]
	return v, ok
}

func (s *Store) Set(key, value string) error {
	s.mu.Lock()
	s.data[key] = value
	s.mu.Unlock()
	return s.save()
}

func (s *Store) Delete(key string) error {
	s.mu.Lock()
	delete(s.data, key)
	s.mu.Unlock()
	return s.save()
}

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

func (s *Store) Export() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]string, len(s.data))
	for k, v := range s.data {
		out[k] = v
	}
	return out
}

func (s *Store) Rotate(newPassphrase string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

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
	if err := verifyDecrypt(tmp, newKey); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rotate: post-write decrypt verify failed: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	s.key = newKey
	s.salt = newSalt
	s.passphrase = newPP
	s.needsResalt = false
	return nil
}

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

func (s *Store) save() error {
	s.mu.Lock()
	defer s.mu.Unlock()

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
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return err
	}
	s.stamp()
	return nil
}

func (s *Store) stamp() {
	if fi, err := os.Stat(s.path); err == nil {
		s.lastMod, s.lastSize = fi.ModTime(), fi.Size()
	}
}

func (s *Store) ReloadIfChanged() (bool, error) {
	fi, err := os.Stat(s.path)
	if err != nil {
		return false, err
	}
	s.mu.RLock()
	unchanged := fi.ModTime().Equal(s.lastMod) && fi.Size() == s.lastSize
	pp := string(s.passphrase)
	s.mu.RUnlock()
	if unchanged {
		return false, nil
	}
	fresh, err := Open(s.path, pp)
	if err != nil {
		return false, err
	}
	s.mu.Lock()
	s.data, s.salt, s.key, s.needsResalt = fresh.data, fresh.salt, fresh.key, fresh.needsResalt
	s.lastMod, s.lastSize = fresh.lastMod, fresh.lastSize
	s.mu.Unlock()
	return true, nil
}

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

var ErrNotFound = errors.New("not found")
