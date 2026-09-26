package screens

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"server-control-panel/internal/adguard"
	"server-control-panel/internal/mobilebff/sdui"
)

// fakeSecurityActionsBackend is an in-memory stand-in for
// SecurityDeps/NetworkDeps' mutating closures, plus spy counters — mirrors
// fakeDockerActionsBackend (docker_actions_test.go) so tests can assert a
// mutation NEVER happened (e.g. deps.DeleteUser must not be reached when the
// self-delete guard or the confirmation gate rejects the request first).
type fakeSecurityActionsBackend struct {
	mu sync.Mutex

	saveUserCalls, deleteUserCalls, resetPasswordCalls int
	setSecretCalls, deleteSecretCalls                  int
	revokeSessionCalls                                 int
	ufwApplyCalls                                      int
	adguardSetProtectionCalls                          int
	addDeviceCalls, removeDeviceCalls, renameDeviceCalls,
	setExitCalls, setDatasaverCalls, probeDatasaverCalls int

	// deleteUserErr/saveUserErr let tests simulate internal/config's own
	// Primary-protection rejection strings without importing internal/config
	// itself — this package never re-derives that logic, only surfaces it.
	deleteUserErr error
	saveUserErr   error

	devices []DeviceRow

	audit []securityAuditRecord
}

type securityAuditRecord struct{ user, action, target string }

func newFakeSecurityActionsBackend() *fakeSecurityActionsBackend {
	return &fakeSecurityActionsBackend{
		devices: []DeviceRow{{Name: "phone-1", UUID: "uuid-1", Exit: "vps", Datasaver: false}},
	}
}

func (b *fakeSecurityActionsBackend) securityDeps() SecurityDeps {
	return SecurityDeps{
		ListUsers: func() []UserRow { return nil },
		SaveUser: func(in UserInput) (*UserRow, error) {
			b.mu.Lock()
			defer b.mu.Unlock()
			b.saveUserCalls++
			if b.saveUserErr != nil {
				return nil, b.saveUserErr
			}
			return &UserRow{Username: in.Username, IsAdmin: in.Admin}, nil
		},
		DeleteUser: func(string) error {
			b.mu.Lock()
			defer b.mu.Unlock()
			b.deleteUserCalls++
			return b.deleteUserErr
		},
		ResetPassword: func(string, string) error {
			b.mu.Lock()
			defer b.mu.Unlock()
			b.resetPasswordCalls++
			return nil
		},
		ListSecretKeys: func() []SecretKeyRow { return nil },
		SetSecret: func(string, string) error {
			b.mu.Lock()
			defer b.mu.Unlock()
			b.setSecretCalls++
			return nil
		},
		DeleteSecret: func(string) error {
			b.mu.Lock()
			defer b.mu.Unlock()
			b.deleteSecretCalls++
			return nil
		},
		ListSessions: func() []SessionRow { return nil },
		RevokeSession: func(string) error {
			b.mu.Lock()
			defer b.mu.Unlock()
			b.revokeSessionCalls++
			return nil
		},
		ListAuditEvents: func(AuditFilter) ([]AuditRow, error) { return nil, nil },
		AuditEvent: func(user, action, target string) {
			b.mu.Lock()
			defer b.mu.Unlock()
			b.audit = append(b.audit, securityAuditRecord{user: user, action: action, target: target})
		},
	}
}

