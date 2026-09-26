package api

// Server-side persistence of the frontend's "Terminal" state (the auto-snapshot
// of the panes + named workspaces). It used to live only in the browser's
// localStorage — lost when the cache was cleared or the device changed. Now it
// lives in `<DataDir>/users/<user>/terminal-state.json` and the frontend syncs
// it through the API. Same strategy as browser-instances.json (per-user, atomic
// write).
//
// File schema:
//
//	{
//	  "v": 1,
//	  "snapshots": {
//	    "claude":  { ... the frontend's snapshot (panes/layout/activePane/...) },
//	    "venice":  { ... }
//	  },
//	  "workspaces": [
//	    { "name":"foo", "savedAt":<ms>, "namespace":"claude",
//	      "panes":[...], "layout":{...}, "activePane":"..." },
//	    ...
//	  ]
//	}
//
// The server does NOT understand the snapshot's internal format (the `panes`,
// `layout` and other fields): it treats it as `json.RawMessage`. The frontend
// owns the schema — if it changes, bumping the snapshot's internal `v` is
// enough. That decouples the frontend and backend deploys.

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"server-control-panel/internal/auth"
)

const (
	termStateFile     = "terminal-state.json"
	termStateMaxFile  = 1 << 20   // 1 MiB total — caps growth on disk
	termBodyMaxBytes  = 256 << 10 // 256 KiB por request — snapshot real ~5KB
	termMaxWorkspaces = 100
)

// Namespaces the frontend knows about. Only "claude" is left (Venice/ChatGPT
// were dropped in the Claude-only consolidation).
var termNamespaces = map[string]struct{}{
	"claude": {},
}

// Workspace name: Unicode letter/digit, space, dot, hyphen, underscore.
// Limit 64 chars. Matches what the frontend accepts (a prompt with no regex).
var termWorkspaceNameRe = regexp.MustCompile(`^[\p{L}\p{N}_\- .]{1,64}$`)

type terminalState struct {
	V          int                        `json:"v"`
	Snapshots  map[string]json.RawMessage `json:"snapshots"`
	Workspaces []terminalWorkspace        `json:"workspaces"`
	// Tombstones: workspaces deleted deliberately. Without them, an offline
	// device with a stale cache "resurrects" a workspace the user has already
	// deleted on another device — on merge it would decide "the remote does not
	// have it, the local does; push to the remote". Global (server-side)
	// tombstones prevent that.
	// GC: entries older than 30 days are cleaned on every save (keeps it from inflating).
	DeletedWorkspaces []terminalTombstone `json:"deletedWorkspaces,omitempty"`
}

type terminalWorkspace struct {
	Name       string          `json:"name"`
	SavedAt    int64           `json:"savedAt"`
	Namespace  string          `json:"namespace"`
	Panes      json.RawMessage `json:"panes,omitempty"`
	Layout     json.RawMessage `json:"layout,omitempty"`
	ActivePane json.RawMessage `json:"activePane,omitempty"`
}

type terminalTombstone struct {
	Name      string `json:"name"`
	DeletedAt int64  `json:"deletedAt"`
}

// termTombstoneTTL is a tombstone's lifetime. After that the GC removes it on
// the next save — the assumption being that every device has consumed the delete.
const termTombstoneTTL = 30 * 24 * 60 * 60 * 1000 // 30 dias em ms

// gcTombstones removes expired tombstones (now - TTL). Idempotent.
func gcTombstones(st *terminalState, nowMs int64) {
	if len(st.DeletedWorkspaces) == 0 {
		return
	}
	cutoff := nowMs - termTombstoneTTL
	kept := st.DeletedWorkspaces[:0]
	for _, t := range st.DeletedWorkspaces {
		if t.DeletedAt >= cutoff {
			kept = append(kept, t)
		}
	}
	st.DeletedWorkspaces = kept
}

// tombstoneFor returns the tombstone (and its index) for a workspace name, or
// -1 when there is none. A linear scan is fine — fewer than 100 entries expected.
func tombstoneFor(st *terminalState, name string) (int, terminalTombstone) {
	for i, t := range st.DeletedWorkspaces {
		if t.Name == name {
			return i, t
		}
	}
	return -1, terminalTombstone{}
}

