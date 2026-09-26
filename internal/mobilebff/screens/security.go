package screens

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/config"
	"server-control-panel/internal/httpx"
	"server-control-panel/internal/mobilebff"
	"server-control-panel/internal/mobilebff/sdui"
)

// Screen ids for the four Security screens — also the golden fixture
// filename stems (security.users.*, etc, see
// contracts/sdui/fixtures/screens/).
const (
	securityUsersScreenID    = "security.users"
	securitySecretsScreenID  = "security.secrets"
	securitySessionsScreenID = "security.sessions"
	securityAuditScreenID    = "security.audit"
)

// Screen ids for the four Network screens — registered under the same
// "security." prefix as the four screens above even though the backing seam
// is NetworkDeps, a separate struct (see deps.go's NetworkDeps doc comment
// and PLAN.md's Blocker 3: "rede" was never one screen, it was four genuinely
// distinct subsystems).
const (
	securityUFWScreenID       = "security.ufw"
	securityAdGuardScreenID   = "security.adguard"
	securityDevicesScreenID   = "security.devices"
	securityDataSaverScreenID = "security.economia"
)

// Rows/detail endpoints — one per table or detail screen. Absolute paths
// (carry mobilebff.Prefix), same convention as schedulerJobsRowsEndpoint /
// dockerContainersRowsEndpoint.
const (
	securityUsersRowsEndpoint    = mobilebff.Prefix + "/security/users"
	securitySecretsRowsEndpoint  = mobilebff.Prefix + "/security/secrets"
	securitySessionsRowsEndpoint = mobilebff.Prefix + "/security/sessions"
	securityAuditRowsEndpoint    = mobilebff.Prefix + "/security/audit"

	securityUFWDetailEndpoint     = mobilebff.Prefix + "/security/ufw/status"
	securityAdGuardDetailEndpoint = mobilebff.Prefix + "/security/adguard/status"
	securityDevicesRowsEndpoint   = mobilebff.Prefix + "/security/devices"
	securityDataSaverRowsEndpoint = mobilebff.Prefix + "/security/economia"
)

// securityTimestampFormat mirrors dockerTimestampFormat/schedulerTimestampFormat
// — every timestamp in these eight screens is rendered server-side, never a
// raw epoch.
const securityTimestampFormat = "2006-01-02 15:04 UTC"

// --- RegisterSecurity --------------------------------------------------

// RegisterSecurity wires the four Security screens (users, secrets, sessions,
// audit), their actions and their rows endpoints. Called explicitly by
// internal/api/api.go, mirroring RegisterDocker/RegisterSystem. All four
// screens are admin-only for their WHOLE envelope — see
// buildSecurity*ScreenForViewer below — because every one of them exposes
// either credentials, secret metadata, live session control or the full
// system-wide audit trail, none of which the web panel shows to a non-admin
// either.
func RegisterSecurity(deps SecurityDeps) {
	sdui.Register(securityUsersScreenID, func(_ context.Context, v sdui.Viewer) (*sdui.Envelope, error) {
		return buildSecurityUsersScreenForViewer(v)
	})
	sdui.Register(securitySecretsScreenID, func(_ context.Context, v sdui.Viewer) (*sdui.Envelope, error) {
		return buildSecuritySecretsScreenForViewer(v)
	})
	sdui.Register(securitySessionsScreenID, func(_ context.Context, v sdui.Viewer) (*sdui.Envelope, error) {
		return buildSecuritySessionsScreenForViewer(v)
	})
	sdui.Register(securityAuditScreenID, func(_ context.Context, v sdui.Viewer) (*sdui.Envelope, error) {
		return buildSecurityAuditScreenForViewer(v)
	})

	// Catalog entries. All four are adminOnly because the four builders
	// above are `...ForViewer` — they refuse a non-admin with
	// ErrScreenNotFound, so for a non-admin these items simply do not exist
	// in the picker (omission, never a disabled item).
	sdui.RegisterCatalog(securityUsersScreenID, sdui.GroupSecurity, "Users", adminOnly)
	sdui.RegisterCatalog(securitySecretsScreenID, sdui.GroupSecurity, "Vault secrets", adminOnly)
	sdui.RegisterCatalog(securitySessionsScreenID, sdui.GroupSecurity, "Active sessions", adminOnly)
	sdui.RegisterCatalog(securityAuditScreenID, sdui.GroupSecurity, "Audit log", adminOnly)

	registerSecurityActions(deps)

	mobilebff.Register("security.users.rows", func(api huma.API, mbDeps mobilebff.Deps) {
		registerSecurityUsersRows(api, deps, mbDeps)
	})
	mobilebff.Register("security.secrets.rows", func(api huma.API, mbDeps mobilebff.Deps) {
		registerSecuritySecretsRows(api, deps, mbDeps)
	})
	mobilebff.Register("security.sessions.rows", func(api huma.API, mbDeps mobilebff.Deps) {
		registerSecuritySessionsRows(api, deps, mbDeps)
	})
	mobilebff.Register("security.audit.rows", func(api huma.API, mbDeps mobilebff.Deps) {
		registerSecurityAuditRows(api, deps, mbDeps)
	})

	// Forbidden-for-non-admin ledger: all four screens are whole-screen
	// admin-only (ErrScreenNotFound above), so there is no non-admin
	// envelope to omit anything FROM — each entry only needs to cover the
	// case where the harness still probes an action id directly (see
	// TestGoldenScreens_NonAdminNeverContainsForbiddenStrings and
	// docker.go's dockerPruneScreenID entry for the same reasoning).
	sdui.RegisterForbiddenForNonAdmin(securityUsersScreenID, func() []string {
		return []string{securityActionUserSave, securityActionUserDelete, securityActionUserResetPassword}
	})
	sdui.RegisterForbiddenForNonAdmin(securitySecretsScreenID, func() []string {
		return []string{securityActionSecretSet, securityActionSecretDelete}
	})
	sdui.RegisterForbiddenForNonAdmin(securitySessionsScreenID, func() []string {
		return []string{securityActionSessionRevoke}
	})
	sdui.RegisterForbiddenForNonAdmin(securityAuditScreenID, func() []string {
		// security.audit has no actions at all — the forbidden set is the
		// screen's own id, so a non-admin envelope can never even claim to
		// be the audit screen (belt-and-suspenders on top of
		// ErrScreenNotFound, matching docker.prune's reasoning).
		return []string{securityAuditScreenID}
	})
}