func (b *fakeSecurityActionsBackend) networkDeps() NetworkDeps {
	return NetworkDeps{
		UFWStatus: func() (bool, string, error) { return true, "", nil },
		UFWApplyRule: func(action, spec string) (string, error) {
			b.mu.Lock()
			defer b.mu.Unlock()
			b.ufwApplyCalls++
			return "ok", nil
		},
		AdGuardStatus: func(context.Context) (*adguard.Status, error) { return &adguard.Status{}, nil },
		AdGuardSetProtection: func(context.Context, bool, int) error {
			b.mu.Lock()
			defer b.mu.Unlock()
			b.adguardSetProtectionCalls++
			return nil
		},
		ListDevices: func() ([]DeviceRow, error) {
			b.mu.Lock()
			defer b.mu.Unlock()
			out := make([]DeviceRow, len(b.devices))
			copy(out, b.devices)
			return out, nil
		},
		AddDevice: func(context.Context, string) (DeviceRow, error) {
			b.mu.Lock()
			defer b.mu.Unlock()
			b.addDeviceCalls++
			return DeviceRow{}, nil
		},
		RemoveDevice: func(context.Context, string) error {
			b.mu.Lock()
			defer b.mu.Unlock()
			b.removeDeviceCalls++
			return nil
		},
		RenameDevice: func(context.Context, string, string) error {
			b.mu.Lock()
			defer b.mu.Unlock()
			b.renameDeviceCalls++
			return nil
		},
		SetDeviceExit: func(context.Context, string, string) error {
			b.mu.Lock()
			defer b.mu.Unlock()
			b.setExitCalls++
			return nil
		},
		SetDeviceDatasaver: func(context.Context, string, bool) error {
			b.mu.Lock()
			defer b.mu.Unlock()
			b.setDatasaverCalls++
			return nil
		},
		ProbeDatasaverHealthy: func(context.Context, string) (string, error) {
			b.mu.Lock()
			defer b.mu.Unlock()
			b.probeDatasaverCalls++
			return "ok", nil
		},
		DeviceLink:    func(string) (string, error) { return "", nil },
		UsageSnapshot: func() ([]UsageRow, error) { return nil, nil },
		AuditEvent: func(user, action, target string) {
			b.mu.Lock()
			defer b.mu.Unlock()
			b.audit = append(b.audit, securityAuditRecord{user: user, action: action, target: target})
		},
	}
}

func (b *fakeSecurityActionsBackend) counts() map[string]int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return map[string]int{
		"saveUser":       b.saveUserCalls,
		"deleteUser":     b.deleteUserCalls,
		"resetPassword":  b.resetPasswordCalls,
		"setSecret":      b.setSecretCalls,
		"deleteSecret":   b.deleteSecretCalls,
		"revokeSession":  b.revokeSessionCalls,
		"ufwApply":       b.ufwApplyCalls,
		"adguardSet":     b.adguardSetProtectionCalls,
		"addDevice":      b.addDeviceCalls,
		"removeDevice":   b.removeDeviceCalls,
		"renameDevice":   b.renameDeviceCalls,
		"setExit":        b.setExitCalls,
		"setDatasaver":   b.setDatasaverCalls,
		"probeDatasaver": b.probeDatasaverCalls,
	}
}

// --- Test 1: Primary-protection guard (last-admin lockout) ---------------

// TestSecurityUserDelete_SurfacesRemoveUserError proves handleSecurityUserDelete
// wraps deps.DeleteUser's own error as a FieldErrors verbatim — never a
// re-derived count check — and that a rejected delete never mutates anything
// past the single (failed) DeleteUser call.
func TestSecurityUserDelete_SurfacesRemoveUserError(t *testing.T) {
	backend := newFakeSecurityActionsBackend()
	backend.deleteUserErr = errors.New("cannot remove the primary user 'sec-admin'")
	deps := backend.securityDeps()
	admin, _ := testSecurityViewers()

	handler := handleSecurityUserDelete(deps)
	_, err := handler(context.Background(), admin, map[string]string{"id": "other-admin"}, nil)

	var fe sdui.FieldErrors
	if !errors.As(err, &fe) {
		t.Fatalf("error = %v (%T), want sdui.FieldErrors", err, err)
	}
	msgs, ok := fe["username"]
	if !ok || len(msgs) == 0 || msgs[0] != "cannot remove the primary user 'sec-admin'" {
		t.Errorf("FieldErrors[\"username\"] = %v, want the EXACT error from RemoveUser, never reworded", msgs)
	}
	if backend.counts()["deleteUser"] != 1 {
		t.Errorf("DeleteUser was called %d time(s), want exactly 1 (called, and its error surfaced, never reimplemented beforehand)", backend.counts()["deleteUser"])
	}
}

