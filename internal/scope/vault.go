package scope

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"

	"server-control-panel/internal/secrets"
)

var globalKeys = map[string]struct{}{
	"JWT_SECRET":           {},
	"adguard_user":         {},
	"adguard_password":     {},
	"singbox_clash_secret": {},
}

func IsGlobalKey(key string) bool {
	_, ok := globalKeys[key]
	return ok
}

const metaLogicalKey = "__meta__"

const defaultMaxValueBytes = 64 << 10

type EntryMeta struct {
	Group     string `json:"group,omitempty"`
	Type      string `json:"type,omitempty"`
	Notes     string `json:"notes,omitempty"`
	CreatedAt int64  `json:"created_at,omitempty"`
	UpdatedAt int64  `json:"updated_at,omitempty"`
}

type Entry struct {
	Key       string `json:"key"`
	Group     string `json:"group,omitempty"`
	Type      string `json:"type,omitempty"`
	Notes     string `json:"notes,omitempty"`
	CreatedAt int64  `json:"created_at,omitempty"`
	UpdatedAt int64  `json:"updated_at,omitempty"`
}

type GroupView struct {
	Name    string  `json:"name"`
	Entries []Entry `json:"entries"`
}

type UserVault struct {
	user  User
	store *secrets.Store
}

func NewUserVault(store *secrets.Store, user User) *UserVault {
	return &UserVault{user: user, store: store}
}

func (v *UserVault) key(logical string) string {
	if IsGlobalKey(logical) {
		return logical
	}
	return v.user.String() + ":" + logical
}

func (v *UserVault) Get(key string) (string, bool) {
	return v.store.Get(v.key(key))
}

func (v *UserVault) Set(key, value string) error {
	return v.store.Set(v.key(key), value)
}

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
			continue
		}
		out = append(out, logical)
	}
	return out
}

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

func (v *UserVault) loadMeta() map[string]EntryMeta {
	m := map[string]EntryMeta{}
	blob, ok := v.store.Get(v.key(metaLogicalKey))
	if !ok || blob == "" {
		return m
	}
	_ = json.Unmarshal([]byte(blob), &m)
	return m
}

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

func (v *UserVault) targetFor(key string) string {
	if m, ok := v.loadMeta()[key]; ok && m.Group != "" {
		return m.Group + "/" + key
	}
	return key
}

func (v *UserVault) Store() *secrets.Store { return v.store }

type HandlerOpts struct {
	Audit            func(action, target string)
	AllowSystemGroup bool
	MaxValueBytes    int
}

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
			"keys":   v.List(),
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
		target := v.targetFor(body.Key)
		if err := v.Delete(body.Key); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		audit("secrets.delete", target)
	})

	return mux
}

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
			return b == ""
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

func validLogicalKey(key string) bool {
	if key == "" || key == metaLogicalKey {
		return false
	}
	if strings.ContainsAny(key, ":\x00") {
		return false
	}
	return true
}

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