// A mutex per user: reads and writes on the same file have to be serialised
// (a sendBeacon on pagehide can arrive concurrently with a debounced push).
// sync.Map avoids a global map+lock.
var termStateMu sync.Map // string -> *sync.Mutex

func termLockFor(user string) *sync.Mutex {
	if v, ok := termStateMu.Load(user); ok {
		return v.(*sync.Mutex)
	}
	nm := &sync.Mutex{}
	actual, _ := termStateMu.LoadOrStore(user, nm)
	return actual.(*sync.Mutex)
}

func (r *Router) termStatePath(req *http.Request) (string, string) {
	user := auth.UserFrom(req)
	// Defence in depth: sanitise at the path boundary (the same pattern as
	// sessionlog.go). The user comes from the JWT and account creation already
	// restricts the charset, but we do not rely on that here — it blocks
	// traversal from any identity source (Supabase federation, for instance).
	if user == "" || strings.Contains(user, "..") || strings.ContainsAny(user, "/\\") {
		return "", ""
	}
	return user, filepath.Join(r.cfg.DataDir, "users", user, termStateFile)
}

func loadTerminalState(path string) (*terminalState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return emptyTerminalState(), nil
		}
		return nil, err
	}
	if len(data) > termStateMaxFile {
		// Corrupted file, or inflated beyond what we expect: discard rather than
		// propagate junk back to the frontend. Safer than trying to parse a giant
		// blob (DoS-by-disk).
		return emptyTerminalState(), nil
	}
	var st terminalState
	if err := json.Unmarshal(data, &st); err != nil {
		// Same reasoning: invalid JSON on disk → start from scratch. Unlikely
		// (atomic write), but the paranoia is worth it.
		return emptyTerminalState(), nil
	}
	if st.Snapshots == nil {
		st.Snapshots = map[string]json.RawMessage{}
	}
	if st.Workspaces == nil {
		st.Workspaces = []terminalWorkspace{}
	}
	if st.DeletedWorkspaces == nil {
		st.DeletedWorkspaces = []terminalTombstone{}
	}
	return &st, nil
}

func emptyTerminalState() *terminalState {
	return &terminalState{
		V:                 1,
		Snapshots:         map[string]json.RawMessage{},
		Workspaces:        []terminalWorkspace{},
		DeletedWorkspaces: []terminalTombstone{},
	}
}

func saveTerminalState(path string, st *terminalState) error {
	st.V = 1
	// GC tombstones on every save: amortises the cost across common operations.
	gcTombstones(st, time.Now().UnixMilli())
	return termAtomicWriteJSON(path, st, 0o600)
}

// termAtomicWriteJSON mirrors videocall.atomicWriteJSON without creating a
// cross dependency: write to tmp → fsync → rename → fsync of the parent
// directory. It guarantees that a crash midway does not leave the main file
// corrupted.
func termAtomicWriteJSON(path string, v any, mode os.FileMode) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".new"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	_ = f.Close()
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if df, err := os.Open(filepath.Dir(path)); err == nil {
		_ = df.Sync()
		_ = df.Close()
	}
	return nil
}

