package sdui_test

import (
	"context"

	"server-control-panel/internal/adguard"
	"server-control-panel/internal/mobilebff/screens"
)

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

func init() {
	backend := securityGoldenBackend{}
	screens.RegisterSecurity(backend.securityDeps())
	screens.RegisterNetwork(backend.networkDeps())
}
