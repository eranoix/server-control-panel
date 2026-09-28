package scope

import (
	"net/http"
	"os"
	"path/filepath"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/secrets"
)

type Factory struct {
	dataDir string
	vault   *secrets.Store
	primary User
}

func NewFactory(dataDir string, vault *secrets.Store, primary string) *Factory {
	f := &Factory{dataDir: dataDir, vault: vault}
	if primary != "" {
		if u, err := New(primary); err == nil {
			f.primary = u
		}
	}
	return f
}

func (f *Factory) Primary() User { return f.primary }

func (f *Factory) DataDir() string { return f.dataDir }

func (f *Factory) Vault() *secrets.Store { return f.vault }

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
		_ = os.Chmod(d, 0o700)
	}
	return nil
}

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