// validateTermState applies the limits and schema rules before writing. It runs
// both on the full PUT and after targeted mutations (defence in depth).
func validateTermState(st *terminalState) error {
	if st == nil {
		return errors.New("nil state")
	}
	if st.Snapshots == nil {
		st.Snapshots = map[string]json.RawMessage{}
	}
	if st.Workspaces == nil {
		st.Workspaces = []terminalWorkspace{}
	}
	// Silently drop snapshots of obsolete namespaces (a legacy "chatgpt", say,
	// once the feature was removed). Without this, a single on-disk state JSON
	// with an old key breaks every subsequent PUT — it breaks the Claude/Venice
	// sync, because the load reads the whole blob and validate rejects it.
	// This approach treats removing a namespace as graceful deprecation:
	// the server drops it on the next write, without ever returning 400.
	for k, raw := range st.Snapshots {
		if _, ok := termNamespaces[k]; !ok {
			delete(st.Snapshots, k)
			continue
		}
		if len(raw) > termBodyMaxBytes {
			return errors.New("snapshot too large: " + k)
		}
	}
	if len(st.Workspaces) > termMaxWorkspaces {
		return errors.New("too many workspaces")
	}
	// Same strategy for the workspaces — a removed namespace becomes a drop, not a 400.
	kept := st.Workspaces[:0]
	for _, ws := range st.Workspaces {
		if !termWorkspaceNameRe.MatchString(ws.Name) {
			return errors.New("invalid workspace name")
		}
		if _, ok := termNamespaces[ws.Namespace]; !ok && ws.Namespace != "" {
			continue
		}
		kept = append(kept, ws)
	}
	st.Workspaces = kept
	return nil
}

func readRawJSONBody(rc io.ReadCloser) (json.RawMessage, error) {
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, errors.New("empty body")
	}
	if !json.Valid(data) {
		return nil, errors.New("invalid json")
	}
	return json.RawMessage(data), nil
}

func decodeTermWorkspaceName(raw string) (string, error) {
	if raw == "" {
		return "", errors.New("missing workspace name")
	}
	name, err := url.PathUnescape(raw)
	if err != nil {
		return "", errors.New("invalid encoding")
	}
	name = strings.TrimSpace(name)
	if !termWorkspaceNameRe.MatchString(name) {
		return "", errors.New("invalid name")
	}
	return name, nil
}

// handleTerminalState: GET/PUT of the whole blob.
//
//	GET  /api/terminal/state         → 200 { v, snapshots, workspaces }
//	PUT  /api/terminal/state  body=^ → 200 { ok:true }
//
// PUT is used when the frontend wants to reconcile the entire state at once
// (the first login on a new device, for instance).
func (r *Router) handleTerminalState(w http.ResponseWriter, req *http.Request) {
	user, path := r.termStatePath(req)
	if user == "" {
		writeErr(w, 401, "unauthorized")
		return
	}
	mu := termLockFor(user)
	switch req.Method {
	case http.MethodGet:
		mu.Lock()
		defer mu.Unlock()
		st, err := loadTerminalState(path)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		writeJSON(w, st)
	case http.MethodPut:
		req.Body = http.MaxBytesReader(w, req.Body, termBodyMaxBytes*4)
		var incoming terminalState
		if err := json.NewDecoder(req.Body).Decode(&incoming); err != nil {
			writeErr(w, 400, "bad json: "+err.Error())
			return
		}
		if err := validateTermState(&incoming); err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if err := saveTerminalState(path, &incoming); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	default:
		writeErr(w, 405, "method not allowed")
	}
}