// --- security.users ------------------------------------------------------

// buildSecurityUsersScreenForViewer applies the admin-only gate (non-admin
// Build returns ErrScreenNotFound, the 404-never-403 posture every admin-only
// surface in this package uses) and delegates to buildSecurityUsersScreen.
// Split out, like buildDockerPruneScreenForViewer, so tests can exercise the
// gate directly without going through the global registry.
func buildSecurityUsersScreenForViewer(v sdui.Viewer) (*sdui.Envelope, error) {
	if !v.IsAdmin() {
		return nil, sdui.ErrScreenNotFound
	}
	return buildSecurityUsersScreen(), nil
}

// buildSecurityUsersScreen builds the users-table + user-form + password-
// reset-form screen. One entry point (security.user.save) handles BOTH
// create and edit, exactly like scheduler.job.save — the client populates
// user-form's fields from a selected row to edit, or leaves it blank to
// create. Password reset is a SEPARATE form/action
// (security.user.reset_password), never a field folded into user-form — see
// UserInput's doc comment in deps.go for why.
func buildSecurityUsersScreen() *sdui.Envelope {
	table := sdui.TableComponent{
		ComponentBase: sdui.ComponentBase{Type: sdui.ComponentTypeTable, ID: "users-table"},
		Columns: []sdui.TableColumn{
			{Key: "username", Label: "Username", Kind: "text"},
			{Key: "role", Label: "Role", Kind: "badge", BadgeMap: map[string]string{
				"admin": "success", "user": "neutral",
			}},
			{Key: "has_totp", Label: "2FA", Kind: "badge", BadgeMap: map[string]string{
				"yes": "success", "no": "neutral",
			}},
			{Key: "sessions", Label: "Active sessions", Kind: "text"},
		},
		RowsSource: sdui.DataSource{Endpoint: securityUsersRowsEndpoint},
		RowActions: []sdui.ActionRef{
			{ActionID: securityActionUserDelete, Label: "Remove", Style: "destructive"},
		},
		EmptyState: &sdui.EmptyState{Text: "The accounts that can sign in to the panel, and which of them are administrators. This list should never be empty: your own account is in it. To create another, fill in username and password in the form right below and save."},
	}

	userForm := sdui.FormComponent{
		ComponentBase: sdui.ComponentBase{Type: sdui.ComponentTypeForm, ID: "user-form"},
		Fields: []sdui.FormField{
			{Key: "username", Label: "Username", Kind: "text", Required: true},
			{Key: "password", Label: "Password (only when creating)", Kind: "text", Placeholder: "at least 8 characters — ignored when editing"},
			{Key: "admin", Label: "Administrator", Kind: "bool"},
		},
		SubmitAction: sdui.ActionRef{ActionID: securityActionUserSave, Label: "Save", Style: "primary"},
	}

	passwordForm := sdui.FormComponent{
		ComponentBase: sdui.ComponentBase{Type: sdui.ComponentTypeForm, ID: "user-password-reset-form"},
		Fields: []sdui.FormField{
			{Key: "username", Label: "Username", Kind: "text", Required: true},
			{Key: "password", Label: "New password", Kind: "text", Required: true, Placeholder: "at least 8 characters"},
		},
		SubmitAction: sdui.ActionRef{ActionID: securityActionUserResetPassword, Label: "Reset password", Style: "destructive"},
	}

	deleteConfirm := sdui.ConfirmDestructiveComponent{
		ComponentBase: sdui.ComponentBase{Type: sdui.ComponentTypeConfirmDestructive, ID: "user-delete-confirm"},
		ActionID:      securityActionUserDelete,
		Message:       "This user will be removed permanently, together with their active sessions. You cannot remove yourself or the primary user.",
	}

	// RequireTypedConfirmation empty: a password is recreatable (just set it
	// again), but the confirmation step itself (Destructive:true) is already
	// the separation the plan demands between "saving the user's data" and
	// "changing their password" — see security_actions.go.
	resetConfirm := sdui.ConfirmDestructiveComponent{
		ComponentBase: sdui.ComponentBase{Type: sdui.ComponentTypeConfirmDestructive, ID: "user-password-reset-confirm"},
		ActionID:      securityActionUserResetPassword,
		Message:       "This user's current password will be replaced immediately.",
	}

	screen := sdui.Screen{
		ID:    securityUsersScreenID,
		Title: "Users",
		Components: []sdui.Component{
			table,
			userForm,
			passwordForm,
			deleteConfirm,
			resetConfirm,
		},
	}
	return &sdui.Envelope{Screen: screen}
}

func registerSecurityUsersRows(api huma.API, deps SecurityDeps, mbDeps mobilebff.Deps) {
	registerSecurityRows(api, "getSecurityUserRows", "/security/users", "Linhas de security.users", mbDeps.Cfg,
		func(_ context.Context, _ sdui.Viewer) ([]map[string]any, error) {
			list := deps.ListUsers()
			rows := make([]map[string]any, 0, len(list))
			for _, u := range list {
				rows = append(rows, securityUserRow(u))
			}
			return rows, nil
		})
}