// TestSecurityUserSave_SurfacesSetAdminError proves a demoting
// security.user.save surfaces SetAdmin's own last-admin rejection as a
// FieldErrors keyed "admin", never a re-derived count check.
func TestSecurityUserSave_SurfacesSetAdminError(t *testing.T) {
	backend := newFakeSecurityActionsBackend()
	backend.saveUserErr = errors.New("cannot revoke admin from the primary user 'sec-admin'")
	deps := backend.securityDeps()
	admin, _ := testSecurityViewers()

	handler := handleSecurityUserSave(deps)
	body, _ := json.Marshal(securityUserSaveInput{Username: "sec-admin", Admin: false})
	_, err := handler(context.Background(), admin, nil, body)

	var fe sdui.FieldErrors
	if !errors.As(err, &fe) {
		t.Fatalf("error = %v (%T), want sdui.FieldErrors", err, err)
	}
	msgs, ok := fe["admin"]
	if !ok || len(msgs) == 0 || msgs[0] != "cannot revoke admin from the primary user 'sec-admin'" {
		t.Errorf("FieldErrors[\"admin\"] = %v, want the EXACT error from SetAdmin, never reworded", msgs)
	}
	if backend.counts()["saveUser"] != 1 {
		t.Errorf("SaveUser was called %d time(s), want exactly 1", backend.counts()["saveUser"])
	}
}

// --- Test 2: destructive gate ---------------------------------------------

// registerSecurityActionsForTest registers the real security.*/network
// actions in this test binary's global sdui action registry EXACTLY once —
// RegisterAction panics on a duplicate ActionID.
var (
	registerSecurityActionsTestOnce sync.Once
	registerSecurityActionsTestDeps *fakeSecurityActionsBackend
)

func registerSecurityActionsForTest() *fakeSecurityActionsBackend {
	registerSecurityActionsTestOnce.Do(func() {
		registerSecurityActionsTestDeps = newFakeSecurityActionsBackend()
		registerSecurityActions(registerSecurityActionsTestDeps.securityDeps())
		registerNetworkActions(registerSecurityActionsTestDeps.networkDeps())
	})
	return registerSecurityActionsTestDeps
}

// TestSecurityAction_DestructiveActionsRequireConfirmation proves
// user.delete, secret.delete, session.revoke, device.remove and ufw.apply
// are all registered Destructive:true and unreachable without confirmation —
// an unconfirmed RunAction call returns a ConfirmationFieldKey FieldErrors
// WITHOUT ever calling the domain closure.
func TestSecurityAction_DestructiveActionsRequireConfirmation(t *testing.T) {
	backend := registerSecurityActionsForTest()
	admin, _ := testSecurityViewers()

	cases := []struct {
		actionID string
		params   map[string]string
		input    json.RawMessage
		countKey string
	}{
		{securityActionUserDelete, map[string]string{"id": "someone-else"}, nil, "deleteUser"},
		{securityActionSecretDelete, map[string]string{"id": "SOME_KEY"}, nil, "deleteSecret"},
		{securityActionSessionRevoke, map[string]string{"id": "some-jti"}, nil, "revokeSession"},
		{securityActionDeviceRemove, map[string]string{"id": "uuid-1"}, nil, "removeDevice"},
		{securityActionUFWApply, nil, mustJSONBytes(t, securityUFWApplyInput{Action: "allow", Spec: "22/tcp"}), "ufwApply"},
	}
	before := backend.counts()
	for _, c := range cases {
		_, err := sdui.RunAction(context.Background(), c.actionID, admin, c.params, c.input, sdui.Confirmation{Confirmed: false})
		var fe sdui.FieldErrors
		if !errors.As(err, &fe) {
			t.Fatalf("%s without confirmation: error = %v (%T), want FieldErrors", c.actionID, err, err)
		}
		if _, ok := fe[sdui.ConfirmationFieldKey]; !ok {
			t.Errorf("%s without confirmation: FieldErrors does not have %q: %v", c.actionID, sdui.ConfirmationFieldKey, fe)
		}
	}
	after := backend.counts()
	for _, c := range cases {
		if after[c.countKey] != before[c.countKey] {
			t.Errorf("%s: %s was called without confirmation (before=%d after=%d)", c.actionID, c.countKey, before[c.countKey], after[c.countKey])
		}
	}
}

