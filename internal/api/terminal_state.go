package api

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
	termStateMaxFile  = 1 << 20
	termBodyMaxBytes  = 256 << 10
	termMaxWorkspaces = 100
)

var termNamespaces = map[string]struct{}{
	"claude": {},
}

var termWorkspaceNameRe = regexp.MustCompile(`^[\p{L}\p{N}_\- .]{1,64}$`)

type terminalState struct {
	V                 int                        `json:"v"`
	Snapshots         map[string]json.RawMessage `json:"snapshots"`
	Workspaces        []terminalWorkspace        `json:"workspaces"`
	DeletedWorkspaces []terminalTombstone        `json:"deletedWorkspaces,omitempty"`
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

const termTombstoneTTL = 30 * 24 * 60 * 60 * 1000

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

func tombstoneFor(st *terminalState, name string) (int, terminalTombstone) {
	for i, t := range st.DeletedWorkspaces {
		if t.Name == name {
			return i, t
		}
	}
	return -1, terminalTombstone{}
}

var termStateMu sync.Map

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
		return emptyTerminalState(), nil
	}
	var st terminalState
	if err := json.Unmarshal(data, &st); err != nil {
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
	gcTombstones(st, time.Now().UnixMilli())
	return termAtomicWriteJSON(path, st, 0o600)
}

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
		if idx, t := tombstoneFor(st, name); idx >= 0 {
			if ws.SavedAt <= t.DeletedAt {
				writeErr(w, 409, "workspace deleted")
				return
			}
			st.DeletedWorkspaces = append(st.DeletedWorkspaces[:idx], st.DeletedWorkspaces[idx+1:]...)
		}
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