// securityUserRow shapes one UserRow into the row wire format. Wire shape:
// {"id","username","role","has_totp","sessions"} — role collapses
// IsPrimary/IsAdmin into a single badge value ("admin" covers both: the
// primary is always an admin, see config.Config.IsAdmin), matching the
// binary admin/non-admin model this whole surface uses. "id" is the
// username: every action in security_actions.go reads params["id"] for the
// row it was invoked on, same convention as dockerComposeRow.
func securityUserRow(u UserRow) map[string]any {
	role := "user"
	if u.IsAdmin {
		role = "admin"
	}
	totp := "no"
	if u.HasTOTP {
		totp = "yes"
	}
	return map[string]any{
		"id":         u.Username,
		"username":   u.Username,
		"role":       role,
		"is_primary": u.IsPrimary,
		"has_totp":   totp,
		"sessions":   fmt.Sprintf("%d", u.Sessions),
	}
}

// --- security.secrets -----------------------------------------------------

func buildSecuritySecretsScreenForViewer(v sdui.Viewer) (*sdui.Envelope, error) {
	if !v.IsAdmin() {
		return nil, sdui.ErrScreenNotFound
	}
	return buildSecuritySecretsScreen(), nil
}

// buildSecuritySecretsScreen builds the secrets-table + secret-form screen.
// The table carries ONLY key names (SecretKeyRow has no Value field — see
// deps.go); the form's value field is write-only, submitted straight to
// security.secret.set and never echoed back in any Patch/Invalidate result.
func buildSecuritySecretsScreen() *sdui.Envelope {
	table := sdui.TableComponent{
		ComponentBase: sdui.ComponentBase{Type: sdui.ComponentTypeTable, ID: "secrets-table"},
		Columns: []sdui.TableColumn{
			{Key: "key", Label: "Key", Kind: "text"},
		},
		RowsSource: sdui.DataSource{Endpoint: securitySecretsRowsEndpoint},
		RowActions: []sdui.ActionRef{
			{ActionID: securityActionSecretDelete, Label: "Remove", Style: "destructive"},
		},
		EmptyState: &sdui.EmptyState{Text: "The encrypted vault holding the tokens and passwords the panel injects into integrations. Empty is normal if you have not connected any integration yet — nothing breaks because of it. Add a key/value pair in the form below; the value is never shown again once saved."},
	}

	form := sdui.FormComponent{
		ComponentBase: sdui.ComponentBase{Type: sdui.ComponentTypeForm, ID: "secret-form"},
		Fields: []sdui.FormField{
			{Key: "key", Label: "Key", Kind: "text", Required: true},
			{Key: "value", Label: "Value", Kind: "text", Required: true, Placeholder: "never shown again once saved"},
		},
		SubmitAction: sdui.ActionRef{ActionID: securityActionSecretSet, Label: "Save", Style: "primary"},
	}

	confirm := sdui.ConfirmDestructiveComponent{
		ComponentBase: sdui.ComponentBase{Type: sdui.ComponentTypeConfirmDestructive, ID: "secret-delete-confirm"},
		ActionID:      securityActionSecretDelete,
		Message:       "This secret will be removed permanently. Any integration that depends on it stops working.",
	}

	screen := sdui.Screen{
		ID:         securitySecretsScreenID,
		Title:      "Secrets",
		Components: []sdui.Component{table, form, confirm},
	}
	return &sdui.Envelope{Screen: screen}
}

func registerSecuritySecretsRows(api huma.API, deps SecurityDeps, mbDeps mobilebff.Deps) {
	registerSecurityRows(api, "getSecuritySecretRows", "/security/secrets", "Linhas de security.secrets", mbDeps.Cfg,
		func(_ context.Context, _ sdui.Viewer) ([]map[string]any, error) {
			list := deps.ListSecretKeys()
			rows := make([]map[string]any, 0, len(list))
			for _, s := range list {
				rows = append(rows, map[string]any{"id": s.Key, "key": s.Key})
			}
			return rows, nil
		})
}

// --- security.sessions -----------------------------------------------------

func buildSecuritySessionsScreenForViewer(v sdui.Viewer) (*sdui.Envelope, error) {
	if !v.IsAdmin() {
		return nil, sdui.ErrScreenNotFound
	}
	return buildSecuritySessionsScreen(), nil
}

// buildSecuritySessionsScreen builds the sessions-table screen: one row per
// LIVE session across every user (admin console, not the self-service list —
// see SessionRow's doc comment in deps.go). revoke is a real tombstone write
// (sessions.Store.Revoke), never a cosmetic row removal. The confirm message
// is deliberately worded to cover BOTH "someone else's session" and "my own
// current session" in one static string — ConfirmDestructiveComponent
// carries one message per action id, not a per-row variant — while the
// is_current column lets the client highlight the caller's own row distinctly
// before they tap revoke on it.
func buildSecuritySessionsScreen() *sdui.Envelope {
	table := sdui.TableComponent{
		ComponentBase: sdui.ComponentBase{Type: sdui.ComponentTypeTable, ID: "sessions-table"},
		Columns: []sdui.TableColumn{
			{Key: "user", Label: "Username", Kind: "text"},
			{Key: "ip", Label: "IP", Kind: "text"},
			{Key: "user_agent", Label: "Device", Kind: "text"},
			{Key: "issued_at", Label: "Created", Kind: "text"},
			{Key: "last_seen", Label: "Last seen", Kind: "text"},
			{Key: "is_current", Label: "Current session", Kind: "badge", BadgeMap: map[string]string{
				"yes": "warning", "no": "neutral",
			}},
		},
		RowsSource: sdui.DataSource{Endpoint: securitySessionsRowsEndpoint},
		RowActions: []sdui.ActionRef{
			{ActionID: securityActionSessionRevoke, Label: "Revoke", Style: "destructive"},
		},
		EmptyState: &sdui.EmptyState{Text: "Each row is an active sign-in to the panel: user, IP, browser and last access. This screen is never empty — the session you are using right now shows up marked as current. Empty means the session registry did not respond."},
	}

	confirm := sdui.ConfirmDestructiveComponent{
		ComponentBase: sdui.ComponentBase{Type: sdui.ComponentTypeConfirmDestructive, ID: "session-revoke-confirm"},
		ActionID:      securityActionSessionRevoke,
		Message:       "This session will be revoked immediately. If it is your current session (see the 'Current session' column), you will be signed out right now.",
	}

	screen := sdui.Screen{
		ID:         securitySessionsScreenID,
		Title:      "Sessions",
		Components: []sdui.Component{table, confirm},
	}
	return &sdui.Envelope{Screen: screen}
}

