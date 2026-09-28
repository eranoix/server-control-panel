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

const (
	securityUsersScreenID    = "security.users"
	securitySecretsScreenID  = "security.secrets"
	securitySessionsScreenID = "security.sessions"
	securityAuditScreenID    = "security.audit"
)

const (
	securityUFWScreenID       = "security.ufw"
	securityAdGuardScreenID   = "security.adguard"
	securityDevicesScreenID   = "security.devices"
	securityDataSaverScreenID = "security.savings"
)

const (
	securityUsersRowsEndpoint    = mobilebff.Prefix + "/security/users"
	securitySecretsRowsEndpoint  = mobilebff.Prefix + "/security/secrets"
	securitySessionsRowsEndpoint = mobilebff.Prefix + "/security/sessions"
	securityAuditRowsEndpoint    = mobilebff.Prefix + "/security/audit"

	securityUFWDetailEndpoint     = mobilebff.Prefix + "/security/ufw/status"
	securityAdGuardDetailEndpoint = mobilebff.Prefix + "/security/adguard/status"
	securityDevicesRowsEndpoint   = mobilebff.Prefix + "/security/devices"
	securityDataSaverRowsEndpoint = mobilebff.Prefix + "/security/savings"
)

const securityTimestampFormat = "2006-01-02 15:04 UTC"

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
		return []string{securityAuditScreenID}
	})
}

func buildSecurityUsersScreenForViewer(v sdui.Viewer) (*sdui.Envelope, error) {
	if !v.IsAdmin() {
		return nil, sdui.ErrScreenNotFound
	}
	return buildSecurityUsersScreen(), nil
}

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
	registerSecurityRows(api, "getSecurityUserRows", "/security/users", "Rows of security.users", mbDeps.Cfg,
		func(_ context.Context, _ sdui.Viewer) ([]map[string]any, error) {
			list := deps.ListUsers()
			rows := make([]map[string]any, 0, len(list))
			for _, u := range list {
				rows = append(rows, securityUserRow(u))
			}
			return rows, nil
		})
}

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

func buildSecuritySecretsScreenForViewer(v sdui.Viewer) (*sdui.Envelope, error) {
	if !v.IsAdmin() {
		return nil, sdui.ErrScreenNotFound
	}
	return buildSecuritySecretsScreen(), nil
}

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
	registerSecurityRows(api, "getSecuritySecretRows", "/security/secrets", "Rows of security.secrets", mbDeps.Cfg,
		func(_ context.Context, _ sdui.Viewer) ([]map[string]any, error) {
			list := deps.ListSecretKeys()
			rows := make([]map[string]any, 0, len(list))
			for _, s := range list {
				rows = append(rows, map[string]any{"id": s.Key, "key": s.Key})
			}
			return rows, nil
		})
}

func buildSecuritySessionsScreenForViewer(v sdui.Viewer) (*sdui.Envelope, error) {
	if !v.IsAdmin() {
		return nil, sdui.ErrScreenNotFound
	}
	return buildSecuritySessionsScreen(), nil
}

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

func registerSecuritySessionsRows(api huma.API, deps SecurityDeps, mbDeps mobilebff.Deps) {
	cfg := mbDeps.Cfg
	huma.Register(api, huma.Operation{
		OperationID: "getSecuritySessionRows",
		Method:      http.MethodGet,
		Path:        "/security/sessions",
		Summary:     "Rows of security.sessions",
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

func buildSecurityAuditScreenForViewer(v sdui.Viewer) (*sdui.Envelope, error) {
	if !v.IsAdmin() {
		return nil, sdui.ErrScreenNotFound
	}
	return buildSecurityAuditScreen(), nil
}

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

const securityAuditRowsLimit = 200

func registerSecurityAuditRows(api huma.API, deps SecurityDeps, mbDeps mobilebff.Deps) {
	registerSecurityRows(api, "getSecurityAuditRows", "/security/audit", "Rows of security.audit", mbDeps.Cfg,
		func(_ context.Context, _ sdui.Viewer) ([]map[string]any, error) {
			list, err := deps.ListAuditEvents(AuditFilter{Limit: securityAuditRowsLimit})
			if err != nil {
				return nil, err
			}
			rows := make([]map[string]any, 0, len(list))
			for _, e := range list {
				rows = append(rows, securityAuditRow(e))
			}
			return rows, nil
		})
}

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
	mobilebff.Register("security.savings.rows", func(api huma.API, mbDeps mobilebff.Deps) {
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
		return []string{securityDataSaverScreenID}
	})
}

func buildSecurityUFWScreenForViewer(v sdui.Viewer) (*sdui.Envelope, error) {
	if !v.IsAdmin() {
		return nil, sdui.ErrScreenNotFound
	}
	return buildSecurityUFWScreen(), nil
}

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
	registerSecurityDetail(api, "getSecurityUFWDetail", "/security/ufw/status", "Detail of security.ufw", mbDeps.Cfg,
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

func buildSecurityAdGuardScreenForViewer(v sdui.Viewer) (*sdui.Envelope, error) {
	if !v.IsAdmin() {
		return nil, sdui.ErrScreenNotFound
	}
	return buildSecurityAdGuardScreen(), nil
}

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
	registerSecurityDetail(api, "getSecurityAdGuardDetail", "/security/adguard/status", "Detail of security.adguard", mbDeps.Cfg,
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

func buildSecurityDevicesScreenForViewer(v sdui.Viewer) (*sdui.Envelope, error) {
	if !v.IsAdmin() {
		return nil, sdui.ErrScreenNotFound
	}
	return buildSecurityDevicesScreen(), nil
}

func buildSecurityDevicesScreen() *sdui.Envelope {
	table := sdui.TableComponent{
		ComponentBase: sdui.ComponentBase{Type: sdui.ComponentTypeTable, ID: "devices-table"},
		Columns: []sdui.TableColumn{
			{Key: "name", Label: "Name", Kind: "text"},
			{Key: "exit", Label: "Exit", Kind: "badge", BadgeMap: map[string]string{
				"vps": "neutral", "home": "success",
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
	registerSecurityRows(api, "getSecurityDeviceRows", "/security/devices", "Rows of security.devices", mbDeps.Cfg,
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

func buildSecurityDataSaverScreenForViewer(v sdui.Viewer) (*sdui.Envelope, error) {
	if !v.IsAdmin() {
		return nil, sdui.ErrScreenNotFound
	}
	return buildSecurityDataSaverScreen(), nil
}

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
	registerSecurityRows(api, "getSecuritySavingsRows", "/security/savings", "Rows of security.savings", mbDeps.Cfg,
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

func formatSecurityTimestamp(epoch int64) string {
	if epoch == 0 {
		return ""
	}
	return time.Unix(epoch, 0).UTC().Format(securityTimestampFormat)
}

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
