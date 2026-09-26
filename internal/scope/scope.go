// Package scope implements per-user tenant isolation for vps-manager.
//
// While `auth` answers "who is this request?", `scope` answers "what can this
// who-is-this see?". The two are deliberately separate: scope depends on the
// vault and the data-dir layout, neither of which auth knows about.
//
// The User type is a distinct string subtype. Construction goes through New(),
// which enforces the [A-Za-z0-9_-] charset and the 1-40 length bound. Raw
// strings cannot reach the filesystem or vault unless they were validated —
// the type system, not vigilance, is what keeps a malicious username from
// becoming "data/users/../etc/passwd".
package scope

import (
	"errors"
	"path/filepath"
	"strings"
)

// User is a sanitized username — the only thing that may be substituted into
// a filesystem path, a vault key prefix, a session name, or a systemd
// instance name. Construct via New(); a zero User is invalid.
type User string

const (
	// MaxUsernameLen bounds storage of usernames in paths, JSON, env files,
	// and systemd unit instance names. systemd actually accepts 256, but we
	// stay conservative — usernames are typed by humans, not generated.
	MaxUsernameLen = 40
)

var (
	// ErrEmpty is returned when New is given "".
	ErrEmpty = errors.New("scope: empty username")
	// ErrTooLong is returned when len(raw) > MaxUsernameLen.
	ErrTooLong = errors.New("scope: username too long")
	// ErrInvalidChar is returned when raw contains a byte outside
	// [A-Za-z0-9_-].
	ErrInvalidChar = errors.New("scope: invalid character in username")
	// ErrReserved blocks names like "system" (used as audit sentinel),
	// ".archive" (delete-target directory), or single/double dot (path
	// traversal regardless of the charset check, defense-in-depth).
	ErrReserved = errors.New("scope: reserved username")
)

// reserved is the closed set of names that pass the charset filter but must
// not be allowed as a real user. "system" is the audit sentinel; "anon" is
// the localStorage fallback prefix; "archive" / ".archive" appear as folder
// names under data/users/. Keep lowercase — comparison is case-insensitive.
var reserved = map[string]struct{}{
	"system":  {},
	"anon":    {},
	"archive": {},
	"root":    {},
	"":        {},
}

// New validates raw and returns it as a User. The validation is
// intentionally strict — any byte outside [A-Za-z0-9_-] is rejected, no
// case folding, no Unicode normalisation. The dot character is explicitly
// disallowed so "..", ".", "a..b" cannot reach a path component.
func New(raw string) (User, error) {
	if raw == "" {
		return "", ErrEmpty
	}
	if len(raw) > MaxUsernameLen {
		return "", ErrTooLong
	}
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		switch {
		case c >= 'a' && c <= 'z':
		case c >= 'A' && c <= 'Z':
		case c >= '0' && c <= '9':
		case c == '_' || c == '-':
		default:
			return "", ErrInvalidChar
		}
	}
	if _, ok := reserved[strings.ToLower(raw)]; ok {
		return "", ErrReserved
	}
	return User(raw), nil
}

// MustNew panics on validation failure. Use only in initialisation paths
// (tests, constants) where the input is a literal and the panic surfaces
// programmer error at boot. Never use with input that crosses a trust
// boundary.
func MustNew(raw string) User {
	u, err := New(raw)
	if err != nil {
		panic("scope.MustNew(" + raw + "): " + err.Error())
	}
	return u
}

// String returns the underlying username. Safe to log and to embed in paths.
func (u User) String() string { return string(u) }

// Valid reports whether u was constructed via New (and not the zero value
// or an explicit cast). Cheap re-check for defense-in-depth.
func (u User) Valid() bool {
	_, err := New(string(u))
	return err == nil
}

// Paths is the closed set of per-user directories and files that vps-manager
// touches. Holding them in one struct means a handler never builds a path by
// hand — it asks for the one it needs and the layout is enforced centrally.
//
// All Root-relative paths live under <DataDir>/users/<User>/. The two paths
// outside DataDir (WhatsappContainer, AIEnv) follow OS conventions and
// cannot easily move.
type Paths struct {
	// Root is <DataDir>/users/<user>/. All app-data lives here.
	Root string

	// Whatsapp is the per-user store of WhatsApp panel state (state.json,
	// chats.json, contacts.json, messages/). Mirrors what whatsapp.Store
	// used to write under <DataDir>/whatsapp/.
	Whatsapp string

	// WhatsappContainer is /var/lib/vpsm-whatsapp/<user>/ — owned by the
	// WAHA container for this user (docker-compose.yml, .env, sessions/,
	// media/, files/). NOT under DataDir because the container's bind
	// mounts need a stable host path that survives DataDir migration.
	WhatsappContainer string

	// WhatsappMedia points at WhatsappContainer + "/media" — sugar for
	// callers that only need the media subdir.
	WhatsappMedia string

	// Uploads is <Root>/uploads/. Replaces /tmp/vpsm-paste-* for
	// terminal-paste images. The session engine runs as root → it still reads 0700.
	Uploads string

	// Browser is <Root>/browser/. Holds browser-instances.json (port
	// allocation for the per-user persistent browser session) plus
	// anything else the browser tab persists.
	Browser string

	// AIEnv is /etc/claude-router/users/<user>.env — the ANTHROPIC_*
	// env file claude-router reads for this user's CLI sessions.
	AIEnv string
}

// PathsFor derives Paths from a DataDir + User. No filesystem access; pure
// computation. Use EnsureDirs to actually create the directories.
func PathsFor(dataDir string, u User) Paths {
	root := filepath.Join(dataDir, "users", u.String())
	wac := filepath.Join("/var/lib/vpsm-whatsapp", u.String())
	return Paths{
		Root:              root,
		Whatsapp:          filepath.Join(root, "whatsapp"),
		WhatsappContainer: wac,
		WhatsappMedia:     filepath.Join(wac, "media"),
		Uploads:           filepath.Join(root, "uploads"),
		Browser:           filepath.Join(root, "browser"),
		AIEnv:             filepath.Join("/etc/claude-router/users", u.String()+".env"),
	}
}

// Scope is the per-request execution context for an authenticated user.
// Handlers should reach for Paths and Vault on Scope, never compute either
// from a raw string. Construction goes through Factory.
type Scope struct {
	User    User
	DataDir string
	Vault   *UserVault
	Paths   Paths
	// IsPrimary is true when this scope's user is the account that
	// inherited legacy untagged state on the v1→v2 migration (config
	// field Primary). Used by the session ACL to surface sessions that
	// existed before per-user prefixing — they belong to the primary
	// user by definition and no rename happens at migration time.
	IsPrimary bool
}