// registerSecurityUsersRows and its siblings resolve the Viewer from the
// request themselves (serveSecurityRows), but is_current on a session row
// depends on the CALLER's own session id, not just their username (a user
// can be logged in from several devices at once) — so this rows handler is
// the one place in this package that reads auth.JTIFrom(req) directly,
// instead of going through the generic serveSecurityRows fetch closure.
func registerSecuritySessionsRows(api huma.API, deps SecurityDeps, mbDeps mobilebff.Deps) {
	cfg := mbDeps.Cfg
	huma.Register(api, huma.Operation{
		OperationID: "getSecuritySessionRows",
		Method:      http.MethodGet,
		Path:        "/security/sessions",
		Summary:     "Linhas de security.sessions",
		Tags:        []string{"mobile", "sdui", "security"},
		Middlewares: huma.Middlewares{mobilebff.RequireAuth, serveSecuritySessionsRows(cfg, deps)},
	}, securityRowsDocHandler)
}

func serveSecuritySessionsRows(cfg *config.Config, deps SecurityDeps) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		req, w := humago.Unwrap(ctx)

		username := auth.UserFrom(req)
		v := sdui.ViewerFrom(cfg, username)
		if !v.IsAdmin() {
			httpx.WriteErr(w, http.StatusNotFound, "not_found")
			return
		}

		currentJTI := auth.JTIFrom(req)

		list := deps.ListSessions()
		rows := make([]map[string]any, 0, len(list))
		for _, s := range list {
			rows = append(rows, securitySessionRow(s, currentJTI))
		}

		body, err := json.Marshal(map[string]any{"rows": rows})
		if err != nil {
			log.Printf("mobilebff/screens: error serializing the security.sessions rows: %v", err)
			httpx.WriteErr(w, http.StatusInternalServerError, "internal_error")
			return
		}

		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}
}

// securitySessionRow shapes one SessionRow into the row wire format. Wire
// shape: {"id","user","ip","user_agent","issued_at","last_seen","is_current"}.
// expires_at is intentionally not a column (admin console cares about who is
// live and from where, not TTL bookkeeping) but is still read by
// security_actions.go's revoke handler indirectly through deps.ListSessions
// if ever needed — this row shaper only controls what the TABLE shows.
func securitySessionRow(s SessionRow, currentJTI string) map[string]any {
	isCurrent := "no"
	if currentJTI != "" && s.ID == currentJTI {
		isCurrent = "yes"
	}
	return map[string]any{
		"id":         s.ID,
		"user":       s.User,
		"ip":         s.IP,
		"user_agent": s.UserAgent,
		"issued_at":  formatSecurityTimestamp(s.IssuedAt),
		"last_seen":  formatSecurityTimestamp(s.LastSeen),
		"is_current": isCurrent,
	}
}

// --- security.audit ---------------------------------------------------------

func buildSecurityAuditScreenForViewer(v sdui.Viewer) (*sdui.Envelope, error) {
	if !v.IsAdmin() {
		return nil, sdui.ErrScreenNotFound
	}
	return buildSecurityAuditScreen(), nil
}

// buildSecurityAuditScreen builds the audit-table screen: read-only, no row
// actions, no form, no confirm — the audit trail itself is never mutated
// through this surface. System-wide (AuditRow's doc comment in deps.go), not
// per-tenant filtered — this is the admin console, not a self-service view.
func buildSecurityAuditScreen() *sdui.Envelope {
	table := sdui.TableComponent{
		ComponentBase: sdui.ComponentBase{Type: sdui.ComponentTypeTable, ID: "audit-table"},
		Columns: []sdui.TableColumn{
			{Key: "time", Label: "When", Kind: "text"},
			{Key: "user", Label: "Username", Kind: "text"},
			{Key: "action", Label: "Action", Kind: "text"},
			{Key: "target", Label: "Target", Kind: "text"},
			{Key: "ip", Label: "IP", Kind: "text"},
		},
		RowsSource: sdui.DataSource{Endpoint: securityAuditRowsEndpoint},
		EmptyState: &sdui.EmptyState{Text: "The trail of who did what in the panel: sign-ins, account creation, destructive actions. Empty means nothing has happened yet — an unavailable audit log shows up as a load error, not as an empty table. There is nothing to configure here: rows come in on their own."},
	}
	screen := sdui.Screen{ID: securityAuditScreenID, Title: "Audit log", Components: []sdui.Component{table}}
	return &sdui.Envelope{Screen: screen}
}

// securityAuditRowsLimit is the fixed page size ListAuditEvents is always
// called with — see AuditFilter's doc comment in deps.go for why v1 never
// takes a client-supplied limit.
const securityAuditRowsLimit = 200

func registerSecurityAuditRows(api huma.API, deps SecurityDeps, mbDeps mobilebff.Deps) {
	registerSecurityRows(api, "getSecurityAuditRows", "/security/audit", "Linhas de security.audit", mbDeps.Cfg,
		func(_ context.Context, _ sdui.Viewer) ([]map[string]any, error) {
			list, err := deps.ListAuditEvents(AuditFilter{Limit: securityAuditRowsLimit})
			if err != nil {
				// 500 and not an empty list: the client shows a load error, which
				// is the truth. An empty table would say "nothing happened".
				return nil, err
			}
			rows := make([]map[string]any, 0, len(list))
			for _, e := range list {
				rows = append(rows, securityAuditRow(e))
			}
			return rows, nil
		})
}

