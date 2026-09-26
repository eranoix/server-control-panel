package screens

import (
	"context"
	"encoding/json"

	"server-control-panel/internal/mobilebff/sdui"
)

// Action ids the four Security screens reference from their tables'
// row_actions and their forms' submit_action.
const (
	securityActionUserSave          = "security.user.save"
	securityActionUserDelete        = "security.user.delete"
	securityActionUserResetPassword = "security.user.reset_password"

	securityActionSecretSet    = "security.secret.set"
	securityActionSecretDelete = "security.secret.delete"

	securityActionSessionRevoke = "security.session.revoke"
)

// Action ids the four Network screens reference.
const (
	securityActionUFWApply             = "security.ufw.apply"
	securityActionAdGuardSetProtection = "security.adguard.set_protection"

	securityActionDeviceAdd          = "security.device.add"
	securityActionDeviceRemove       = "security.device.remove"
	securityActionDeviceRename       = "security.device.rename"
	securityActionDeviceSetExit      = "security.device.set_exit"
	securityActionDeviceSetDatasaver = "security.device.set_datasaver"
)

// securityAdminViewer is the RegisterAction authorize gate for every one of
// these twelve actions: all eight Security/Network screens are whole-screen
// admin-only (see security.go's buildXScreenForViewer functions), so no
// action reachable from them is ever authenticated-but-non-admin.
func securityAdminViewer(v sdui.Viewer) bool { return v.IsAdmin() }

// registerSecurityActions registers the six security.* mutations
// (user.save/delete/reset_password, secret.set/delete, session.revoke).
// Called once by RegisterSecurity (security.go).
func registerSecurityActions(deps SecurityDeps) {
	sdui.RegisterAction(
		sdui.ActionDescriptor{
			ActionID:   securityActionUserSave,
			Method:     "POST",
			Endpoint:   "/api/mobile/v1/actions/" + securityActionUserSave,
			Permission: "admin",
		},
		securityAdminViewer,
		handleSecurityUserSave(deps),
	)
	sdui.RegisterAction(
		sdui.ActionDescriptor{
			ActionID:    securityActionUserDelete,
			Method:      "POST",
			Endpoint:    "/api/mobile/v1/actions/" + securityActionUserDelete,
			Permission:  "admin",
			Destructive: true,
			// RequireTypedConfirmation deliberately empty: Destructive:true
			// plus the guards inside handleSecurityUserDelete (self-delete,
			// then Primary-protection) already make this proportional —
			// same posture as docker.container.remove.
		},
		securityAdminViewer,
		handleSecurityUserDelete(deps),
	)
	sdui.RegisterAction(
		sdui.ActionDescriptor{
			ActionID:    securityActionUserResetPassword,
			Method:      "POST",
			Endpoint:    "/api/mobile/v1/actions/" + securityActionUserResetPassword,
			Permission:  "admin",
			Destructive: true,
			// Password reset is a SEPARATE action id from security.user.save
			// (see UserInput's doc comment in deps.go) — Destructive:true
			// here is what forces its own confirm step, distinct from the
			// save form's.
		},
		securityAdminViewer,
		handleSecurityUserResetPassword(deps),
	)

	sdui.RegisterAction(
		sdui.ActionDescriptor{
			ActionID:   securityActionSecretSet,
			Method:     "POST",
			Endpoint:   "/api/mobile/v1/actions/" + securityActionSecretSet,
			Permission: "admin",
		},
		securityAdminViewer,
		handleSecuritySecretSet(deps),
	)
	sdui.RegisterAction(
		sdui.ActionDescriptor{
			ActionID:    securityActionSecretDelete,
			Method:      "POST",
			Endpoint:    "/api/mobile/v1/actions/" + securityActionSecretDelete,
			Permission:  "admin",
			Destructive: true,
		},
		securityAdminViewer,
		handleSecuritySecretDelete(deps),
	)

	sdui.RegisterAction(
		sdui.ActionDescriptor{
			ActionID:    securityActionSessionRevoke,
			Method:      "POST",
			Endpoint:    "/api/mobile/v1/actions/" + securityActionSessionRevoke,
			Permission:  "admin",
			Destructive: true,
			// The confirm message (security.go's buildSecuritySessionsScreen)
			// already covers "this may be your own current session" — see
			// SessionRow.IsCurrent's doc comment for why that is a row flag
			// rather than a second static message.
		},
		securityAdminViewer,
		handleSecuritySessionRevoke(deps),
	)
}

