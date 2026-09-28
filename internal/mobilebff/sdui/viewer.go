package sdui

import (
	"server-control-panel/internal/config"
	"server-control-panel/internal/httpx"
)

type Viewer struct {
	Username string
	isAdmin  bool
}

func ViewerFrom(cfg *config.Config, username string) Viewer {
	if username == "" {
		return Viewer{}
	}
	return Viewer{
		Username: username,
		isAdmin:  httpx.IsAdmin(cfg, username),
	}
}

func (v Viewer) IsAdmin() bool { return v.isAdmin }

func (v Viewer) Can(capability string) bool {
	_ = capability
	return v.isAdmin
}