// securityAuditRow shapes one AuditRow into the row wire format. Wire shape:
// {"id","time","user","action","target","ip"}. auth.Event has no natural id
// (it is an append-only log line, not a keyed entity), so "id" is synthesized
// from time+user+action — stable enough for client-side row keys, never
// persisted or compared against anything server-side.
func securityAuditRow(e AuditRow) map[string]any {
	return map[string]any{
		"id":     fmt.Sprintf("%d:%s:%s", e.Time, e.User, e.Action),
		"time":   formatSecurityTimestamp(e.Time),
		"user":   e.User,
		"action": e.Action,
		"target": e.Target,
		"ip":     e.IP,
	}
}

// --- RegisterNetwork ---------------------------------------------------

// RegisterNetwork wires the four Network screens (ufw, adguard, devices,
// economia), their actions and their rows/detail endpoints. Called
// explicitly by internal/api/api.go. All four screen ids carry the
// "security." prefix even though the seam is NetworkDeps, a separate struct
// — see NetworkDeps' doc comment in deps.go and PLAN.md's Blocker 3. All four
// are whole-screen admin-only, same posture as the four Security screens.
func RegisterNetwork(deps NetworkDeps) {
	sdui.Register(securityUFWScreenID, func(_ context.Context, v sdui.Viewer) (*sdui.Envelope, error) {
		return buildSecurityUFWScreenForViewer(v)
	})
	sdui.Register(securityAdGuardScreenID, func(_ context.Context, v sdui.Viewer) (*sdui.Envelope, error) {
		return buildSecurityAdGuardScreenForViewer(v)
	})
	sdui.Register(securityDevicesScreenID, func(_ context.Context, v sdui.Viewer) (*sdui.Envelope, error) {
		return buildSecurityDevicesScreenForViewer(v)
	})
	sdui.Register(securityDataSaverScreenID, func(_ context.Context, v sdui.Viewer) (*sdui.Envelope, error) {
		return buildSecurityDataSaverScreenForViewer(v)
	})

	// Catalog entries — the same four network screens, all `...ForViewer` and
	// therefore adminOnly. "Network usage" instead of "Economia": the id is
	// historical (security.economia), but the label has to say what the person
	// will find, not the internal name of the field.
	sdui.RegisterCatalog(securityUFWScreenID, sdui.GroupSecurity, "Firewall (UFW)", adminOnly)
	sdui.RegisterCatalog(securityAdGuardScreenID, sdui.GroupSecurity, "AdGuard DNS", adminOnly)
	sdui.RegisterCatalog(securityDevicesScreenID, sdui.GroupSecurity, "Devices (VLESS)", adminOnly)
	sdui.RegisterCatalog(securityDataSaverScreenID, sdui.GroupSecurity, "Network usage", adminOnly)

	registerNetworkActions(deps)

	mobilebff.Register("security.ufw.detail", func(api huma.API, mbDeps mobilebff.Deps) {
		registerSecurityUFWDetail(api, deps, mbDeps)
	})
	mobilebff.Register("security.adguard.detail", func(api huma.API, mbDeps mobilebff.Deps) {
		registerSecurityAdGuardDetail(api, deps, mbDeps)
	})
	mobilebff.Register("security.devices.rows", func(api huma.API, mbDeps mobilebff.Deps) {
		registerSecurityDevicesRows(api, deps, mbDeps)
	})
	mobilebff.Register("security.economia.rows", func(api huma.API, mbDeps mobilebff.Deps) {
		registerSecurityDataSaverRows(api, deps, mbDeps)
	})

	sdui.RegisterForbiddenForNonAdmin(securityUFWScreenID, func() []string {
		return []string{securityActionUFWApply}
	})
	sdui.RegisterForbiddenForNonAdmin(securityAdGuardScreenID, func() []string {
		return []string{securityActionAdGuardSetProtection}
	})
	sdui.RegisterForbiddenForNonAdmin(securityDevicesScreenID, func() []string {
		return []string{
			securityActionDeviceAdd, securityActionDeviceRemove,
			securityActionDeviceRename, securityActionDeviceSetExit, securityActionDeviceSetDatasaver,
		}
	})
	sdui.RegisterForbiddenForNonAdmin(securityDataSaverScreenID, func() []string {
		// security.economia has no actions at all — same belt-and-suspenders
		// reasoning as security.audit above.
		return []string{securityDataSaverScreenID}
	})
}

// --- security.ufw -----------------------------------------------------------

func buildSecurityUFWScreenForViewer(v sdui.Viewer) (*sdui.Envelope, error) {
	if !v.IsAdmin() {
		return nil, sdui.ErrScreenNotFound
	}
	return buildSecurityUFWScreen(), nil
}

// buildSecurityUFWScreen builds the ufw detail+form screen — the FIRST
// production use of DetailComponent in this codebase (see deps.go's
// NetworkDeps doc comment). The detail shows the raw `ufw status numbered`
// output; the form mirrors handleUFWRule's body (action select +
// verbatim spec text). Applying a firewall rule is admin-only and destructive
// — a bad rule (or a mistaken "disable") can lock the operator out of the
// VPS over SSH, so this screen requires confirmation even though
// NetworkDeps.UFWApplyRule itself performs no such gate (Rule 2: missing
// critical safety confirmation for a genuinely lockout-capable action).
func buildSecurityUFWScreen() *sdui.Envelope {
	detail := sdui.DetailComponent{
		ComponentBase: sdui.ComponentBase{Type: sdui.ComponentTypeDetail, ID: "ufw-status-detail"},
		DataSource:    sdui.DataSource{Endpoint: securityUFWDetailEndpoint},
		Fields: []sdui.DetailField{
			{Key: "enabled", Label: "Status", Kind: "text"},
			{Key: "output", Label: "Rules (ufw status numbered)", Kind: "text"},
		},
	}

	form := sdui.FormComponent{
		ComponentBase: sdui.ComponentBase{Type: sdui.ComponentTypeForm, ID: "ufw-rule-form"},
		Fields: []sdui.FormField{
			{Key: "action", Label: "Action", Kind: "select", Required: true,
				Options: []string{"allow", "deny", "reject", "delete", "enable", "disable"}},
			{Key: "spec", Label: "Rule", Kind: "text", Placeholder: "e.g. 22/tcp — empty for enable/disable"},
		},
		SubmitAction: sdui.ActionRef{ActionID: securityActionUFWApply, Label: "Apply", Style: "destructive"},
	}

	confirm := sdui.ConfirmDestructiveComponent{
		ComponentBase: sdui.ComponentBase{Type: sdui.ComponentTypeConfirmDestructive, ID: "ufw-rule-confirm"},
		ActionID:      securityActionUFWApply,
		Message:       "This changes the firewall immediately. A wrong rule (or disabling the firewall) can lock you out of your own remote access.",
	}

	screen := sdui.Screen{
		ID:         securityUFWScreenID,
		Title:      "Firewall (UFW)",
		Components: []sdui.Component{detail, form, confirm},
	}
	return &sdui.Envelope{Screen: screen}
}

