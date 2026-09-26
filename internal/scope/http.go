package scope

import (
	"net/http"
	"os"
	"path/filepath"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/secrets"
)

// Factory builds Scope values for incoming requests. One Factory per Router;
// it carries the immutable bits (DataDir, vault, primary) so handlers only
// need to hand a *http.Request to get back a Scope.
type Factory struct {
	dataDir string
	vault   *secrets.Store
	primary User
}

// NewFactory binds a Factory to a data directory and a vault. Both are
// required — vault may be nil only in tests that explicitly skip secrets.
// primary is the username from config.Primary (may be empty on a fresh
// install before migration runs); when non-empty it must validate.
func NewFactory(dataDir string, vault *secrets.Store, primary string) *Factory {
	f := &Factory{dataDir: dataDir, vault: vault}
	if primary != "" {
		if u, err := New(primary); err == nil {
			f.primary = u
		}
	}
	return f
}

// Primary returns the primary user the factory was built with, or "" when
// none is configured. Exposed for the few callers that need to short-circuit
// on "is anybody primary at all" (boot logging, migration probes).
func (f *Factory) Primary() User { return f.primary }

// DataDir is the root data directory the factory uses to build paths.
func (f *Factory) DataDir() string { return f.dataDir }

// Vault exposes the underlying *secrets.Store. Reserved for migration and
// the few callers (audit init, sessions store) that need to reach global
// secrets bypassing the per-user wrapper.
func (f *Factory) Vault() *secrets.Store { return f.vault }

// For builds a Scope for an explicit User. Use when there's no request to
// derive identity from (cron jobs, lifecycle hooks). EnsureDirs is NOT
// called here — caller decides.
func (f *Factory) For(u User) Scope {
	var v *UserVault
	if f.vault != nil {
		v = NewUserVault(f.vault, u)
	}
	return Scope{
		User:      u,
		DataDir:   f.dataDir,
		Vault:     v,
		Paths:     PathsFor(f.dataDir, u),
		IsPrimary: f.primary != "" && u == f.primary,
	}
}

// FromRequest returns the Scope for the authenticated user on r, or an
// error when the request is unauthenticated or the username on it fails
// validation. The username comes from auth.UserFrom(r) — the JWT sub —
// and nowhere else. Query string ?user=X is never consulted.
func (f *Factory) FromRequest(r *http.Request) (Scope, error) {
	raw := auth.UserFrom(r)
	if raw == "" {
		return Scope{}, ErrEmpty
	}
	u, err := New(raw)
	if err != nil {
		return Scope{}, err
	}
	return f.For(u), nil
}

// Require returns the Scope for the authenticated user, or writes a 401
// to w and returns ok=false. Idiomatic in handler bodies:
//
//	sc, ok := f.Require(w, r)
//	if !ok {
//	    return
//	}
//	// use sc.Paths.*, sc.Vault.*
//
// If the request authenticated but its sub fails scope.New (a username
// that somehow slipped past the user-create validator), the response is
// 500 — a server-side invariant has been violated and the client cannot
// recover.
func (f *Factory) Require(w http.ResponseWriter, r *http.Request) (Scope, bool) {
	sc, err := f.FromRequest(r)
	if err != nil {
		switch err {
		case ErrEmpty:
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		default:
			http.Error(w, "internal scope error", http.StatusInternalServerError)
		}
		return Scope{}, false
	}
	return sc, true
}

// EnsureDirs creates the per-user data directories with 0o700 perms.
// Idempotent: existing dirs with the right mode are left alone, missing
// ones are created, wrong-mode existing dirs are chmod'd back to 0o700.
//
// Called eagerly at boot for every user in config.AllUsers(), and again
// from the user-create hook. Lazy creation would race the first request.
func EnsureDirs(p Paths) error {
	dirs := []string{
		p.Root,
		p.Whatsapp,
		filepath.Join(p.Whatsapp, "messages"),
		p.Uploads,
		p.Browser,
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
		// MkdirAll honours umask; force 0o700 on top so a 0o022 umask
		// doesn't silently widen the tenant boundary.
		_ = os.Chmod(d, 0o700)
	}
	return nil
}

// EnsureWhatsappContainerDirs creates the /var/lib/vpsm-whatsapp/<user>/
// tree the WAHA container needs. Separate from EnsureDirs because not
// every test or non-WhatsApp boot path needs to touch /var/lib.
//
// Permissions: 0o700 on the per-user root, 0o700 on the volumes. The
// container itself runs as root inside the namespace, so the host
// permissions only need to keep human users out.
func EnsureWhatsappContainerDirs(p Paths) error {
	dirs := []string{
		p.WhatsappContainer,
		filepath.Join(p.WhatsappContainer, "sessions"),
		filepath.Join(p.WhatsappContainer, "media"),
		filepath.Join(p.WhatsappContainer, "files"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
		_ = os.Chmod(d, 0o700)
	}
	return nil
}