// registerNetworkActions registers the six security.ufw/adguard/device
// mutations. Called once by RegisterNetwork (security.go).
func registerNetworkActions(deps NetworkDeps) {
	sdui.RegisterAction(
		sdui.ActionDescriptor{
			ActionID:    securityActionUFWApply,
			Method:      "POST",
			Endpoint:    "/api/mobile/v1/actions/" + securityActionUFWApply,
			Permission:  "admin",
			Destructive: true,
			// A misapplied rule (or "disable") can sever the operator's own
			// SSH access — Destructive:true is a Rule 2 deviation from the
			// plan's baseline, since NetworkDeps.UFWApplyRule itself has no
			// such gate (see security.go's buildSecurityUFWScreen doc
			// comment).
		},
		securityAdminViewer,
		handleSecurityUFWApply(deps),
	)

	sdui.RegisterAction(
		sdui.ActionDescriptor{
			ActionID:   securityActionAdGuardSetProtection,
			Method:     "POST",
			Endpoint:   "/api/mobile/v1/actions/" + securityActionAdGuardSetProtection,
			Permission: "admin",
			// Non-destructive: pausing/resuming DNS filtering is reversible
			// in seconds and carries no lockout risk, unlike UFW.
		},
		securityAdminViewer,
		handleSecurityAdGuardSetProtection(deps),
	)

	sdui.RegisterAction(
		sdui.ActionDescriptor{
			ActionID:   securityActionDeviceAdd,
			Method:     "POST",
			Endpoint:   "/api/mobile/v1/actions/" + securityActionDeviceAdd,
			Permission: "admin",
		},
		securityAdminViewer,
		handleSecurityDeviceAdd(deps),
	)
	sdui.RegisterAction(
		sdui.ActionDescriptor{
			ActionID:    securityActionDeviceRemove,
			Method:      "POST",
			Endpoint:    "/api/mobile/v1/actions/" + securityActionDeviceRemove,
			Permission:  "admin",
			Destructive: true,
		},
		securityAdminViewer,
		handleSecurityDeviceRemove(deps),
	)
	sdui.RegisterAction(
		sdui.ActionDescriptor{
			ActionID:   securityActionDeviceRename,
			Method:     "POST",
			Endpoint:   "/api/mobile/v1/actions/" + securityActionDeviceRename,
			Permission: "admin",
		},
		securityAdminViewer,
		handleSecurityDeviceRename(deps),
	)
	sdui.RegisterAction(
		sdui.ActionDescriptor{
			ActionID:   securityActionDeviceSetExit,
			Method:     "POST",
			Endpoint:   "/api/mobile/v1/actions/" + securityActionDeviceSetExit,
			Permission: "admin",
		},
		securityAdminViewer,
		handleSecurityDeviceSetExit(deps),
	)
	sdui.RegisterAction(
		sdui.ActionDescriptor{
			ActionID:   securityActionDeviceSetDatasaver,
			Method:     "POST",
			Endpoint:   "/api/mobile/v1/actions/" + securityActionDeviceSetDatasaver,
			Permission: "admin",
			// Non-destructive by SDUI's own definition (reversible, no data
			// loss) even though it carries a real outage risk — the risk is
			// mitigated by the ca_ack + health-probe gate inside the handler
			// below, not by a destructive-confirm round trip.
		},
		securityAdminViewer,
		handleSecurityDeviceSetDatasaver(deps),
	)
}