func registerSecurityUFWDetail(api huma.API, deps NetworkDeps, mbDeps mobilebff.Deps) {
	registerSecurityDetail(api, "getSecurityUFWDetail", "/security/ufw/status", "Detalhe de security.ufw", mbDeps.Cfg,
		func(_ context.Context, _ sdui.Viewer) (map[string]any, error) {
			enabled, output, err := deps.UFWStatus()
			if err != nil {
				return map[string]any{
					"enabled": "error: " + err.Error(),
					"output":  output,
				}, nil
			}
			status := "Inactive"
			if enabled {
				status = "Active"
			}
			return map[string]any{
				"enabled": status,
				"output":  output,
			}, nil
		})
}

// --- security.adguard --------------------------------------------------

func buildSecurityAdGuardScreenForViewer(v sdui.Viewer) (*sdui.Envelope, error) {
	if !v.IsAdmin() {
		return nil, sdui.ErrScreenNotFound
	}
	return buildSecurityAdGuardScreen(), nil
}

// buildSecurityAdGuardScreen builds the adguard detail+form screen: current
// protection status plus query stats (detail — mirrors adguard.Status
// exactly) and a form to toggle protection, mirroring
// adguard.Client.SetProtection's (enabled, durationMs) signature exactly.
func buildSecurityAdGuardScreen() *sdui.Envelope {
	detail := sdui.DetailComponent{
		ComponentBase: sdui.ComponentBase{Type: sdui.ComponentTypeDetail, ID: "adguard-status-detail"},
		DataSource:    sdui.DataSource{Endpoint: securityAdGuardDetailEndpoint},
		Fields: []sdui.DetailField{
			{Key: "protection_enabled", Label: "Protection", Kind: "text"},
			{Key: "running", Label: "Service", Kind: "text"},
			{Key: "version", Label: "Version", Kind: "text"},
			{Key: "num_queries", Label: "Queries", Kind: "text"},
			{Key: "num_blocked", Label: "Blocked", Kind: "text"},
			{Key: "blocked_pct", Label: "% blocked", Kind: "text"},
		},
	}

	form := sdui.FormComponent{
		ComponentBase: sdui.ComponentBase{Type: sdui.ComponentTypeForm, ID: "adguard-protection-form"},
		Fields: []sdui.FormField{
			{Key: "enabled", Label: "Protection enabled", Kind: "bool"},
			{Key: "duration_ms", Label: "Pause for (ms, 0 = indefinitely)", Kind: "text", Placeholder: "0"},
		},
		SubmitAction: sdui.ActionRef{ActionID: securityActionAdGuardSetProtection, Label: "Apply", Style: "primary"},
	}

	screen := sdui.Screen{
		ID:         securityAdGuardScreenID,
		Title:      "AdGuard DNS",
		Components: []sdui.Component{detail, form},
	}
	return &sdui.Envelope{Screen: screen}
}

func registerSecurityAdGuardDetail(api huma.API, deps NetworkDeps, mbDeps mobilebff.Deps) {
	registerSecurityDetail(api, "getSecurityAdGuardDetail", "/security/adguard/status", "Detalhe de security.adguard", mbDeps.Cfg,
		func(ctx context.Context, _ sdui.Viewer) (map[string]any, error) {
			status, err := deps.AdGuardStatus(ctx)
			if err != nil {
				return map[string]any{
					"protection_enabled": "error: " + err.Error(),
					"running":            "",
					"version":            "",
					"num_queries":        "",
					"num_blocked":        "",
					"blocked_pct":        "",
				}, nil
			}
			enabled := "Active"
			if !status.ProtectionEnabled {
				enabled = "Paused"
			}
			running := "stopped"
			if status.Running {
				running = "running"
			}
			return map[string]any{
				"protection_enabled": enabled,
				"running":            running,
				"version":            status.Version,
				"num_queries":        fmt.Sprintf("%d", status.NumQueries),
				"num_blocked":        fmt.Sprintf("%d", status.NumBlocked),
				"blocked_pct":        fmt.Sprintf("%.1f%%", status.BlockedPct),
			}, nil
		})
}

// --- security.devices ----------------------------------------------------

func buildSecurityDevicesScreenForViewer(v sdui.Viewer) (*sdui.Envelope, error) {
	if !v.IsAdmin() {
		return nil, sdui.ErrScreenNotFound
	}
	return buildSecurityDevicesScreen(), nil
}

