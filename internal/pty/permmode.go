// permmode.go — per-session Claude Code permission mode (VPSM Wave-3 #53).
//
// A session may carry a Claude Code permission mode that is passed to `claude`
// at spawn via `--permission-mode <mode>`. The mode is stored in a tiny sidecar
// <DataDir>/session-permmode.json (dtach session name → mode) written by the
// cockpit picker (via `vpsmctl agent-permmode`) and READ by the spawners here.
//
// Anti-injection: the mode is allowlist-validated before it ever
// reaches an argv, and it is only ever passed as a discrete argv element (never
// shell-concatenated). An empty/unset/invalid mode means "no flag" — claude
// keeps whatever its own default is.
package pty

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

// errPermMode is returned by SetSessionPermMode for a non-empty mode outside the
// allowlist.
var errPermMode = errors.New("invalid permission mode (use plan|acceptEdits|default)")

// permModeAllow is the closed set of permission modes we will forward to
// `claude`. We deliberately EXCLUDE "bypassPermissions" (too dangerous to wire
// through a stored sidecar). "default" is claude's own manual/ask mode and is a
// harmless no-op when forwarded explicitly.
var permModeAllow = map[string]bool{
	"plan":        true,
	"acceptEdits": true,
	"default":     true,
}

// ValidPermMode reports whether m is an allowlisted permission mode. "" is NOT
// valid here (callers treat "" as "unset / no flag" separately).
func ValidPermMode(m string) bool { return permModeAllow[m] }

// sessionPermModePath is the sidecar file mapping session name → permission mode.
func sessionPermModePath(dataDir string) string {
	return filepath.Join(dataDir, "session-permmode.json")
}

// permModeMu serialises reads/writes of the sidecar (tiny file, whole-map
// rewrite, same posture as the other JSON sidecars).
var permModeMu sync.Mutex

func loadPermModes(dataDir string) map[string]string {
	m := map[string]string{}
	if data, err := os.ReadFile(sessionPermModePath(dataDir)); err == nil && len(data) > 0 {
		_ = json.Unmarshal(data, &m) // tolerate corruption: start fresh
		if m == nil {
			m = map[string]string{}
		}
	}
	return m
}

// SessionPermMode returns the allowlisted permission mode recorded for session,
// or "" when none/unknown/invalid. nil-safe on inputs.
func SessionPermMode(dataDir, session string) string {
	if dataDir == "" || session == "" {
		return ""
	}
	permModeMu.Lock()
	defer permModeMu.Unlock()
	mode := loadPermModes(dataDir)[safeSessionName(session)]
	if !ValidPermMode(mode) {
		return ""
	}
	return mode
}

// SetSessionPermMode records (or, when mode=="", clears) the permission mode for
// session. mode is allowlist-validated; an unknown non-empty mode is rejected.
// Atomic temp+rename write, same pattern as Registry/Ownership.
func SetSessionPermMode(dataDir, session, mode string) error {
	session = safeSessionName(session)
	permModeMu.Lock()
	defer permModeMu.Unlock()
	m := loadPermModes(dataDir)
	if mode == "" {
		delete(m, session)
	} else {
		if !ValidPermMode(mode) {
			return errPermMode
		}
		m[session] = mode
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp := sessionPermModePath(dataDir) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, sessionPermModePath(dataDir))
}

// permModeArgs returns the discrete argv elements to forward the stored
// permission mode for session (read from the ACTIVE data dir), or nil. Kept
// unexported: only the spawners in this package call it.
func permModeArgs(session string) []string {
	if mode := SessionPermMode(activeDD(), session); mode != "" {
		return []string{"--permission-mode", mode}
	}
	return nil
}
