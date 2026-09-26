package auth

// webauthn_credentials.go — persistent, per-user storage of
// WebAuthn credentials (passkeys). One JSON file per user, following
// exactly the pattern of trusted_devices.go (atomic tmp+rename, chmod 0600,
// SchemaVersion, one mutex per instance).
//
// The central security property of this file: a
// freshly registered credential is born in "pending" status and ONLY List()
// (used to authorize login and to drop duplicates on registration)
// hides it. It only enters the "real" list — the one WebAuthn considers to
// belong to the user for login purposes — after Approve(), called from
// an already authenticated desktop session. That is what makes
// QR-code pairing safe: photographing the QR and completing the registration does NOT
// grant access — it only creates an inert record waiting for approval.

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

// Possible statuses of a CredentialRecord.
const (
	CredentialStatusPending  = "pending"
	CredentialStatusApproved = "approved"
)

// CredentialRecord is a persisted WebAuthn credential, carrying the approval
// metadata that go-webauthn does not have (it only knows webauthn.Credential).
type CredentialRecord struct {
	ID         string              `json:"id"` // base64url (no padding) of Credential.ID — stable external identifier
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

// WebAuthnCredentialsStore is ONE user's credential file. It never opens or
// even sees another user's file — the separation between users is by file
// path, not by an in-memory filter, so no logic bug can leak a credential to
// the wrong user.
type WebAuthnCredentialsStore struct {
	mu   sync.Mutex
	path string
}

// WebAuthnCredentialsPath builds the per-user path, same scheme as
// TrustedDevicesPath.
func WebAuthnCredentialsPath(dataDir, user string) string {
	return filepath.Join(dataDir, "webauthn_credentials", user+".json")
}

// NewWebAuthnCredentialsStore opens (without loading yet — lazy) the
// credentials file at the given path.
func NewWebAuthnCredentialsStore(path string) *WebAuthnCredentialsStore {
	return &WebAuthnCredentialsStore{path: path}
}

// CredentialRecordID computes the stable external identifier (base64url
// without padding of Credential.ID) used as the key in Add/Approve/Remove/
// CredentialByID — the same conversion everywhere keeps writers and readers
// from diverging.
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

// Add persists a freshly registered credential in PENDING status — never
// approved by default. label is what the UI shows on approval
// (e.g. "Pixel 8 — Chrome"), and may be empty.
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

// List returns ONLY approved credentials — the set WebAuthn should treat as
// "the user's credentials" when excluding duplicates at registration. A
// pending credential never appears here.
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

// ListAll returns every credential, approved or pending — used by the pairing
// panel to show what is waiting for approval, and by the login flow itself so
// it can RECOGNISE a pending credential (and then refuse with 403
// pending_approval, instead of "unknown credential").
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

// CredentialByID looks a credential up by ID WITHIN this store — it never
// consults another file, so there is no code path in which one user's
// credential could be confused with another's.
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

// Approve marks credential `id` as approved. It rejects an unknown ID WITHOUT
// mutating the file (it never even reaches save).
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

// Remove deletes credential `id`, approved or pending — used both to reject a
// pending pairing and to revoke an approved passkey.
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

// UpdateCredential overwrites the webauthn.Credential blob of an existing
// record (same ID), preserving label/status/timestamps. Necessary because
// every successful authentication updates the authenticator's signature
// counter (SignCount) — the library returns the updated Credential and expects
// the Relying Party to persist it back (see the go-webauthn/webauthn package
// docs, "Storage" section); not doing so weakens authenticator clone
// detection.
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