// buildSecurityDevicesScreen builds the devices-table + add-device-form
// screen. rename/set_exit/set_datasaver are non-destructive row actions that
// return a Patch of the updated row (see security_actions.go); remove is
// destructive. set_datasaver additionally requires a health-probe + CA-ack
// gate server-side (deps.go's SetDeviceDatasaver doc comment) — not
// expressed in this screen because SDUI has no eighth "gated toggle"
// component; the gate lives entirely in the action handler.
func buildSecurityDevicesScreen() *sdui.Envelope {
	table := sdui.TableComponent{
		ComponentBase: sdui.ComponentBase{Type: sdui.ComponentTypeTable, ID: "devices-table"},
		Columns: []sdui.TableColumn{
			{Key: "name", Label: "Name", Kind: "text"},
			{Key: "exit", Label: "Exit", Kind: "badge", BadgeMap: map[string]string{
				"vps": "neutral", "casa": "success",
			}},
			{Key: "datasaver", Label: "Datasaver", Kind: "badge", BadgeMap: map[string]string{
				"yes": "warning", "no": "neutral",
			}},
			{Key: "created", Label: "Created", Kind: "text"},
		},
		RowsSource: sdui.DataSource{Endpoint: securityDevicesRowsEndpoint},
		RowActions: []sdui.ActionRef{
			{ActionID: securityActionDeviceRename, Label: "Rename", Style: "secondary"},
			{ActionID: securityActionDeviceSetExit, Label: "Change exit", Style: "secondary"},
			{ActionID: securityActionDeviceSetDatasaver, Label: "Toggle datasaver", Style: "secondary"},
			{ActionID: securityActionDeviceRemove, Label: "Remove", Style: "destructive"},
		},
		EmptyState: &sdui.EmptyState{Text: "The devices allowed to use the VLESS tunnel, each with its own UUID and exit. Empty is the initial state: with no paired device, the tunnel accepts no one. Give the device a name in the form below to create the first one."},
	}

	addForm := sdui.FormComponent{
		ComponentBase: sdui.ComponentBase{Type: sdui.ComponentTypeForm, ID: "device-add-form"},
		Fields: []sdui.FormField{
			{Key: "name", Label: "Device name", Kind: "text", Required: true},
		},
		SubmitAction: sdui.ActionRef{ActionID: securityActionDeviceAdd, Label: "Add", Style: "primary"},
	}

	confirm := sdui.ConfirmDestructiveComponent{
		ComponentBase: sdui.ComponentBase{Type: sdui.ComponentTypeConfirmDestructive, ID: "device-remove-confirm"},
		ActionID:      securityActionDeviceRemove,
		Message:       "This device loses access to the tunnel immediately. Its configuration stops working.",
	}

	screen := sdui.Screen{
		ID:         securityDevicesScreenID,
		Title:      "Devices (VLESS)",
		Components: []sdui.Component{table, addForm, confirm},
	}
	return &sdui.Envelope{Screen: screen}
}

func registerSecurityDevicesRows(api huma.API, deps NetworkDeps, mbDeps mobilebff.Deps) {
	registerSecurityRows(api, "getSecurityDeviceRows", "/security/devices", "Linhas de security.devices", mbDeps.Cfg,
		func(_ context.Context, _ sdui.Viewer) ([]map[string]any, error) {
			list, err := deps.ListDevices()
			if err != nil {
				return nil, err
			}
			rows := make([]map[string]any, 0, len(list))
			for _, d := range list {
				rows = append(rows, securityDeviceRow(d))
			}
			return rows, nil
		})
}

// securityDeviceRow shapes one DeviceRow into the row wire format. Wire
// shape: {"id","name","uuid","exit","datasaver","created"}. "id" is the
// device UUID: every device action in security_actions.go reads params["id"].
func securityDeviceRow(d DeviceRow) map[string]any {
	datasaver := "no"
	if d.Datasaver {
		datasaver = "yes"
	}
	return map[string]any{
		"id":        d.UUID,
		"name":      d.Name,
		"uuid":      d.UUID,
		"exit":      d.Exit,
		"datasaver": datasaver,
		"created":   formatSecurityTimestamp(d.Created),
	}
}

// --- security.economia ---------------------------------------------------

func buildSecurityDataSaverScreenForViewer(v sdui.Viewer) (*sdui.Envelope, error) {
	if !v.IsAdmin() {
		return nil, sdui.ErrScreenNotFound
	}
	return buildSecurityDataSaverScreen(), nil
}

// buildSecurityDataSaverScreen builds the usage-table screen: read-only,
// conntrack-derived per-device usage — no actions, no form, no confirm.
func buildSecurityDataSaverScreen() *sdui.Envelope {
	table := sdui.TableComponent{
		ComponentBase: sdui.ComponentBase{Type: sdui.ComponentTypeTable, ID: "usage-table"},
		Columns: []sdui.TableColumn{
			{Key: "name", Label: "Device", Kind: "text"},
			{Key: "port", Label: "Port", Kind: "text"},
			{Key: "total_bytes", Label: "Total", Kind: "text"},
			{Key: "rate_bps", Label: "Rate", Kind: "text"},
			{Key: "active_conns", Label: "Active connections", Kind: "text"},
		},
		RowsSource: sdui.DataSource{Endpoint: securityDataSaverRowsEndpoint},
		EmptyState: &sdui.EmptyState{Text: "How much of the tunnel each paired device has used, measured by conntrack. Empty means no traffic since the last reading — a collector that is down shows up as a load error, not as an empty table. Start by pairing a device under Devices (VLESS)."},
	}
	screen := sdui.Screen{ID: securityDataSaverScreenID, Title: "Network usage", Components: []sdui.Component{table}}
	return &sdui.Envelope{Screen: screen}
}

func registerSecurityDataSaverRows(api huma.API, deps NetworkDeps, mbDeps mobilebff.Deps) {
	registerSecurityRows(api, "getSecurityEconomiaRows", "/security/economia", "Linhas de security.economia", mbDeps.Cfg,
		func(_ context.Context, _ sdui.Viewer) ([]map[string]any, error) {
			list, err := deps.UsageSnapshot()
			if err != nil {
				return nil, err
			}
			rows := make([]map[string]any, 0, len(list))
			for _, u := range list {
				rows = append(rows, securityUsageRow(u))
			}
			return rows, nil
		})
}

