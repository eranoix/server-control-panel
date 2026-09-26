package scope

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"

	"server-control-panel/internal/secrets"
)

// globalKeys is the closed set of vault keys that are NOT namespaced per
// user. Currently only secrets the daemon as a whole needs (JWT signing
// material, TLS material in the future). Anything not in this set gets
// "<user>:" prepended when written via UserVault.
//
// Edit with care: a key that is meant to be per-user but accidentally lives
// in this set leaks across tenants. A key that is meant to be global but
// gets per-user'd here just duplicates innocently.
var globalKeys = map[string]struct{}{
	"JWT_SECRET": {},
	// AdGuard Home is a daemon-wide service (a DNS filter for the whole
	// instance, not per user), like the JWT. Its admin credentials are
	// read by the Segurança → AdGuard panel via a plain r.secrets.Get(...).
	"adguard_user":     {},
	"adguard_password": {},
	// Secret of sing-box's Clash API (a daemon-wide tunnel, one per instance),
	// read plain by the Segurança → Dispositivos panel.
	"singbox_clash_secret": {},
}

// IsGlobalKey reports whether key bypasses per-user namespacing.
func IsGlobalKey(key string) bool {
	_, ok := globalKeys[key]
	return ok
}

// metaLogicalKey is the reserved logical key under which a user's per-entry
// metadata blob lives. On disk it becomes "<user>:__meta__" and its value is
// a JSON map[logicalKey]EntryMeta (a string, like every other vault value).
//
// Design rationale: keeping metadata as a reserved entry inside the
// existing map[string]string means the on-disk envelope and secrets.Store stay
// byte-for-byte unchanged. A legacy binary still json.Unmarshal's the plaintext
// into map[string]string successfully — it just sees one extra string key it
// ignores. No format version, no envelope change, no cross-binary brick.
const metaLogicalKey = "__meta__"

// defaultMaxValueBytes caps a stored secret value when the handler is not
// given an explicit limit. 64 KiB comfortably holds a PEM bundle or token
// while refusing accidental file uploads.
const defaultMaxValueBytes = 64 << 10

// EntryMeta is the optional metadata attached to a single secret. Every field
// is optional; a secret written via plain Set (e.g. the daemon's waha_* keys
// or the v1→v2 migration) simply has no EntryMeta and reads back as zero.
type EntryMeta struct {
	Group     string `json:"group,omitempty"`
	Type      string `json:"type,omitempty"`
	Notes     string `json:"notes,omitempty"`
	CreatedAt int64  `json:"created_at,omitempty"` // unix seconds, stamped once
	UpdatedAt int64  `json:"updated_at,omitempty"` // unix seconds, every write
}

// Entry is a secret's logical key plus its metadata, as surfaced to the UI by
// ListEntries. The value is never included — reveal is an explicit, audited op.
type Entry struct {
	Key       string `json:"key"`
	Group     string `json:"group,omitempty"`
	Type      string `json:"type,omitempty"`
	Notes     string `json:"notes,omitempty"`
	CreatedAt int64  `json:"created_at,omitempty"`
	UpdatedAt int64  `json:"updated_at,omitempty"`
}

// GroupView buckets entries by group for the grouped list payload.
type GroupView struct {
	Name    string  `json:"name"`
	Entries []Entry `json:"entries"`
}

// UserVault wraps a *secrets.Store and transparently namespaces every key
// it sees with "<user>:". Callers ask for "waha_api_key" and get the value
// at "<user>:waha_api_key" on disk. Global keys (see globalKeys) pass
// through unchanged.
//
// One *UserVault per Scope; constructed via Factory.UserVault.
type UserVault struct {
	user  User
	store *secrets.Store
}

// NewUserVault binds store to user. The caller guarantees user is valid.
func NewUserVault(store *secrets.Store, user User) *UserVault {
	return &UserVault{user: user, store: store}
}

// key returns the on-disk key for a logical key. Global keys pass through;
// everything else is prefixed with "<user>:". Already-prefixed keys (i.e.
// "foo:bar" where foo is some other user) are detected and rejected by the
// public methods, not here — this is the dumb mapper.
func (v *UserVault) key(logical string) string {
	if IsGlobalKey(logical) {
		return logical
	}
	return v.user.String() + ":" + logical
}

// Get returns the value of the per-user key. Global keys are also reachable.
func (v *UserVault) Get(key string) (string, bool) {
	return v.store.Get(v.key(key))
}

// Set writes value under the per-user key. It does NOT touch metadata —
// callers wanting group/type/notes use SetWithMeta. Plain Set is preserved
// for the migration path and the daemon's own keys.
func (v *UserVault) Set(key, value string) error {
	return v.store.Set(v.key(key), value)
}

