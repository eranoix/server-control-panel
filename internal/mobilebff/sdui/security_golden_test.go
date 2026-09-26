// Package sdui_test wires the eight Security/Network screens into the sdui
// package's own golden-fixture test binary — same rationale as
// docker_golden_test.go's header comment: golden_test.go's harness only sees
// screens registered inside the SAME test binary process, and being an
// external test package (sdui_test) is what lets this file import
// internal/mobilebff/screens without an import cycle.
package sdui_test

import (
	"context"

	"server-control-panel/internal/adguard"
	"server-control-panel/internal/mobilebff/screens"
)

// securityGoldenBackend is a small in-memory stand-in for internal/api.Router's
// real config/secrets/sessions/audit/UFW/AdGuard/singbox/netusage wiring —
// enough to build all eight Security/Network screens and their rows/detail
// endpoints deterministically for the golden corpus. It never touches real
// config, secrets, sessions or an external daemon; the golden harness only
// calls Build (via the sdui.Screen builder), never RunAction, so the
// mutating closures below are unreachable from the harness and exist only
// to satisfy SecurityDeps/NetworkDeps' shape.
type securityGoldenBackend struct{}

func (securityGoldenBackend) securityDeps() screens.SecurityDeps {
	return screens.SecurityDeps{
		ListUsers: func() []screens.UserRow {
			return []screens.UserRow{
				{Username: "golden-admin", IsPrimary: true, IsAdmin: true, HasTOTP: true, Sessions: 1},
				{Username: "golden-user", IsPrimary: false, IsAdmin: false, HasTOTP: false, Sessions: 0},
			}
		},
		SaveUser: func(in screens.UserInput) (*screens.UserRow, error) {
			return &screens.UserRow{Username: in.Username, IsAdmin: in.Admin}, nil
		},
		DeleteUser:    func(string) error { return nil },
		ResetPassword: func(string, string) error { return nil },

		ListSecretKeys: func() []screens.SecretKeyRow {
			return []screens.SecretKeyRow{{Key: "GOLDEN_SECRET_KEY"}}
		},
		SetSecret:    func(string, string) error { return nil },
		DeleteSecret: func(string) error { return nil },

		ListSessions: func() []screens.SessionRow {
			return []screens.SessionRow{
				{ID: "golden-jti-1", User: "golden-admin", IP: "127.0.0.1", UserAgent: "golden-agent", IssuedAt: 1798000000, LastSeen: 1798000100, ExpiresAt: 1798100000, IsCurrent: true},
			}
		},
		RevokeSession: func(string) error { return nil },

		ListAuditEvents: func(screens.AuditFilter) ([]screens.AuditRow, error) {
			return []screens.AuditRow{
				{Time: 1798000000, User: "golden-admin", Action: "user.create", Target: "golden-user", IP: "127.0.0.1"},
			}, nil
		},
		AuditEvent: func(_, _, _ string) {},
	}
}

func (securityGoldenBackend) networkDeps() screens.NetworkDeps {
	return screens.NetworkDeps{
		UFWStatus: func() (bool, string, error) {
			return true, "Status: active\n\nTo  Action  From\n22/tcp  ALLOW  Anywhere", nil
		},
		UFWApplyRule: func(string, string) (string, error) { return "Rule added", nil },

		AdGuardStatus: func(context.Context) (*adguard.Status, error) {
			return &adguard.Status{
				ProtectionEnabled: true,
				Running:           true,
				Version:           "v0.107.0",
				NumQueries:        1000,
				NumBlocked:        100,
				BlockedPct:        10.0,
			}, nil
		},
		AdGuardSetProtection: func(context.Context, bool, int) error { return nil },

		ListDevices: func() ([]screens.DeviceRow, error) {
			return []screens.DeviceRow{
				{Name: "golden-phone", UUID: "golden-uuid-1", Exit: "vps", Datasaver: false, Created: 1798000000},
			}, nil
		},
		AddDevice:             func(context.Context, string) (screens.DeviceRow, error) { return screens.DeviceRow{}, nil },
		RemoveDevice:          func(context.Context, string) error { return nil },
		SetDeviceExit:         func(context.Context, string, string) error { return nil },
		SetDeviceDatasaver:    func(context.Context, string, bool) error { return nil },
		ProbeDatasaverHealthy: func(context.Context, string) (string, error) { return "ok", nil },
		RenameDevice:          func(context.Context, string, string) error { return nil },
		DeviceLink:            func(string) (string, error) { return "vless://golden", nil },

		UsageSnapshot: func() ([]screens.UsageRow, error) {
			return []screens.UsageRow{
				{Name: "golden-phone", Port: 40001, TotalBytes: 1073741824, RateBps: 1024.0, ActiveConns: 2},
			}, nil
		},
		AuditEvent: func(_, _, _ string) {},
	}
}

// init registers all eight Security/Network screens into this test binary's
// process-global sdui registries exactly once — the same
// RegisterSecurity(deps)/RegisterNetwork(deps) internal/api/api.go calls in
// production, fed synthetic data instead of real config/secrets/sessions/
// audit/UFW/AdGuard/singbox/netusage. This is what makes RegisteredScreens()
// (used by TestGoldenScreens and its two role-omission checks) see
// security.users, security.secrets, security.sessions, security.audit,
// security.ufw, security.adguard, security.devices and security.economia at
// all when running `go test ./internal/mobilebff/sdui/...`.
func init() {
	backend := securityGoldenBackend{}
	screens.RegisterSecurity(backend.securityDeps())
	screens.RegisterNetwork(backend.networkDeps())
}