// securityUsageRow shapes one UsageRow into the row wire format. Wire shape:
// {"id","name","port","total_bytes","rate_bps","active_conns"} — bytes and
// rate are rendered as display-ready strings, never raw numbers, same
// convention as formatDockerBytes.
func securityUsageRow(u UsageRow) map[string]any {
	return map[string]any{
		"id":           u.Name,
		"name":         u.Name,
		"port":         fmt.Sprintf("%d", u.Port),
		"total_bytes":  formatSecurityBytes(u.TotalBytes),
		"rate_bps":     formatSecurityRate(u.RateBps),
		"active_conns": fmt.Sprintf("%d", u.ActiveConns),
	}
}

// --- Shared rows/detail plumbing --------------------------------------------

// registerSecurityRows is the shared plumbing for every table-shaped
// endpoint in this file (both Security and Network screens): authenticate,
// resolve Viewer, call fetch, wrap as {"rows":[...]} — the same wire shape
// registerDockerRows/registerSystemRows established, duplicated here per this
// package's one-helper-per-file convention (see docker.go/system.go).
func registerSecurityRows(api huma.API, opID, path, summary string, cfg *config.Config, fetch func(context.Context, sdui.Viewer) ([]map[string]any, error)) {
	huma.Register(api, huma.Operation{
		OperationID: opID,
		Method:      http.MethodGet,
		Path:        path,
		Summary:     summary,
		Tags:        []string{"mobile", "sdui", "security"},
		Middlewares: huma.Middlewares{mobilebff.RequireAuth, serveSecurityRows(cfg, fetch)},
	}, securityRowsDocHandler)
}

type securityRowsInput struct{}

type securityRowsOutput struct {
	Body json.RawMessage
}

func securityRowsDocHandler(_ context.Context, _ *securityRowsInput) (*securityRowsOutput, error) {
	return &securityRowsOutput{Body: json.RawMessage(`{"rows":[]}`)}, nil
}

func serveSecurityRows(cfg *config.Config, fetch func(context.Context, sdui.Viewer) ([]map[string]any, error)) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		req, w := humago.Unwrap(ctx)

		username := auth.UserFrom(req)
		v := sdui.ViewerFrom(cfg, username)
		if !v.IsAdmin() {
			httpx.WriteErr(w, http.StatusNotFound, "not_found")
			return
		}

		rows, err := fetch(req.Context(), v)
		if err != nil {
			log.Printf("mobilebff/screens: error fetching the security rows: %v", err)
			httpx.WriteErr(w, http.StatusInternalServerError, "internal_error")
			return
		}
		if rows == nil {
			rows = []map[string]any{}
		}

		body, err := json.Marshal(map[string]any{"rows": rows})
		if err != nil {
			log.Printf("mobilebff/screens: error serializing the security rows: %v", err)
			httpx.WriteErr(w, http.StatusInternalServerError, "internal_error")
			return
		}

		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}
}

// registerSecurityDetail is the shared plumbing for the two DetailComponent
// endpoints in this file (ufw, adguard): authenticate, resolve Viewer, call
// fetch, wrap as {"detail": {...}} — the wire shape this plan establishes for
// DetailComponent.DataSource, the first production use of that component
// type (see buildSecurityUFWScreen's doc comment). Pinned by a real HTTP
// round-trip test in security_test.go, never a hand-authored fixture, same
// posture as every rows endpoint.
func registerSecurityDetail(api huma.API, opID, path, summary string, cfg *config.Config, fetch func(context.Context, sdui.Viewer) (map[string]any, error)) {
	huma.Register(api, huma.Operation{
		OperationID: opID,
		Method:      http.MethodGet,
		Path:        path,
		Summary:     summary,
		Tags:        []string{"mobile", "sdui", "security"},
		Middlewares: huma.Middlewares{mobilebff.RequireAuth, serveSecurityDetail(cfg, fetch)},
	}, securityDetailDocHandler)
}

type securityDetailInput struct{}

type securityDetailOutput struct {
	Body json.RawMessage
}

func securityDetailDocHandler(_ context.Context, _ *securityDetailInput) (*securityDetailOutput, error) {
	return &securityDetailOutput{Body: json.RawMessage(`{"detail":{}}`)}, nil
}

func serveSecurityDetail(cfg *config.Config, fetch func(context.Context, sdui.Viewer) (map[string]any, error)) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		req, w := humago.Unwrap(ctx)

		username := auth.UserFrom(req)
		v := sdui.ViewerFrom(cfg, username)
		if !v.IsAdmin() {
			httpx.WriteErr(w, http.StatusNotFound, "not_found")
			return
		}

		detail, err := fetch(req.Context(), v)
		if err != nil {
			log.Printf("mobilebff/screens: error fetching the security detail: %v", err)
			httpx.WriteErr(w, http.StatusInternalServerError, "internal_error")
			return
		}
		if detail == nil {
			detail = map[string]any{}
		}

		body, err := json.Marshal(map[string]any{"detail": detail})
		if err != nil {
			log.Printf("mobilebff/screens: error serializing the security detail: %v", err)
			httpx.WriteErr(w, http.StatusInternalServerError, "internal_error")
			return
		}

		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}
}

// --- formatting helpers ------------------------------------------------

// formatSecurityTimestamp renders a Unix epoch as the server-formatted
// display string every timestamp in these eight screens uses — mirrors
// formatDockerTimestamp's zero-is-empty rule.
func formatSecurityTimestamp(epoch int64) string {
	if epoch == 0 {
		return ""
	}
	return time.Unix(epoch, 0).UTC().Format(securityTimestampFormat)
}

// formatSecurityBytes mirrors formatDockerBytes — negative values (no
// equivalent "not calculated" sentinel exists for netusage, but the
// convention is kept for consistency) render as empty.
func formatSecurityBytes(n int64) string {
	if n < 0 {
		return ""
	}
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// formatSecurityRate renders a bytes-per-second float as a human-readable
// display string.
func formatSecurityRate(bps float64) string {
	if bps < 0 {
		return ""
	}
	const unit = 1024.0
	if bps < unit {
		return fmt.Sprintf("%.0f B/s", bps)
	}
	div, exp := unit, 0
	for m := bps / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB/s", bps/div, "KMGTPE"[exp])
}