// handleTerminalSnapshot: PUT/DELETE of a namespace's auto-snapshot.
//
//	PUT    /api/terminal/snapshot/claude   body=<raw snapshot>
//	DELETE /api/terminal/snapshot/claude
func (r *Router) handleTerminalSnapshot(w http.ResponseWriter, req *http.Request) {
	user, path := r.termStatePath(req)
	if user == "" {
		writeErr(w, 401, "unauthorized")
		return
	}
	ns := strings.TrimPrefix(req.URL.Path, "/api/terminal/snapshot/")
	ns = strings.TrimSuffix(ns, "/")
	if _, ok := termNamespaces[ns]; !ok {
		writeErr(w, 400, "invalid namespace")
		return
	}
	mu := termLockFor(user)
	mu.Lock()
	defer mu.Unlock()
	st, err := loadTerminalState(path)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	switch req.Method {
	case http.MethodPut:
		req.Body = http.MaxBytesReader(w, req.Body, termBodyMaxBytes)
		raw, err := readRawJSONBody(req.Body)
		if err != nil {
			writeErr(w, 400, "bad body: "+err.Error())
			return
		}
		st.Snapshots[ns] = raw
	case http.MethodDelete:
		delete(st.Snapshots, ns)
	default:
		writeErr(w, 405, "method not allowed")
		return
	}
	if err := validateTermState(st); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if err := saveTerminalState(path, st); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// handleTerminalWorkspace: PUT/DELETE of a named workspace.
//
//	PUT    /api/terminal/workspace/<name>  body={ name, savedAt, namespace, panes, layout, activePane }
//	DELETE /api/terminal/workspace/<name>
//
// The `name` in the path is the source of truth — if the body carries another
// name, the path wins. That avoids accidental renames through a
// frontend/backend mismatch.
func (r *Router) handleTerminalWorkspace(w http.ResponseWriter, req *http.Request) {
	user, path := r.termStatePath(req)
	if user == "" {
		writeErr(w, 401, "unauthorized")
		return
	}
	rawName := strings.TrimPrefix(req.URL.Path, "/api/terminal/workspace/")
	rawName = strings.TrimSuffix(rawName, "/")
	name, err := decodeTermWorkspaceName(rawName)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	mu := termLockFor(user)
	mu.Lock()
	defer mu.Unlock()
	st, err := loadTerminalState(path)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	switch req.Method {
	case http.MethodPut:
		req.Body = http.MaxBytesReader(w, req.Body, termBodyMaxBytes)
		var ws terminalWorkspace
		if err := json.NewDecoder(req.Body).Decode(&ws); err != nil {
			writeErr(w, 400, "bad json: "+err.Error())
			return
		}
		ws.Name = name
		if _, ok := termNamespaces[ws.Namespace]; !ok {
			ws.Namespace = "claude"
		}
		// Tombstone check: reject the PUT if the workspace was deleted MORE
		// RECENTLY than the version being sent. Covers the "an offline device
		// tries to resurrect a workspace another device already deleted" case. If
		// the PUT is genuinely newer (savedAt > deletedAt), it is promoted: the
		// tombstone is removed and the PUT accepted (the user recreated the
		// workspace on purpose).
		//
		// It uses the RAW savedAt (0 when the client omitted it): the
		// default-to-now used to be applied BEFORE this check, so a device that
		// does not send savedAt always "won" (now > DeletedAt) and resurrected a
		// deleted workspace. A missing savedAt = as old as possible = loses to any
		// tombstone; the client can recreate on purpose by sending an explicit savedAt.
		if idx, t := tombstoneFor(st, name); idx >= 0 {
			if ws.SavedAt <= t.DeletedAt {
				writeErr(w, 409, "workspace deleted")
				return
			}
			st.DeletedWorkspaces = append(st.DeletedWorkspaces[:idx], st.DeletedWorkspaces[idx+1:]...)
		}
		// Only after clearing the tombstone do we stamp a real savedAt to persist.
		if ws.SavedAt == 0 {
			ws.SavedAt = time.Now().UnixMilli()
		}
		replaced := false
		for i := range st.Workspaces {
			if st.Workspaces[i].Name == name {
				st.Workspaces[i] = ws
				replaced = true
				break
			}
		}
		if !replaced {
			if len(st.Workspaces) >= termMaxWorkspaces {
				writeErr(w, 400, "too many workspaces")
				return
			}
			st.Workspaces = append(st.Workspaces, ws)
		}
	case http.MethodDelete:
		filtered := st.Workspaces[:0]
		for _, ws := range st.Workspaces {
			if ws.Name != name {
				filtered = append(filtered, ws)
			}
		}
		st.Workspaces = filtered
		// Create/update the tombstone. Idempotent: a re-delete only updates deletedAt.
		now := time.Now().UnixMilli()
		if idx, _ := tombstoneFor(st, name); idx >= 0 {
			st.DeletedWorkspaces[idx].DeletedAt = now
		} else {
			st.DeletedWorkspaces = append(st.DeletedWorkspaces, terminalTombstone{
				Name: name, DeletedAt: now,
			})
		}
	default:
		writeErr(w, 405, "method not allowed")
		return
	}
	if err := validateTermState(st); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if err := saveTerminalState(path, st); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}
