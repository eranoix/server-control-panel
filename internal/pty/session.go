// session.go — the active backend (a package singleton) + tool-agnostic dispatchers.
//
// Handlers and spawners used to call the engine directly. So that swapping the
// engine could be driven by ONE flag (a parallel run +
// instant rollback), they now call the Session* dispatchers here, which route to
// the active backend. (There used to be an IMPLEMENTATION of
// the old engine behind them — the dispatchers never call the old functions, so
// there is no recursion; today it no longer exists — the only backend is dtach.)
package pty

import (
	"sync"

	"server-control-panel/internal/claudever"
)

var (
	activeMu      sync.RWMutex
	activeBackend SessionBackend
	activeDataDir string
	activeReg     *Registry
	activeOwn     *Ownership // posse ativa — resolve o DONO de um nome sem o caller
)

// SetActiveOwnership registers the active ownership map so the package can
// resolve who owns a session when the caller does not pass the user (backup,
// Jira watcher). Without it, those paths did a users/* glob + matches[0] and
// could read ANOTHER user's log (names like "main" collide). Called at boot,
// after LoadOwnership. nil is tolerated (Owner is nil-safe).
func SetActiveOwnership(o *Ownership) {
	activeMu.Lock()
	defer activeMu.Unlock()
	activeOwn = o
}

// activeOwner returns the registered owner of name ("" if unknown). nil-safe.
func activeOwner(name string) string {
	activeMu.RLock()
	o := activeOwn
	activeMu.RUnlock()
	return o.Owner(name)
}

// activeCWDFn resolves a session's cwd by name (source: session-cwd.json,
// populated by the agent's hooks). Used on snapshot/restore to recreate the
// session in the right directory instead of falling back to $HOME.
var activeCWDFn func(string) string

// SetActiveCWDResolver registers the per-session cwd resolver. Called at boot.
func SetActiveCWDResolver(fn func(string) string) {
	activeMu.Lock()
	activeCWDFn = fn
	activeMu.Unlock()
}

// resolveSessionCWD returns the session's known cwd ("" if unknown).
func resolveSessionCWD(name string) string {
	activeMu.RLock()
	fn := activeCWDFn
	activeMu.RUnlock()
	if fn == nil {
		return ""
	}
	return fn(name)
}

// InitSessionBackend pins the active backend from the VPSM_SESSION_BACKEND flag.
// Called once at server boot, after loading the registry.
func InitSessionBackend(dataDir string, reg *Registry) {
	activeMu.Lock()
	defer activeMu.Unlock()
	activeDataDir = dataDir
	activeReg = reg
	activeBackend = NewSessionBackend(dataDir, reg)
}

// ActiveBackend returns the active backend (dtach). If it has not been
// initialised yet (early boot / tests), it builds a dtach backend from the
// current state (dataDir/reg may be empty — it degrades, but is never nil).
func ActiveBackend() SessionBackend {
	activeMu.RLock()
	defer activeMu.RUnlock()
	if activeBackend == nil {
		return newDtachBackend(activeDataDir, activeReg)
	}
	return activeBackend
}

func activeDD() string {
	activeMu.RLock()
	defer activeMu.RUnlock()
	return activeDataDir
}

// activeSocket resolves `name`'s dtach socket: the one recorded in the active
// registry, or the default derived from the name. Used by the backup/proc
// operations on the dtach backend.
func activeSocket(name string) string {
	activeMu.RLock()
	dd, reg := activeDataDir, activeReg
	activeMu.RUnlock()
	if reg != nil {
		if rec, ok := reg.Get(name); ok && rec.Socket != "" {
			return rec.Socket
		}
	}
	return socketPathFor(dd, name)
}

// ── Tool-agnostic dispatchers (the handlers call these) ────────────────────

// SessionHas reports whether the session exists / is alive on the active backend.
func SessionHas(name string) (bool, error) { return ActiveBackend().Has(name) }

// SessionKill terminates the session (idempotent).
func SessionKill(name string) error { return ActiveBackend().Kill(name) }

// SessionRename renames the session.
func SessionRename(old, newName string) error { return ActiveBackend().Rename(old, newName) }

// SessionListAll lists every session known to the active backend.
func SessionListAll() ([]map[string]any, error) { return ActiveBackend().List() }

// SessionCreateDetached creates a detached session running argv (the AI spawners).
func SessionCreateDetached(name string, argv, env []string, cwd string) error {
	return ActiveBackend().CreateDetached(name, argv, env, cwd)
}

// SessionPasteAndEnter injects text into the session as a bracketed paste + Enter.
func SessionPasteAndEnter(name, text string) error {
	return ActiveBackend().PasteAndEnter(name, text)
}

// SessionTail returns the tail (plain text) of the session's scrollback without
// needing the user — used by the Jira watcher. It reads the pty log through a
// glob. "" on failure.
func SessionTail(name string, lines int) string {
	return dtachSessionScrollback(name, lines)
}

// SessionScrollback returns the scrollback used to prime the terminal on attach /
// preview / readiness detection — the tail of the pty log (the single source that
// replaced capture-pane). escapes=false → plain text (ANSI stripped, for "copy
// everything"). "" on any failure (best-effort).
func SessionScrollback(user, name string, lines int, escapes bool) string {
	return tailSessionLog(activeDD(), user, name, lines, escapes)
}

// SessionRawLogTail returns the last maxBytes of the session's RAW log (escapes
// included) and the log's total size. It is what the app asks for on attach in
// order to prime its own emulator: it has a libghostty-vt and knows how to
// assemble the screen from the bytes, which no rendering done here could match —
// see rawLogTail. nil/0 on any failure (best-effort).
func SessionRawLogTail(user, name string, maxBytes int) ([]byte, int) {
	return rawLogTail(activeDD(), user, name, maxBytes)
}

// SessionSockets returns socket→name for the LIVE sessions of the active
// backend. It is what lets you tell which session a process belongs to by
// walking up the process tree: the master's argv (`dtach -n <socket> …`) carries
// the path, which is unique per session.
//
// Needed because the dtach record keeps no PID — the master is forked by
// `dtach -n`, and the PID the server sees when spawning dies right afterwards.
// Without this anchor, nothing links a running `claude` to its session.
//
// An empty map when there is no live session.
func SessionSockets() map[string]string {
	out := map[string]string{}
	sess, err := SessionListAll()
	if err != nil {
		return out
	}
	dd := activeDD()
	activeMu.RLock()
	reg := activeReg
	activeMu.RUnlock()
	for _, s := range sess {
		name, _ := s["name"].(string)
		if name == "" {
			continue
		}
		sock := ""
		if reg != nil {
			if rec, ok := reg.Get(name); ok && rec.Socket != "" {
				sock = rec.Socket
			}
		}
		if sock == "" {
			sock = socketPathFor(dd, name)
		}
		out[sock] = name
	}
	return out
}

// SessionOf returns the name of the session that owns pid, or "" when it belongs
// to no session of the active backend. It walks up the process tree until it
// finds the master (see SessionSockets).
func SessionOf(pid int) string {
	return claudever.AncestralPorArgv(pid, SessionSockets())
}
