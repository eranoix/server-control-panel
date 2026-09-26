// backend.go — SessionBackend: the interface of the persistent session engine.
//
// The engine migration is COMPLETE: the engine is dtach (a transparent persister
// that gives xterm.js back native selection/scroll/right-click). The interface
// stays so callers remain decoupled from the concrete engine. The previous
// backend (which no longer exists) was
// removed; the Session* dispatchers (session.go) route to this backend.
package pty

import "os/exec"

// SessionBackend abstracts the persistent session engine. A session is
// identified by its name (already put through safeSessionName by the callers).
type SessionBackend interface {
	// Kind identifies the engine ("dtach") — used in logs/health/diagnostics.
	Kind() string

	// Attach returns an *exec.Cmd ready for pty.Start that attaches-or-creates the
	// session `name`, running `cmd` (e.g. ["/bin/bash","-l"]) with extra `env`
	// (each entry "KEY=VALUE") and cwd `cwd` (""=inherit). The master is born
	// sandboxed in user.slice (it survives a service restart).
	Attach(name string, cmd []string, env []string, cwd string) (*exec.Cmd, error)

	// CreateDetached creates the session `name` DETACHED (no client), running
	// `argv` with `env`/`cwd`, sandboxed in user.slice. Used by the AI spawners
	// (Claude/Jira), which create the session for the user to reattach later.
	CreateDetached(name string, argv []string, env []string, cwd string) error

	// PasteAndEnter injects `text` into the session as a bracketed paste + Enter
	// (via `dtach -p`). Replaces the Jira prompt's load-buffer/paste-buffer/send-keys.
	PasteAndEnter(name, text string) error

	// Has reports whether the session exists and is alive.
	Has(name string) (bool, error)

	// List returns every known session (shape name/tab/created/attached).
	List() ([]map[string]any, error)

	// Kill terminates the session (idempotent).
	Kill(name string) error

	// Rename renames the session (idempotent when old==new).
	Rename(old, newName string) error

	// LogPath returns the path of the session's pty log (the tee). The single
	// source for scrollback/preview/backup.
	LogPath(dataDir, user, name string) string
}

// NewSessionBackend builds the session backend (dtach). dataDir = the socket +
// registry directory; reg = the session catalogue (always pass a real registry).
func NewSessionBackend(dataDir string, reg *Registry) SessionBackend {
	return newDtachBackend(dataDir, reg)
}
