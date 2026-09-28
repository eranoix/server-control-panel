package auth

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
)

const (
	CredentialStatusPending  = "pending"
	CredentialStatusApproved = "approved"
)

type CredentialRecord struct {
	ID         string              `json:"id"`
	Label      string              `json:"label,omitempty"`
	Status     string              `json:"status"`
	CreatedAt  time.Time           `json:"created_at"`
	ApprovedAt *time.Time          `json:"approved_at,omitempty"`
	Credential webauthn.Credential `json:"credential"`
}

type webAuthnCredentialsFile struct {
	SchemaVersion int                `json:"schema_version"`
	Credentials   []CredentialRecord `json:"credentials"`
}

type WebAuthnCredentialsStore struct {
	mu   sync.Mutex
	path string
}

func WebAuthnCredentialsPath(dataDir, user string) string {
	return filepath.Join(dataDir, "webauthn_credentials", user+".json")
}

func NewWebAuthnCredentialsStore(path string) *WebAuthnCredentialsStore {
	return &WebAuthnCredentialsStore{path: path}
}

func CredentialRecordID(cred webauthn.Credential) string {
	return base64.RawURLEncoding.EncodeToString(cred.ID)
}

func (s *WebAuthnCredentialsStore) load() (*webAuthnCredentialsFile, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return &webAuthnCredentialsFile{SchemaVersion: 1}, nil
		}
		return nil, err
	}
	var f webAuthnCredentialsFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	if f.SchemaVersion == 0 {
		f.SchemaVersion = 1
	}
	return &f, nil
}

func (s *WebAuthnCredentialsStore) save(f *webAuthnCredentialsFile) error {
	if len(f.Credentials) == 0 {
		if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *WebAuthnCredentialsStore) Add(cred webauthn.Credential, label string) (CredentialRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return CredentialRecord{}, err
	}
	id := CredentialRecordID(cred)
	for _, r := range f.Credentials {
		if r.ID == id {
			return CredentialRecord{}, errors.New("credential already registered")
		}
	}
	rec := CredentialRecord{
		ID:         id,
		Label:      label,
		Status:     CredentialStatusPending,
		CreatedAt:  time.Now().UTC(),
		Credential: cred,
	}
	f.Credentials = append(f.Credentials, rec)
	if err := s.save(f); err != nil {
		return CredentialRecord{}, err
	}
	return rec, nil
}

func (s *WebAuthnCredentialsStore) List() ([]CredentialRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return nil, err
	}
	out := make([]CredentialRecord, 0, len(f.Credentials))
	for _, r := range f.Credentials {
		if r.Status == CredentialStatusApproved {
			out = append(out, r)
		}
	}
	return out, nil
}

func (s *WebAuthnCredentialsStore) ListAll() ([]CredentialRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return nil, err
	}
	out := make([]CredentialRecord, len(f.Credentials))
	copy(out, f.Credentials)
	return out, nil
}

func (s *WebAuthnCredentialsStore) CredentialByID(id string) (CredentialRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return CredentialRecord{}, false, err
	}
	for _, r := range f.Credentials {
		if r.ID == id {
			return r, true, nil
		}
	}
	return CredentialRecord{}, false, nil
}

func (s *WebAuthnCredentialsStore) Approve(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return err
	}
	idx := -1
	for i, r := range f.Credentials {
		if r.ID == id {
			idx = i
			break
		}
	}
	if idx == -1 {
		return errors.New("credential not found")
	}
	now := time.Now().UTC()
	f.Credentials[idx].Status = CredentialStatusApproved
	f.Credentials[idx].ApprovedAt = &now
	return s.save(f)
}

func (s *WebAuthnCredentialsStore) Remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return err
	}
	out := make([]CredentialRecord, 0, len(f.Credentials))
	removed := false
	for _, r := range f.Credentials {
		if r.ID == id {
			removed = true
			continue
		}
		out = append(out, r)
	}
	if !removed {
		return errors.New("credential not found")
	}
	f.Credentials = out
	return s.save(f)
}

func (s *WebAuthnCredentialsStore) UpdateCredential(cred webauthn.Credential) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return err
	}
	id := CredentialRecordID(cred)
	idx := -1
	for i, r := range f.Credentials {
		if r.ID == id {
			idx = i
			break
		}
	}
	if idx == -1 {
		return errors.New("credential not found")
	}
	f.Credentials[idx].Credential = cred
	return s.save(f)
}