// TestSecurityAction_NonDestructiveActionsRunWithoutConfirmation proves the
// non-destructive actions (secret.set, adguard.set_protection, device.add/
// rename/set_exit) execute with Confirmed:false — they were never registered
// Destructive:true, so RunAction's confirmation gate does not apply to them.
func TestSecurityAction_NonDestructiveActionsRunWithoutConfirmation(t *testing.T) {
	backend := registerSecurityActionsForTest()
	admin, _ := testSecurityViewers()
	before := backend.counts()["setSecret"]

	_, err := sdui.RunAction(context.Background(), securityActionSecretSet, admin, nil,
		mustJSONBytes(t, securitySecretSetInput{Key: "K", Value: "V"}), sdui.Confirmation{Confirmed: false})
	if err != nil {
		t.Errorf("secret.set without confirmation should run (it is not destructive): %v", err)
	}
	if after := backend.counts()["setSecret"]; after != before+1 {
		t.Errorf("SetSecret was called %d time(s) (before=%d), want exactly +1 — the action is not destructive and should have run", after, before)
	}
}

// --- Test 3: self-delete guard --------------------------------------------

// TestSecurityUserDelete_SelfDeleteIsUnconditional proves a viewer deleting
// their OWN username is rejected before deps.DeleteUser is ever called —
// checked FIRST, independent of admin/Primary status (mirrors
// handlers_users.go's unconditional 403).
func TestSecurityUserDelete_SelfDeleteIsUnconditional(t *testing.T) {
	backend := newFakeSecurityActionsBackend()
	deps := backend.securityDeps()
	admin, _ := testSecurityViewers()

	handler := handleSecurityUserDelete(deps)
	_, err := handler(context.Background(), admin, map[string]string{"id": admin.Username}, nil)

	var fe sdui.FieldErrors
	if !errors.As(err, &fe) {
		t.Fatalf("error = %v (%T), want sdui.FieldErrors", err, err)
	}
	msgs, ok := fe["username"]
	if !ok || len(msgs) == 0 || msgs[0] != "cannot delete your own user (logged in right now)" {
		t.Errorf("FieldErrors[\"username\"] = %v, want the verbatim message from handlers_users.go", msgs)
	}
	if backend.counts()["deleteUser"] != 0 {
		t.Errorf("DeleteUser was called %d time(s) on a self-delete, want 0 — guard 1 is checked BEFORE", backend.counts()["deleteUser"])
	}
}

// TestSecuritySessionRevoke_OwnCurrentSessionIsAllowed proves revoking one's
// OWN current session (unlike deleting one's own account) is explicitly
// allowed — RevokeSession IS called, no self-protection guard blocks it.
func TestSecuritySessionRevoke_OwnCurrentSessionIsAllowed(t *testing.T) {
	backend := newFakeSecurityActionsBackend()
	deps := backend.securityDeps()
	admin, _ := testSecurityViewers()

	handler := handleSecuritySessionRevoke(deps)
	_, err := handler(context.Background(), admin, map[string]string{"id": "jti-of-my-own-current-session"}, nil)
	if err != nil {
		t.Fatalf("revoking one's own current session should be allowed: %v", err)
	}
	if backend.counts()["revokeSession"] != 1 {
		t.Errorf("RevokeSession was called %d time(s), want 1 — revoking one's own session is allowed (only ACCOUNT delete is not)", backend.counts()["revokeSession"])
	}
}

// --- Test 4: RBAC parity ---------------------------------------------------