// --- security.user.* handlers -----------------------------------------

// securityUserSaveInput is the body security.user.save decodes — mirrors
// UserInput exactly (deps.go): password is only meaningful on CREATE, never
// read on an edit of an existing username (see UserInput's doc comment for
// why password reset is a wholly separate action).
type securityUserSaveInput struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Admin    bool   `json:"admin"`
}

// handleSecurityUserSave implements security.user.save — create or edit,
// one entry point, exactly like scheduler.job.save. Field-level validation
// mirrors handleUserCreate's rules (username charset/length, password
// length on create) so the mobile surface rejects the same malformed input
// the web panel already does, as sdui.FieldErrors rather than an opaque
// 500.
func handleSecurityUserSave(deps SecurityDeps) sdui.ActionHandler {
	return func(_ context.Context, v sdui.Viewer, _ map[string]string, input json.RawMessage) (sdui.ActionResult, error) {
		var in securityUserSaveInput
		if len(input) > 0 {
			if err := json.Unmarshal(input, &in); err != nil {
				return sdui.ActionResult{}, sdui.FieldErrors{}.Add("username", "invalid request body")
			}
		}

		var fe sdui.FieldErrors
		username := in.Username
		if username == "" {
			fe = fe.Add("username", "username is required")
		} else if len(username) > 40 || !isValidSecurityUsername(username) {
			fe = fe.Add("username", "username may contain only letters, digits, _ and -, up to 40 characters")
		}
		if in.Password != "" && len(in.Password) < 8 {
			fe = fe.Add("password", "password must be at least 8 characters")
		}
		if fe != nil {
			return sdui.ActionResult{}, fe
		}

		row, err := deps.SaveUser(UserInput{Username: in.Username, Password: in.Password, Admin: in.Admin})
		if err != nil {
			// Surfaces internal/config.SetAdmin's own Primary-protection
			// rejection (e.g. demoting the last admin) as a field error —
			// never a reimplemented count check, per the plan's mandate.
			return sdui.ActionResult{}, sdui.FieldErrors{}.Add("admin", err.Error())
		}
		if deps.AuditEvent != nil {
			deps.AuditEvent(v.Username, "security.user.save", in.Username)
		}
		return sdui.ActionResult{Patch: securityUserRow(*row)}, nil
	}
}