// SetWithMeta writes value and updates the entry's metadata in one logical
// operation. CreatedAt is stamped on first write and preserved afterwards;
// UpdatedAt is refreshed every time.
func (v *UserVault) SetWithMeta(key, value string, m EntryMeta) error {
	if err := v.store.Set(v.key(key), value); err != nil {
		return err
	}
	meta := v.loadMeta()
	now := time.Now().Unix()
	if prev, ok := meta[key]; ok && prev.CreatedAt != 0 {
		m.CreatedAt = prev.CreatedAt
	} else if m.CreatedAt == 0 {
		m.CreatedAt = now
	}
	m.UpdatedAt = now
	meta[key] = m
	return v.saveMeta(meta)
}

// SetMeta updates ONLY the metadata for a key, without touching the stored
// value. Used to (re)group/tag credentials already in the vault — e.g. the
// daemon's own waha_*/jira_* keys that were created via plain Set and have no
// group. CreatedAt is preserved on existing entries; UpdatedAt always bumps.
func (v *UserVault) SetMeta(key string, m EntryMeta) error {
	meta := v.loadMeta()
	now := time.Now().Unix()
	if prev, ok := meta[key]; ok && prev.CreatedAt != 0 {
		m.CreatedAt = prev.CreatedAt
	} else if m.CreatedAt == 0 {
		m.CreatedAt = now
	}
	m.UpdatedAt = now
	meta[key] = m
	return v.saveMeta(meta)
}

// Delete removes the per-user key AND its metadata entry, so no orphan meta
// survives. Critical for the WhatsApp teardown which iterates List()+Delete
// to wipe every "<user>:waha_*" key.
func (v *UserVault) Delete(key string) error {
	if err := v.store.Delete(v.key(key)); err != nil {
		return err
	}
	meta := v.loadMeta()
	if _, ok := meta[key]; ok {
		delete(meta, key)
		return v.saveMeta(meta)
	}
	return nil
}

// List returns the logical keys belonging to this user (with the "<user>:"
// prefix stripped). Global keys are NOT included — the UI for /api/secrets
// should not expose JWT_SECRET to anyone. The reserved __meta__ entry is
// filtered out too: it is plumbing, not a user secret.
func (v *UserVault) List() []string {
	prefix := v.user.String() + ":"
	keys := v.store.List()
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		logical := strings.TrimPrefix(k, prefix)
		if logical == metaLogicalKey {
			continue // reserved metadata blob, never a user-facing secret
		}
		out = append(out, logical)
	}
	return out
}

// ListEntries returns each user secret joined with its metadata, sorted by
// logical key. Secrets without metadata come back with zero-value fields.
func (v *UserVault) ListEntries() []Entry {
	keys := v.List()
	meta := v.loadMeta()
	out := make([]Entry, 0, len(keys))
	for _, k := range keys {
		m := meta[k]
		out = append(out, Entry{
			Key:       k,
			Group:     m.Group,
			Type:      m.Type,
			Notes:     m.Notes,
			CreatedAt: m.CreatedAt,
			UpdatedAt: m.UpdatedAt,
		})
	}
	return out
}

// loadMeta reads and parses the reserved metadata blob. A missing or
// unparseable blob yields an empty map — metadata is best-effort decoration
// and must never block access to the real secrets.
func (v *UserVault) loadMeta() map[string]EntryMeta {
	m := map[string]EntryMeta{}
	blob, ok := v.store.Get(v.key(metaLogicalKey))
	if !ok || blob == "" {
		return m
	}
	_ = json.Unmarshal([]byte(blob), &m)
	return m
}

// saveMeta persists the metadata blob. When the map is empty it removes the
// reserved key entirely instead of writing "{}", so a fully-emptied vault
// leaves no plumbing entry behind.
func (v *UserVault) saveMeta(m map[string]EntryMeta) error {
	if len(m) == 0 {
		if _, ok := v.store.Get(v.key(metaLogicalKey)); ok {
			return v.store.Delete(v.key(metaLogicalKey))
		}
		return nil
	}
	blob, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return v.store.Set(v.key(metaLogicalKey), string(blob))
}

// targetFor builds an audit target string "<group>/<key>" (or just "<key>"
// when the entry has no group), looking the group up from metadata. Never
// includes the secret value.
func (v *UserVault) targetFor(key string) string {
	if m, ok := v.loadMeta()[key]; ok && m.Group != "" {
		return m.Group + "/" + key
	}
	return key
}

// Store returns the underlying store. Reserved for the migration path and
// the rare caller that legitimately needs a global key. Handlers should not
// reach for it.
func (v *UserVault) Store() *secrets.Store { return v.store }

// HandlerOpts configures the per-user secrets HTTP handler.
type HandlerOpts struct {
	// Audit, when non-nil, is invoked AFTER a request succeeds (HTTP 200).
	// action is "secrets.set" | "secrets.delete" | "secrets.reveal"; target
	// is "<group>/<key>" (or "<key>"). The secret value is NEVER passed.
	Audit func(action, target string)
	// AllowSystemGroup gates the reserved "system" group name. Pass
	// r.isPrimary(user) so only the primary account can file secrets under
	// the shared "Sistema" group.
	AllowSystemGroup bool
	// MaxValueBytes caps an accepted value's size. <= 0 uses defaultMaxValueBytes.
	MaxValueBytes int
}

