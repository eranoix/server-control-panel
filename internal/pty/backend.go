package pty

import "os/exec"

type SessionBackend interface {
	Kind() string

	Attach(name string, cmd []string, env []string, cwd string) (*exec.Cmd, error)

	CreateDetached(name string, argv []string, env []string, cwd string) error

	PasteAndEnter(name, text string) error

	Has(name string) (bool, error)

	List() ([]map[string]any, error)

	Kill(name string) error

	Rename(old, newName string) error

	LogPath(dataDir, user, name string) string
}

func NewSessionBackend(dataDir string, reg *Registry) SessionBackend {
	return newDtachBackend(dataDir, reg)
}
