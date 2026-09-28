package mobilebff

import (
	"server-control-panel/internal/config"
	"server-control-panel/internal/httpx"
)

const AdminCapability = "admin.full"

func CapabilitiesFor(cfg *config.Config, username string) (isAdmin bool, capabilities []string) {
	isAdmin = httpx.IsAdmin(cfg, username)
	if !isAdmin {
		return false, []string{}
	}
	return true, []string{AdminCapability}
}