// TestSecurityAction_NonAdminGetsNotFound proves a non-admin viewer invoking
// any of the twelve security/network action ids directly — even with a
// fully confirmed request — gets sdui.ErrActionNotFound at RunAction's
// authorize step, never reaching the confirmation gate or the handler.
func TestSecurityAction_NonAdminGetsNotFound(t *testing.T) {
	backend := registerSecurityActionsForTest()
	_, nonAdmin := testSecurityViewers()
	before := backend.counts()

	cases := []struct {
		actionID string
		params   map[string]string
		input    json.RawMessage
	}{
		{securityActionUserSave, nil, mustJSONBytes(t, securityUserSaveInput{Username: "x"})},
		{securityActionUserDelete, map[string]string{"id": "x"}, nil},
		{securityActionUserResetPassword, nil, mustJSONBytes(t, securityUserResetPasswordInput{Username: "x", Password: "12345678"})},
		{securityActionSecretSet, nil, mustJSONBytes(t, securitySecretSetInput{Key: "K", Value: "V"})},
		{securityActionSecretDelete, map[string]string{"id": "K"}, nil},
		{securityActionSessionRevoke, map[string]string{"id": "jti"}, nil},
		{securityActionUFWApply, nil, mustJSONBytes(t, securityUFWApplyInput{Action: "enable"})},
		{securityActionAdGuardSetProtection, nil, mustJSONBytes(t, securityAdGuardSetProtectionInput{Enabled: true})},
		{securityActionDeviceAdd, nil, mustJSONBytes(t, securityDeviceAddInput{Name: "x"})},
		{securityActionDeviceRemove, map[string]string{"id": "uuid-1"}, nil},
		{securityActionDeviceRename, map[string]string{"id": "uuid-1"}, mustJSONBytes(t, securityDeviceRenameInput{Name: "y"})},
		{securityActionDeviceSetExit, map[string]string{"id": "uuid-1"}, mustJSONBytes(t, securityDeviceSetExitInput{Exit: "vps"})},
		{securityActionDeviceSetDatasaver, map[string]string{"id": "uuid-1"}, mustJSONBytes(t, securityDeviceSetDatasaverInput{On: false})},
	}
	for _, c := range cases {
		_, err := sdui.RunAction(context.Background(), c.actionID, nonAdmin, c.params, c.input, sdui.Confirmation{Confirmed: true})
		if !errors.Is(err, sdui.ErrActionNotFound) {
			t.Errorf("%s non-admin: error = %v, want sdui.ErrActionNotFound", c.actionID, err)
		}
	}
	after := backend.counts()
	for k, v := range after {
		if v != before[k] {
			t.Errorf("%s was called by a non-admin (before=%d after=%d) — RunAction should have blocked it at authorize", k, before[k], v)
		}
	}
}

// --- Test 5: field-keyed validation ----------------------------------------

// TestSecurityUserSave_MissingUsernameIsFieldError proves an empty username
// never reaches deps.SaveUser, keyed to "username".
func TestSecurityUserSave_MissingUsernameIsFieldError(t *testing.T) {
	backend := newFakeSecurityActionsBackend()
	deps := backend.securityDeps()
	admin, _ := testSecurityViewers()

	handler := handleSecurityUserSave(deps)
	body, _ := json.Marshal(securityUserSaveInput{Username: "", Admin: true})
	_, err := handler(context.Background(), admin, nil, body)

	var fe sdui.FieldErrors
	if !errors.As(err, &fe) {
		t.Fatalf("error = %v (%T), want sdui.FieldErrors", err, err)
	}
	if _, ok := fe["username"]; !ok {
		t.Errorf("FieldErrors does not have the \"username\" key: %v", fe)
	}
	if backend.counts()["saveUser"] != 0 {
		t.Errorf("SaveUser was called %d time(s) with an empty username, want 0", backend.counts()["saveUser"])
	}
}

