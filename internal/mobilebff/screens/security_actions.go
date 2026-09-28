package screens

import (
	"context"
	"encoding/json"

	"server-control-panel/internal/mobilebff/sdui"
)

const (
	securityActionUserSave          = "security.user.save"
	securityActionUserDelete        = "security.user.delete"
	securityActionUserResetPassword = "security.user.reset_password"

	securityActionSecretSet    = "security.secret.set"
	securityActionSecretDelete = "security.secret.delete"

	securityActionSessionRevoke = "security.session.revoke"
)

const (
	securityActionUFWApply             = "security.ufw.apply"
	securityActionAdGuardSetProtection = "security.adguard.set_protection"

	securityActionDeviceAdd          = "security.device.add"
	securityActionDeviceRemove       = "security.device.remove"
	securityActionDeviceRename       = "security.device.rename"
	securityActionDeviceSetExit      = "security.device.set_exit"
	securityActionDeviceSetDatasaver = "security.device.set_datasaver"
)

func securityAdminViewer(v sdui.Viewer) bool { return v.IsAdmin() }

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
		},
		securityAdminViewer,
		handleSecuritySessionRevoke(deps),
	)
}

func registerNetworkActions(deps NetworkDeps) {
	sdui.RegisterAction(
		sdui.ActionDescriptor{
			ActionID:    securityActionUFWApply,
			Method:      "POST",
			Endpoint:    "/api/mobile/v1/actions/" + securityActionUFWApply,
			Permission:  "admin",
			Destructive: true,
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
		},
		securityAdminViewer,
		handleSecurityDeviceSetDatasaver(deps),
	)
}

type securityUserSaveInput struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Admin    bool   `json:"admin"`
}

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
			return sdui.ActionResult{}, sdui.FieldErrors{}.Add("admin", err.Error())
		}
		if deps.AuditEvent != nil {
			deps.AuditEvent(v.Username, "security.user.save", in.Username)
		}
		return sdui.ActionResult{Patch: securityUserRow(*row)}, nil
	}
}

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

type securityUserDeleteInput struct {
	Username string `json:"username"`
}

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

		if username == v.Username {
			return sdui.ActionResult{}, sdui.FieldErrors{}.Add("username", "cannot delete your own user (logged in right now)")
		}

		if err := deps.DeleteUser(username); err != nil {
			return sdui.ActionResult{}, sdui.FieldErrors{}.Add("username", err.Error())
		}

		if deps.AuditEvent != nil {
			deps.AuditEvent(v.Username, "security.user.delete", username)
		}
		return sdui.ActionResult{Invalidate: []string{"users-table"}}, nil
	}
}

type securityUserResetPasswordInput struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

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

type securityUFWApplyInput struct {
	Action string `json:"action"`
	Spec   string `json:"spec"`
}

var securityUFWValidActions = map[string]bool{
	"allow": true, "deny": true, "reject": true, "delete": true, "enable": true, "disable": true,
}

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
		if in.Exit != "vps" && in.Exit != "home" {
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

type securityDeviceSetDatasaverInput struct {
	On    bool `json:"on"`
	CaAck bool `json:"ca_ack"`
}

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
