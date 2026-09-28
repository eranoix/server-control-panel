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

type UUIDMap struct {
	mu   sync.RWMutex
	data map[string]uuidMapEntry
}

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

func (u *UUIDMap) Size() int {
	if u == nil {
		return 0
	}
	u.mu.RLock()
	defer u.mu.RUnlock()
	return len(u.data)
}

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