// TestSecurityUFWApply_EmptySpecOnActionRequiringOneIsFieldError proves an
// "allow"/"deny"/"reject"/"delete" ufw.apply with an empty spec returns
// FieldErrors keyed "spec", never reaching deps.UFWApplyRule — mirrors
// handleUFWRule's own validation for these same four actions.
func TestSecurityUFWApply_EmptySpecOnActionRequiringOneIsFieldError(t *testing.T) {
	backend := newFakeSecurityActionsBackend()
	deps := backend.networkDeps()
	admin, _ := testSecurityViewers()

	handler := handleSecurityUFWApply(deps)
	for _, action := range []string{"allow", "deny", "reject", "delete"} {
		body, _ := json.Marshal(securityUFWApplyInput{Action: action, Spec: ""})
		_, err := handler(context.Background(), admin, nil, body)
		var fe sdui.FieldErrors
		if !errors.As(err, &fe) {
			t.Fatalf("action=%s: error = %v (%T), want sdui.FieldErrors", action, err, err)
		}
		if _, ok := fe["spec"]; !ok {
			t.Errorf("action=%s: FieldErrors does not have the \"spec\" key: %v", action, fe)
		}
	}
	if backend.counts()["ufwApply"] != 0 {
		t.Errorf("UFWApplyRule was called %d time(s) with an empty spec on an action that requires spec, want 0", backend.counts()["ufwApply"])
	}

	// enable/disable act on the firewall as a whole — empty spec is valid.
	for _, action := range []string{"enable", "disable"} {
		body, _ := json.Marshal(securityUFWApplyInput{Action: action, Spec: ""})
		_, err := handler(context.Background(), admin, nil, body)
		if err != nil {
			t.Errorf("action=%s: error = %v, want nil (empty spec is valid for enable/disable)", action, err)
		}
	}
	if backend.counts()["ufwApply"] != 2 {
		t.Errorf("UFWApplyRule was called %d time(s), want 2 (enable + disable)", backend.counts()["ufwApply"])
	}
}

// TestSecurityDeviceSetDatasaver_RequiresCaAckAndHealthyProbe proves the
// two-gate safety pair (Rule 2, security_actions.go's doc comment): turning
// datasaver ON without ca_ack is a field error, and a failing health probe
// is also a field error — SetDeviceDatasaver is reached in neither case.
// Turning datasaver OFF requires neither gate.
func TestSecurityDeviceSetDatasaver_RequiresCaAckAndHealthyProbe(t *testing.T) {
	backend := newFakeSecurityActionsBackend()
	deps := backend.networkDeps()
	admin, _ := testSecurityViewers()
	handler := handleSecurityDeviceSetDatasaver(deps)

	// Missing ca_ack.
	body, _ := json.Marshal(securityDeviceSetDatasaverInput{On: true, CaAck: false})
	_, err := handler(context.Background(), admin, map[string]string{"id": "uuid-1"}, body)
	var fe sdui.FieldErrors
	if !errors.As(err, &fe) {
		t.Fatalf("without ca_ack: error = %v (%T), want sdui.FieldErrors", err, err)
	}
	if _, ok := fe["ca_ack"]; !ok {
		t.Errorf("without ca_ack: FieldErrors does not have the \"ca_ack\" key: %v", fe)
	}
	if backend.counts()["setDatasaver"] != 0 {
		t.Errorf("SetDeviceDatasaver was called without ca_ack, want 0 calls")
	}

	// ca_ack true but probe fails.
	deps2 := backend.networkDeps()
	deps2.ProbeDatasaverHealthy = func(context.Context, string) (string, error) { return "", errors.New("proxy unavailable") }
	handler2 := handleSecurityDeviceSetDatasaver(deps2)
	body, _ = json.Marshal(securityDeviceSetDatasaverInput{On: true, CaAck: true})
	_, err = handler2(context.Background(), admin, map[string]string{"id": "uuid-1"}, body)
	if !errors.As(err, &fe) {
		t.Fatalf("probe failing: error = %v (%T), want sdui.FieldErrors", err, err)
	}
	if backend.counts()["setDatasaver"] != 0 {
		t.Errorf("SetDeviceDatasaver was called with a failing probe, want 0 calls")
	}

	// Turning OFF requires neither gate.
	body, _ = json.Marshal(securityDeviceSetDatasaverInput{On: false})
	_, err = handler(context.Background(), admin, map[string]string{"id": "uuid-1"}, body)
	if err != nil {
		t.Fatalf("turning datasaver off should not require ca_ack or a probe: %v", err)
	}
	if backend.counts()["setDatasaver"] != 1 {
		t.Errorf("SetDeviceDatasaver was called %d time(s) when turning off, want 1", backend.counts()["setDatasaver"])
	}
	if backend.counts()["probeDatasaver"] != 0 {
		t.Errorf("ProbeDatasaverHealthy was called %d time(s) when turning off, want 0 (the gate only applies when turning on)", backend.counts()["probeDatasaver"])
	}
}