// Handler returns an HTTP mux mirroring secrets.Store.Handler(), but every
// request operates on the per-user namespace and carries group/type/
// notes metadata plus audit emission. Mount on the same /api/secrets path the
// global store used to occupy.
//
// Backward compatible: /set still accepts the bare {key,value} body (group/
// type/notes are optional), and /list still returns "keys" alongside the new
// "groups". Path traversal via "key=other:foo" stays blocked — colons and the
// reserved __meta__ key are rejected by validLogicalKey.
func (v *UserVault) Handler(opts HandlerOpts) http.Handler {
	mux := http.NewServeMux()

	maxValue := opts.MaxValueBytes
	if maxValue <= 0 {
		maxValue = defaultMaxValueBytes
	}
	audit := func(action, target string) {
		if opts.Audit != nil {
			opts.Audit(action, target)
		}
	}

	mux.HandleFunc("/list", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		entries := v.ListEntries()
		writeJSON(w, http.StatusOK, map[string]any{
			"groups": groupEntries(entries),
			"keys":   v.List(), // retained for clients that haven't migrated
		})
	})

	mux.HandleFunc("/get", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		key := r.URL.Query().Get("key")
		if !validLogicalKey(key) {
			http.Error(w, "invalid key", http.StatusBadRequest)
			return
		}
		val, ok := v.Get(key)
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"key": key, "value": val})
		audit("secrets.reveal", v.targetFor(key))
	})

	mux.HandleFunc("/set", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Key   string `json:"key"`
			Value string `json:"value"`
			Group string `json:"group"`
			Type  string `json:"type"`
			Notes string `json:"notes"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if !validLogicalKey(body.Key) {
			http.Error(w, "invalid key", http.StatusBadRequest)
			return
		}
		if len(body.Value) > maxValue {
			http.Error(w, "value too large", http.StatusRequestEntityTooLarge)
			return
		}
		if body.Group != "" && !validGroupName(body.Group, opts.AllowSystemGroup) {
			http.Error(w, "invalid group", http.StatusBadRequest)
			return
		}
		meta := EntryMeta{Group: body.Group, Type: body.Type, Notes: body.Notes}
		if err := v.SetWithMeta(body.Key, body.Value, meta); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		target := body.Key
		if body.Group != "" {
			target = body.Group + "/" + body.Key
		}
		audit("secrets.set", target)
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
		if !validLogicalKey(body.Key) {
			http.Error(w, "invalid key", http.StatusBadRequest)
			return
		}
		target := v.targetFor(body.Key) // resolve group BEFORE the entry is gone
		if err := v.Delete(body.Key); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		audit("secrets.delete", target)
	})

	return mux
}

// groupEntries buckets entries by group. Entries with no group land in a
// bucket named "" (the front renders it as "Sem grupo"). Group names sort
// case-insensitively; the empty bucket is pushed last so real groups lead.
func groupEntries(entries []Entry) []GroupView {
	byGroup := map[string][]Entry{}
	for _, e := range entries {
		byGroup[e.Group] = append(byGroup[e.Group], e)
	}
	names := make([]string, 0, len(byGroup))
	for name := range byGroup {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		a, b := names[i], names[j]
		if (a == "") != (b == "") {
			return b == "" // empty group last
		}
		la, lb := strings.ToLower(a), strings.ToLower(b)
		if la != lb {
			return la < lb
		}
		return a < b
	})
	out := make([]GroupView, 0, len(names))
	for _, name := range names {
		out = append(out, GroupView{Name: name, Entries: byGroup[name]})
	}
	return out
}

// validLogicalKey blocks colons (would let a caller punch across the
// namespace prefix), the reserved __meta__ key, and empty strings. Anything
// else goes — vault values are application-defined, names should not be
// over-policed here.
func validLogicalKey(key string) bool {
	if key == "" || key == metaLogicalKey {
		return false
	}
	if strings.ContainsAny(key, ":\x00") {
		return false
	}
	return true
}

// validGroupName validates a group label. Empty is rejected by the caller
// before this is reached (an empty group means "ungrouped" and is allowed on
// the wire). Colons/slashes/NULs are blocked so a group can't smuggle a
// namespace separator or path component. The reserved "system" group (the
// shared "Sistema" bucket) is only accepted when isPrimary.
func validGroupName(name string, isPrimary bool) bool {
	if name == "" {
		return false
	}
	if strings.ContainsAny(name, ":/\x00") {
		return false
	}
	if strings.EqualFold(name, "system") && !isPrimary {
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
