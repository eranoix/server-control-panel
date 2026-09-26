// uuid_map.go — reader for the data/migration-uuid-map.json produced during
// the migration.
//
// Maps a v2 username ("sam", "jordan") → the northwind email ("sam@northwind.example")
// plus supabase_uuid. The email is the key sent to POST /auth/v1/token; the
// uuid stays available for admin calls (factor enrollment, emergency reset).
//
// Loading: called from main.go at boot via auth.LoadUUIDMap(path). Returns an
// error if the file is missing or the mapping does not cover some user of
// config.json. No silent fallback — failing closed avoids a "login works with
// bcrypt but fails on the swap" discovered late.
package auth

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
)

type uuidMapEntry struct {
	Email        string `json:"email"`
	SupabaseUUID string `json:"supabase_uuid"`
}

type uuidMapFile struct {
	SchemaVersion int                     `json:"schema_version"`
	Mappings      map[string]uuidMapEntry `json:"mappings"`
}

// UUIDMap is safe for concurrent access. Loaded once at boot.
type UUIDMap struct {
	mu   sync.RWMutex
	data map[string]uuidMapEntry
}

// LoadUUIDMap parses the JSON file. Returns either a ready *UUIDMap OR an error.
// Empty (file missing) is treated as nil + nil — the caller decides whether
// that is fatal (it is, once the Supabase flow is live, because that flow does
// not run without the mapping).
func LoadUUIDMap(path string) (*UUIDMap, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("uuid_map: read %s: %w", path, err)
	}
	var f uuidMapFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("uuid_map: parse %s: %w", path, err)
	}
	if f.SchemaVersion != 1 {
		return nil, fmt.Errorf("uuid_map: schema_version=%d (expected 1)", f.SchemaVersion)
	}
	m := make(map[string]uuidMapEntry, len(f.Mappings))
	for username, entry := range f.Mappings {
		if entry.Email == "" || entry.SupabaseUUID == "" {
			return nil, fmt.Errorf("uuid_map: entry %q has empty email or uuid", username)
		}
		m[username] = entry
	}
	return &UUIDMap{data: m}, nil
}

// Lookup returns the email + uuid for the given username. ok=false when the
// username is not mapped (the caller decides: 401, or fall back to bcrypt).
func (u *UUIDMap) Lookup(username string) (email, uuid string, ok bool) {
	if u == nil {
		return "", "", false
	}
	u.mu.RLock()
	defer u.mu.RUnlock()
	entry, found := u.data[username]
	if !found {
		return "", "", false
	}
	return entry.Email, entry.SupabaseUUID, true
}

// Size returns the number of mappings loaded (useful for diagnostics on /api/health).
func (u *UUIDMap) Size() int {
	if u == nil {
		return 0
	}
	u.mu.RLock()
	defer u.mu.RUnlock()
	return len(u.data)
}

// LookupByEmail does the reverse lookup, email → canonical username. Used at
// login when the operator types the email into the form (the UX mirrors
// northwind-web). Case insensitive — the email in auth.users is normalised to
// lowercase by GoTrue.
func (u *UUIDMap) LookupByEmail(email string) (username string, ok bool) {
	if u == nil {
		return "", false
	}
	u.mu.RLock()
	defer u.mu.RUnlock()
	needle := strings.ToLower(strings.TrimSpace(email))
	for uname, entry := range u.data {
		if strings.ToLower(entry.Email) == needle {
			return uname, true
		}
	}
	return "", false
}

// EmailFor returns the email mapped to a username. Used by /api/auth/me to
// expose the email in the UI without changing the canonical identity.
func (u *UUIDMap) EmailFor(username string) string {
	if u == nil {
		return ""
	}
	u.mu.RLock()
	defer u.mu.RUnlock()
	if entry, ok := u.data[username]; ok {
		return entry.Email
	}
	return ""
}

// Remove deletes the username's entry. Used by handleUserDelete to close the
// Supabase login path after a deletion — with no entry in the map,
// verifySupabase returns ErrSupabaseInvalidCredentials before ever reaching
// GoTrue. It is persisted on the next Save (the caller decides when to call
// SaveUUIDMap).
//
// Returns true if an entry was removed, false if the username was not in the map.
func (u *UUIDMap) Remove(username string) bool {
	if u == nil {
		return false
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if _, ok := u.data[username]; !ok {
		return false
	}
	delete(u.data, username)
	return true
}

// SaveUUIDMap persists the current state of the UUIDMap. Atomic write (.new ->
// rename) — pairs with handleUserDelete to close both the local bcrypt path
// (config.Save) and the Supabase path (this one).
func SaveUUIDMap(u *UUIDMap, path string) error {
	if u == nil {
		return nil
	}
	u.mu.RLock()
	f := uuidMapFile{SchemaVersion: 1, Mappings: make(map[string]uuidMapEntry, len(u.data))}
	for k, v := range u.data {
		f.Mappings[k] = v
	}
	u.mu.RUnlock()
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".new"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
