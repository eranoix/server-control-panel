package mobilebff

import (
	"server-control-panel/internal/config"
	"server-control-panel/internal/httpx"
)

// AdminCapability is the only capability CapabilitiesFor exposes today.
//
// Why a single capability, and not one per resource (e.g. "docker.write",
// "users.manage"): this project's RBAC is binary — httpx.IsAdmin (see
// internal/httpx/rbac.go) is the ONLY predicate behind ALL ~30 call sites of
// mustPrimary/isPrimary in internal/api (docker, scheduler, users, proxmox,
// agents, notify, jira_ai, deploy, ...). There is no intermediate check today
// that would give a user access to "docker" without giving access to "users"
// — the two switch on and off together, always. Listing N capability names
// that always appear together would not make the client any more correct, it
// would only suggest a granularity the server does not enforce — the client
// could learn to check "docker.write" in isolation and break the day RBAC
// really does turn granular and the two stop coinciding. One single name,
// today, is the honest reflection of the real gate. If binary RBAC ever
// becomes truly granular, this is the only spot that has to change (the same
// guarantee sdui.Viewer.Can already gives the SDUI screen filter in
// internal/mobilebff/sdui/viewer.go).
const AdminCapability = "admin.full"

// CapabilitiesFor computes what an authenticated user may do, out of the SAME
// check every admin-only gate in the panel uses: httpx.IsAdmin, which delegates
// to cfg.IsAdmin (config.Primary OR any user carrying the Admin flag). No new
// authorization logic is created here — this helper merely gives the mobile app
// a name for a decision the server already makes everywhere else.
//
// A nil cfg, or an unknown/empty username, is never admin (httpx.IsAdmin
// already guarantees that) — capabilities then come back as an empty slice
// (never nil, so it serializes as `[]` and not `null`).
func CapabilitiesFor(cfg *config.Config, username string) (isAdmin bool, capabilities []string) {
	isAdmin = httpx.IsAdmin(cfg, username)
	if !isAdmin {
		return false, []string{}
	}
	return true, []string{AdminCapability}
}