// isValidSecurityUsername mirrors handleUserCreate's charset check
// ([a-zA-Z0-9_-]) without pulling in a regexp for one call site.
func isValidSecurityUsername(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

// securityUserDeleteInput is the body security.user.delete decodes.
type securityUserDeleteInput struct {
	Username string `json:"username"`
}

// handleSecurityUserDelete implements security.user.delete. Two guards, in
// this exact order, both BEFORE deps.DeleteUser is ever called:
//
//  1. Self-delete: unconditional, mirrors handlers_users.go's
//     handleUserDelete 403 on deleting the currently logged-in user;
//     checked first and independent of admin count.
//  2. Primary-protection / last-admin lockout: NOT a count check here —
//     deps.DeleteUser wraps internal/config.RemoveUser, whose own
//     Primary-branch rejection is surfaced below as a field error on the
//     error return path. This handler never re-derives "is this the last
//     admin" itself.
func handleSecurityUserDelete(deps SecurityDeps) sdui.ActionHandler {
	return func(_ context.Context, v sdui.Viewer, params map[string]string, input json.RawMessage) (sdui.ActionResult, error) {
		username := params["id"]
		if username == "" {
			var in securityUserDeleteInput
			if len(input) > 0 {
				_ = json.Unmarshal(input, &in)
			}
			username = in.Username
		}
		if username == "" {
			return sdui.ActionResult{}, sdui.ErrActionNotFound
		}

		// Guard 1 — self-delete, unconditional, checked first.
		if username == v.Username {
			return sdui.ActionResult{}, sdui.FieldErrors{}.Add("username", "cannot delete your own user (logged in right now)")
		}

		// Guard 2 — RemoveUser's own Primary-protection rejection, surfaced
		// as a field error, never reimplemented here.
		if err := deps.DeleteUser(username); err != nil {
			return sdui.ActionResult{}, sdui.FieldErrors{}.Add("username", err.Error())
		}

		if deps.AuditEvent != nil {
			deps.AuditEvent(v.Username, "security.user.delete", username)
		}
		return sdui.ActionResult{Invalidate: []string{"users-table"}}, nil
	}
}

// securityUserResetPasswordInput is the body
// security.user.reset_password decodes.
type securityUserResetPasswordInput struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// handleSecurityUserResetPassword implements security.user.reset_password —
// mirrors handleUserResetPassword's exact validation (username non-empty,
// password >= 8 chars) and calls deps.ResetPassword, NEVER deps.SaveUser.
func handleSecurityUserResetPassword(deps SecurityDeps) sdui.ActionHandler {
	return func(_ context.Context, v sdui.Viewer, params map[string]string, input json.RawMessage) (sdui.ActionResult, error) {
		var in securityUserResetPasswordInput
		if len(input) > 0 {
			if err := json.Unmarshal(input, &in); err != nil {
				return sdui.ActionResult{}, sdui.FieldErrors{}.Add("password", "invalid request body")
			}
		}
		username := in.Username
		if username == "" {
			username = params["id"]
		}

		var fe sdui.FieldErrors
		if username == "" {
			fe = fe.Add("username", "username is required")
		}
		if len(in.Password) < 8 {
			fe = fe.Add("password", "password must be at least 8 characters")
		}
		if fe != nil {
			return sdui.ActionResult{}, fe
		}

		if err := deps.ResetPassword(username, in.Password); err != nil {
			return sdui.ActionResult{}, sdui.FieldErrors{}.Add("password", err.Error())
		}
		if deps.AuditEvent != nil {
			deps.AuditEvent(v.Username, "security.user.reset-password", username)
		}
		return sdui.ActionResult{Invalidate: []string{"users-table"}}, nil
	}
}

// --- security.secret.* handlers -----------------------------------------

// securitySecretSetInput is the body security.secret.set decodes. Value
// never appears in ANY response this handler returns (Patch/Invalidate are
// both value-blind — see securitySecretRow's doc comment in security.go).
type securitySecretSetInput struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

func handleSecuritySecretSet(deps SecurityDeps) sdui.ActionHandler {
	return func(_ context.Context, v sdui.Viewer, _ map[string]string, input json.RawMessage) (sdui.ActionResult, error) {
		var in securitySecretSetInput
		if len(input) > 0 {
			if err := json.Unmarshal(input, &in); err != nil {
				return sdui.ActionResult{}, sdui.FieldErrors{}.Add("key", "invalid request body")
			}
		}
		var fe sdui.FieldErrors
		if in.Key == "" {
			fe = fe.Add("key", "key is required")
		}
		if in.Value == "" {
			fe = fe.Add("value", "value is required")
		}
		if fe != nil {
			return sdui.ActionResult{}, fe
		}

		if err := deps.SetSecret(in.Key, in.Value); err != nil {
			return sdui.ActionResult{}, err
		}
		if deps.AuditEvent != nil {
			// Target is the KEY name only — never the value, in the audit
			// line or anywhere else.
			deps.AuditEvent(v.Username, "security.secret.set", in.Key)
		}
		return sdui.ActionResult{Invalidate: []string{"secrets-table"}}, nil
	}
}

func handleSecuritySecretDelete(deps SecurityDeps) sdui.ActionHandler {
	return func(_ context.Context, v sdui.Viewer, params map[string]string, _ json.RawMessage) (sdui.ActionResult, error) {
		key := params["id"]
		if key == "" {
			return sdui.ActionResult{}, sdui.ErrActionNotFound
		}
		if err := deps.DeleteSecret(key); err != nil {
			return sdui.ActionResult{}, err
		}
		if deps.AuditEvent != nil {
			deps.AuditEvent(v.Username, "security.secret.delete", key)
		}
		return sdui.ActionResult{Invalidate: []string{"secrets-table"}}, nil
	}
}

// --- security.session.revoke ---------------------------------------------

// handleSecuritySessionRevoke implements security.session.revoke — a real
// tombstone write (sessions.Store.Revoke via deps.RevokeSession), checked by
// auth's Touch/HasTombstone on every subsequent request for that JTI. No
// self-protection guard here: revoking one's OWN current session is
// explicitly allowed (unlike deleting one's own account) — the confirm
// message already warns the caller they will be logged out if the row they
// picked is their current one.
func handleSecuritySessionRevoke(deps SecurityDeps) sdui.ActionHandler {
	return func(_ context.Context, v sdui.Viewer, params map[string]string, _ json.RawMessage) (sdui.ActionResult, error) {
		sessionID := params["id"]
		if sessionID == "" {
			return sdui.ActionResult{}, sdui.ErrActionNotFound
		}
		if err := deps.RevokeSession(sessionID); err != nil {
			return sdui.ActionResult{}, err
		}
		if deps.AuditEvent != nil {
			deps.AuditEvent(v.Username, "security.session.revoke", sessionID)
		}
		return sdui.ActionResult{Invalidate: []string{"sessions-table"}}, nil
	}
}

// --- security.ufw.apply ---------------------------------------------------

// securityUFWApplyInput is the body security.ufw.apply decodes — mirrors
// handleUFWRule's body exactly (action + spec).
type securityUFWApplyInput struct {
	Action string `json:"action"`
	Spec   string `json:"spec"`
}

var securityUFWValidActions = map[string]bool{
	"allow": true, "deny": true, "reject": true, "delete": true, "enable": true, "disable": true,
}

// securityUFWActionsRequiringSpec are the actions handleUFWRule itself
// treats as needing a non-empty rule spec — enable/disable act on the
// firewall as a whole and take none.
var securityUFWActionsRequiringSpec = map[string]bool{
	"allow": true, "deny": true, "reject": true, "delete": true,
}

func handleSecurityUFWApply(deps NetworkDeps) sdui.ActionHandler {
	return func(_ context.Context, v sdui.Viewer, _ map[string]string, input json.RawMessage) (sdui.ActionResult, error) {
		var in securityUFWApplyInput
		if len(input) > 0 {
			if err := json.Unmarshal(input, &in); err != nil {
				return sdui.ActionResult{}, sdui.FieldErrors{}.Add("action", "invalid request body")
			}
		}
		if !securityUFWValidActions[in.Action] {
			return sdui.ActionResult{}, sdui.FieldErrors{}.Add("action", "invalid action")
		}
		if securityUFWActionsRequiringSpec[in.Action] && in.Spec == "" {
			return sdui.ActionResult{}, sdui.FieldErrors{}.Add("spec", "rule is required for this action")
		}

		output, err := deps.UFWApplyRule(in.Action, in.Spec)
		if err != nil {
			return sdui.ActionResult{}, err
		}
		if deps.AuditEvent != nil {
			deps.AuditEvent(v.Username, "ufw."+in.Action, in.Spec)
		}
		return sdui.ActionResult{Patch: map[string]any{"enabled": "", "output": output}}, nil
	}
}

// --- security.adguard.set_protection ---------------------------------------

type securityAdGuardSetProtectionInput struct {
	Enabled    bool `json:"enabled"`
	DurationMs int  `json:"duration_ms"`
}

func handleSecurityAdGuardSetProtection(deps NetworkDeps) sdui.ActionHandler {
	return func(ctx context.Context, v sdui.Viewer, _ map[string]string, input json.RawMessage) (sdui.ActionResult, error) {
		var in securityAdGuardSetProtectionInput
		if len(input) > 0 {
			if err := json.Unmarshal(input, &in); err != nil {
				return sdui.ActionResult{}, sdui.FieldErrors{}.Add("enabled", "invalid request body")
			}
		}
		if err := deps.AdGuardSetProtection(ctx, in.Enabled, in.DurationMs); err != nil {
			return sdui.ActionResult{}, err
		}
		if deps.AuditEvent != nil {
			action := "security.adguard.pause"
			if in.Enabled {
				action = "security.adguard.resume"
			}
			deps.AuditEvent(v.Username, action, "")
		}
		return sdui.ActionResult{Invalidate: []string{"adguard-status-detail"}}, nil
	}
}

// --- security.device.* handlers -------------------------------------------

type securityDeviceAddInput struct {
	Name string `json:"name"`
}

func handleSecurityDeviceAdd(deps NetworkDeps) sdui.ActionHandler {
	return func(ctx context.Context, v sdui.Viewer, _ map[string]string, input json.RawMessage) (sdui.ActionResult, error) {
		var in securityDeviceAddInput
		if len(input) > 0 {
			if err := json.Unmarshal(input, &in); err != nil {
				return sdui.ActionResult{}, sdui.FieldErrors{}.Add("name", "invalid request body")
			}
		}
		if in.Name == "" {
			return sdui.ActionResult{}, sdui.FieldErrors{}.Add("name", "name is required")
		}

		row, err := deps.AddDevice(ctx, in.Name)
		if err != nil {
			return sdui.ActionResult{}, sdui.FieldErrors{}.Add("name", err.Error())
		}
		if deps.AuditEvent != nil {
			deps.AuditEvent(v.Username, "security.device.add", row.UUID)
		}
		return sdui.ActionResult{Invalidate: []string{"devices-table"}}, nil
	}
}

func handleSecurityDeviceRemove(deps NetworkDeps) sdui.ActionHandler {
	return func(ctx context.Context, v sdui.Viewer, params map[string]string, _ json.RawMessage) (sdui.ActionResult, error) {
		uuid := params["id"]
		if uuid == "" {
			return sdui.ActionResult{}, sdui.ErrActionNotFound
		}
		if err := deps.RemoveDevice(ctx, uuid); err != nil {
			return sdui.ActionResult{}, err
		}
		if deps.AuditEvent != nil {
			deps.AuditEvent(v.Username, "security.device.remove", uuid)
		}
		return sdui.ActionResult{Invalidate: []string{"devices-table"}}, nil
	}
}

type securityDeviceRenameInput struct {
	Name string `json:"name"`
}

func handleSecurityDeviceRename(deps NetworkDeps) sdui.ActionHandler {
	return func(ctx context.Context, v sdui.Viewer, params map[string]string, input json.RawMessage) (sdui.ActionResult, error) {
		uuid := params["id"]
		if uuid == "" {
			return sdui.ActionResult{}, sdui.ErrActionNotFound
		}
		var in securityDeviceRenameInput
		if len(input) > 0 {
			if err := json.Unmarshal(input, &in); err != nil {
				return sdui.ActionResult{}, sdui.FieldErrors{}.Add("name", "invalid request body")
			}
		}
		if in.Name == "" {
			return sdui.ActionResult{}, sdui.FieldErrors{}.Add("name", "name is required")
		}
		if err := deps.RenameDevice(ctx, uuid, in.Name); err != nil {
			return sdui.ActionResult{}, sdui.FieldErrors{}.Add("name", err.Error())
		}
		if deps.AuditEvent != nil {
			deps.AuditEvent(v.Username, "security.device.rename", uuid)
		}
		return sdui.ActionResult{Invalidate: []string{"devices-table"}}, nil
	}
}

type securityDeviceSetExitInput struct {
	Exit string `json:"exit"`
}

func handleSecurityDeviceSetExit(deps NetworkDeps) sdui.ActionHandler {
	return func(ctx context.Context, v sdui.Viewer, params map[string]string, input json.RawMessage) (sdui.ActionResult, error) {
		uuid := params["id"]
		if uuid == "" {
			return sdui.ActionResult{}, sdui.ErrActionNotFound
		}
		var in securityDeviceSetExitInput
		if len(input) > 0 {
			if err := json.Unmarshal(input, &in); err != nil {
				return sdui.ActionResult{}, sdui.FieldErrors{}.Add("exit", "invalid request body")
			}
		}
		if in.Exit != "vps" && in.Exit != "casa" {
			return sdui.ActionResult{}, sdui.FieldErrors{}.Add("exit", "invalid exit")
		}
		if err := deps.SetDeviceExit(ctx, uuid, in.Exit); err != nil {
			return sdui.ActionResult{}, err
		}
		if deps.AuditEvent != nil {
			deps.AuditEvent(v.Username, "security.device.set_exit", uuid)
		}
		return sdui.ActionResult{Invalidate: []string{"devices-table"}}, nil
	}
}

// securityDeviceSetDatasaverInput is the body security.device.set_datasaver
// decodes. CaAck mirrors handleTunnelDeviceAction's own confirmation
// checkbox for this exact toggle ("it has broken the tunnel repeatedly" — see
// NetworkDeps.SetDeviceDatasaver's doc comment): required whenever `on` is
// true, never checked when turning datasaver off.
type securityDeviceSetDatasaverInput struct {
	On    bool `json:"on"`
	CaAck bool `json:"ca_ack"`
}

// handleSecurityDeviceSetDatasaver implements security.device.set_datasaver,
// reproducing handleTunnelDeviceAction's two-gate safety pair (Rule 2 — a
// missing critical safety check, since NetworkDeps.SetDeviceDatasaver itself
// performs neither):
//
//  1. ca_ack must be explicitly true before turning datasaver ON — a
//     lightweight confirmation that the caller understands the
//     compression proxy's CA cert must already be trusted on the device.
//  2. A live health probe of the exit's compression proxy
//     (ProbeDatasaverHealthy) must succeed before the toggle is applied —
//     turning this on against an unhealthy proxy is the documented outage
//     this pair exists to prevent.
//
// Neither gate applies when turning datasaver OFF (on == false).
func handleSecurityDeviceSetDatasaver(deps NetworkDeps) sdui.ActionHandler {
	return func(ctx context.Context, v sdui.Viewer, params map[string]string, input json.RawMessage) (sdui.ActionResult, error) {
		uuid := params["id"]
		if uuid == "" {
			return sdui.ActionResult{}, sdui.ErrActionNotFound
		}
		var in securityDeviceSetDatasaverInput
		if len(input) > 0 {
			if err := json.Unmarshal(input, &in); err != nil {
				return sdui.ActionResult{}, sdui.FieldErrors{}.Add("on", "invalid request body")
			}
		}

		if in.On {
			if !in.CaAck {
				return sdui.ActionResult{}, sdui.FieldErrors{}.Add("ca_ack", "confirm that the compression proxy certificate is already trusted on the device")
			}
			list, err := deps.ListDevices()
			if err != nil {
				return sdui.ActionResult{}, err
			}
			exit := ""
			for _, d := range list {
				if d.UUID == uuid {
					exit = d.Exit
					break
				}
			}
			if exit == "" {
				return sdui.ActionResult{}, sdui.ErrActionNotFound
			}
			if _, err := deps.ProbeDatasaverHealthy(ctx, exit); err != nil {
				return sdui.ActionResult{}, sdui.FieldErrors{}.Add("on", "compression proxy unavailable: "+err.Error())
			}
		}

		if err := deps.SetDeviceDatasaver(ctx, uuid, in.On); err != nil {
			return sdui.ActionResult{}, err
		}
		if deps.AuditEvent != nil {
			deps.AuditEvent(v.Username, "security.device.set_datasaver", uuid)
		}
		return sdui.ActionResult{Invalidate: []string{"devices-table"}}, nil
	}
}
