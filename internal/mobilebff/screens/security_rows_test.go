package screens

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"server-control-panel/internal/adguard"
	"server-control-panel/internal/auth"
	"server-control-panel/internal/mobilebff"
)

// fakeSecurityRowsDeps builds a SecurityDeps with only the List* fields
// populated — mutating closures deliberately left nil, mirroring
// fakeDockerRowsDeps's rationale (docker_rows_test.go): the rows endpoints
// never call a mutation closure, so a stray call panics loudly.
func fakeSecurityRowsDeps() SecurityDeps {
	return SecurityDeps{
		ListUsers: func() []UserRow {
			return []UserRow{
				{Username: "sec-admin", IsPrimary: true, IsAdmin: true, HasTOTP: true, Sessions: 2},
				{Username: "sec-user", IsPrimary: false, IsAdmin: false, HasTOTP: false, Sessions: 0},
			}
		},
		ListSecretKeys: func() []SecretKeyRow {
			return []SecretKeyRow{{Key: "SUPABASE_URL"}, {Key: "GMAIL_TOKEN"}}
		},
		ListSessions: func() []SessionRow {
			return []SessionRow{
				{ID: "jti-current", User: "sec-admin", IP: "10.0.0.1", UserAgent: "curl/8.0", IssuedAt: 1798000000, LastSeen: 1798000500, ExpiresAt: 1798100000},
				{ID: "jti-other", User: "sec-user", IP: "10.0.0.2", UserAgent: "okhttp/4.0", IssuedAt: 1798000100, LastSeen: 1798000600, ExpiresAt: 1798100100},
			}
		},
		ListAuditEvents: func(AuditFilter) ([]AuditRow, error) {
			return []AuditRow{
				{Time: 1798000000, User: "sec-admin", Action: "user.create", Target: "sec-user", IP: "10.0.0.1"},
			}, nil
		},
	}
}

// fakeNetworkRowsDeps builds a NetworkDeps with only the read closures
// populated.
func fakeNetworkRowsDeps() NetworkDeps {
	return NetworkDeps{
		UFWStatus: func() (bool, string, error) {
			return true, "Status: active\n\nTo  Action  From\n22/tcp  ALLOW  Anywhere", nil
		},
		AdGuardStatus: func(context.Context) (*adguard.Status, error) {
			return &adguard.Status{
				ProtectionEnabled: true,
				Running:           true,
				Version:           "v0.107.5",
				NumQueries:        5000,
				NumBlocked:        750,
				BlockedPct:        15.0,
			}, nil
		},
		ListDevices: func() ([]DeviceRow, error) {
			return []DeviceRow{
				{Name: "phone-1", UUID: "uuid-1", Exit: "vps", Datasaver: false, Created: 1798000000},
			}, nil
		},
		UsageSnapshot: func() ([]UsageRow, error) {
			return []UsageRow{
				{Name: "phone-1", Port: 40001, TotalBytes: 2147483648, RateBps: 2048.0, ActiveConns: 3},
			}, nil
		},
	}
}

// newSecurityRowsMux wires the eight registerSecurityX rows/detail functions
// onto a throwaway huma.API — mirrors newDockerRowsMux's rationale exactly:
// RegisterSecurity/RegisterNetwork go through the process-global sdui
// registries (which panic on double-registration), so tests exercise the
// rows/detail plumbing directly.
func newSecurityRowsMux(secDeps SecurityDeps, netDeps NetworkDeps) *http.ServeMux {
	mux := http.NewServeMux()
	api := humago.NewWithPrefix(mux, mobilebff.Prefix, huma.DefaultConfig("security-rows-test", "0"))
	mbDeps := mobilebff.Deps{Cfg: testSecurityCfg()}
	registerSecurityUsersRows(api, secDeps, mbDeps)
	registerSecuritySecretsRows(api, secDeps, mbDeps)
	registerSecuritySessionsRows(api, secDeps, mbDeps)
	registerSecurityAuditRows(api, secDeps, mbDeps)
	registerSecurityUFWDetail(api, netDeps, mbDeps)
	registerSecurityAdGuardDetail(api, netDeps, mbDeps)
	registerSecurityDevicesRows(api, netDeps, mbDeps)
	registerSecurityDataSaverRows(api, netDeps, mbDeps)
	return mux
}

type securityRowsBody struct {
	Rows []map[string]any `json:"rows"`
}

type securityDetailBody struct {
	Detail map[string]any `json:"detail"`
}

func doSecurityRowsRequest(t *testing.T, mux *http.ServeMux, path, username string) (*httptest.ResponseRecorder, securityRowsBody) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, mobilebff.Prefix+path, nil)
	if username != "" {
		req = req.WithContext(auth.WithUser(req.Context(), username))
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	var body securityRowsBody
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s: decode: %v (body=%s)", path, err, rec.Body.String())
		}
	}
	return rec, body
}

func doSecurityDetailRequest(t *testing.T, mux *http.ServeMux, path, username string) (*httptest.ResponseRecorder, securityDetailBody) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, mobilebff.Prefix+path, nil)
	if username != "" {
		req = req.WithContext(auth.WithUser(req.Context(), username))
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	var body securityDetailBody
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s: decode: %v (body=%s)", path, err, rec.Body.String())
		}
	}
	return rec, body
}

var securityAllRowsPaths = []string{
	"/security/users", "/security/secrets", "/security/sessions", "/security/audit",
	"/security/devices", "/security/economia",
}

var securityAllDetailPaths = []string{
	"/security/ufw/status", "/security/adguard/status",
}

// TestSecurityRows_Unauthenticated proves every one of the eight
// rows/detail endpoints demands authentication BEFORE the admin check ever
// runs — mobilebff.RequireAuth is the first middleware in every
// registerSecurityX call, ahead of serveSecurityRows/serveSecurityDetail's
// own !v.IsAdmin() gate.
func TestSecurityRows_Unauthenticated(t *testing.T) {
	mux := newSecurityRowsMux(fakeSecurityRowsDeps(), fakeNetworkRowsDeps())
	for _, path := range securityAllRowsPaths {
		rec, _ := doSecurityRowsRequest(t, mux, path, "")
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: status = %d, want 401 (body=%s)", path, rec.Code, rec.Body.String())
		}
	}
	for _, path := range securityAllDetailPaths {
		rec, _ := doSecurityDetailRequest(t, mux, path, "")
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: status = %d, want 401 (body=%s)", path, rec.Code, rec.Body.String())
		}
	}
}

// TestSecurityRows_NonAdminGetsNotFound proves an AUTHENTICATED non-admin
// caller gets 404 (never the row/detail data, never a 403) on every one of
// the eight rows/detail endpoints — the admin-gating this package adds at
// the HTTP layer itself (beyond docker.go's precedent, since docker's rows
// are not whole-screen admin-gated) is what this test pins.
func TestSecurityRows_NonAdminGetsNotFound(t *testing.T) {
	mux := newSecurityRowsMux(fakeSecurityRowsDeps(), fakeNetworkRowsDeps())
	for _, path := range securityAllRowsPaths {
		rec, _ := doSecurityRowsRequest(t, mux, path, "sec-user")
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: non-admin status = %d, want 404 (body=%s)", path, rec.Code, rec.Body.String())
		}
	}
	for _, path := range securityAllDetailPaths {
		rec, _ := doSecurityDetailRequest(t, mux, path, "sec-user")
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: non-admin status = %d, want 404 (body=%s)", path, rec.Code, rec.Body.String())
		}
	}
}

// TestSecurityUsersRows_WireShape pins
// {"rows":[{"id","username","role","is_primary","has_totp","sessions"}]}.
func TestSecurityUsersRows_WireShape(t *testing.T) {
	mux := newSecurityRowsMux(fakeSecurityRowsDeps(), fakeNetworkRowsDeps())
	rec, body := doSecurityRowsRequest(t, mux, "/security/users", "sec-admin")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if len(body.Rows) != 2 {
		t.Fatalf("rows = %v, want 2 rows", body.Rows)
	}
	admin := body.Rows[0]
	wantKeys := []string{"id", "username", "role", "is_primary", "has_totp", "sessions"}
	for _, k := range wantKeys {
		if _, ok := admin[k]; !ok {
			t.Errorf("row does not have the %q key: %v", k, admin)
		}
	}
	if admin["role"] != "admin" {
		t.Errorf("role = %v, want \"admin\"", admin["role"])
	}
	if admin["has_totp"] != "yes" {
		t.Errorf("has_totp = %v, want \"yes\"", admin["has_totp"])
	}
	if _, isString := admin["sessions"].(string); !isString {
		t.Errorf("sessions = %v (%T), want a pre-formatted string", admin["sessions"], admin["sessions"])
	}
	// A second admin/user in the table proves multi-admin visibility: the
	// table is never filtered down to one row.
	nonAdminRow := body.Rows[1]
	if nonAdminRow["role"] != "user" {
		t.Errorf("second row role = %v, want \"user\"", nonAdminRow["role"])
	}
}

// TestSecuritySecretsRows_WireShape pins {"rows":[{"id","key"}]} and proves
// no value-shaped field ever appears.
func TestSecuritySecretsRows_WireShape(t *testing.T) {
	mux := newSecurityRowsMux(fakeSecurityRowsDeps(), fakeNetworkRowsDeps())
	rec, body := doSecurityRowsRequest(t, mux, "/security/secrets", "sec-admin")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if len(body.Rows) != 2 {
		t.Fatalf("rows = %v, want 2 rows", body.Rows)
	}
	row := body.Rows[0]
	if _, ok := row["key"]; !ok {
		t.Errorf("row does not have the %q key: %v", "key", row)
	}
	if _, ok := row["value"]; ok {
		t.Fatalf("secrets row exposes \"value\" on the wire, which should never happen: %v", row)
	}
	raw, _ := json.Marshal(body)
	if len(raw) == 0 {
		t.Fatalf("empty marshal")
	}
}

// TestSecuritySessionsRows_WireShape pins
// {"rows":[{"id","user","ip","user_agent","issued_at","last_seen","is_current"}]}
// over a real HTTP round trip (auth.WithUser only — auth.JTIFrom's context
// key is unexported outside internal/auth, so this request carries no JTI
// and both rows correctly render is_current="no"; the CALLER's-own-session
// flagging logic itself is pinned separately by
// TestSecuritySessionRow_FlagsCallersOwnJTI below, calling securitySessionRow
// directly with an explicit currentJTI).
func TestSecuritySessionsRows_WireShape(t *testing.T) {
	mux := newSecurityRowsMux(fakeSecurityRowsDeps(), fakeNetworkRowsDeps())
	rec, body := doSecurityRowsRequest(t, mux, "/security/sessions", "sec-admin")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if len(body.Rows) != 2 {
		t.Fatalf("rows = %v, want 2 rows", body.Rows)
	}
	wantKeys := []string{"id", "user", "ip", "user_agent", "issued_at", "last_seen", "is_current"}
	for _, row := range body.Rows {
		for _, k := range wantKeys {
			if _, ok := row[k]; !ok {
				t.Errorf("row does not have the %q key: %v", k, row)
			}
		}
	}
	for _, row := range body.Rows {
		if row["is_current"] != "no" {
			t.Errorf("with no JTI in the test context, is_current should be \"no\" for every row: %v", row)
		}
	}
}

// TestSecuritySessionRow_FlagsCallersOwnJTI proves securitySessionRow (the
// pure row-shaping function serveSecuritySessionsRows calls per row) flags
// exactly the row whose ID matches the caller's currentJTI, and only that
// one — the actual JTI-threading this screen relies on for "you are about
// to revoke your OWN session" (see SessionRow.IsCurrent's doc comment,
// deps.go).
func TestSecuritySessionRow_FlagsCallersOwnJTI(t *testing.T) {
	own := SessionRow{ID: "jti-current", User: "sec-admin", IssuedAt: 1798000000, LastSeen: 1798000500}
	other := SessionRow{ID: "jti-other", User: "sec-user", IssuedAt: 1798000100, LastSeen: 1798000600}

	if got := securitySessionRow(own, "jti-current")["is_current"]; got != "yes" {
		t.Errorf("the caller's own session: is_current = %v, want \"yes\"", got)
	}
	if got := securitySessionRow(other, "jti-current")["is_current"]; got != "no" {
		t.Errorf("another user's session: is_current = %v, want \"no\"", got)
	}
	if got := securitySessionRow(own, "")["is_current"]; got != "no" {
		t.Errorf("with no caller JTI (currentJTI empty): is_current = %v, want \"no\" (never flag by mistake)", got)
	}
}

// TestSecurityAuditRows_WireShape pins
// {"rows":[{"id","time","user","action","target","ip"}]}.
func TestSecurityAuditRows_WireShape(t *testing.T) {
	mux := newSecurityRowsMux(fakeSecurityRowsDeps(), fakeNetworkRowsDeps())
	rec, body := doSecurityRowsRequest(t, mux, "/security/audit", "sec-admin")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if len(body.Rows) != 1 {
		t.Fatalf("rows = %v, want 1 row", body.Rows)
	}
	row := body.Rows[0]
	wantKeys := []string{"id", "time", "user", "action", "target", "ip"}
	for _, k := range wantKeys {
		if _, ok := row[k]; !ok {
			t.Errorf("row does not have the %q key: %v", k, row)
		}
	}
	if row["action"] != "user.create" || row["target"] != "sec-user" {
		t.Errorf("row = %v, want action=user.create target=sec-user", row)
	}
}

// TestSecurityUFWDetail_WireShape pins {"detail":{"enabled","output"}} — the
// FIRST production round-trip test of DetailComponent's wire shape.
func TestSecurityUFWDetail_WireShape(t *testing.T) {
	mux := newSecurityRowsMux(fakeSecurityRowsDeps(), fakeNetworkRowsDeps())
	rec, body := doSecurityDetailRequest(t, mux, "/security/ufw/status", "sec-admin")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if body.Detail["enabled"] != "Active" {
		t.Errorf("enabled = %v, want \"Active\"", body.Detail["enabled"])
	}
	if _, ok := body.Detail["output"]; !ok {
		t.Errorf("detail does not have \"output\": %v", body.Detail)
	}
}

// TestSecurityAdGuardDetail_WireShape pins
// {"detail":{"protection_enabled","running","version","num_queries","num_blocked","blocked_pct"}}.
func TestSecurityAdGuardDetail_WireShape(t *testing.T) {
	mux := newSecurityRowsMux(fakeSecurityRowsDeps(), fakeNetworkRowsDeps())
	rec, body := doSecurityDetailRequest(t, mux, "/security/adguard/status", "sec-admin")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	wantKeys := []string{"protection_enabled", "running", "version", "num_queries", "num_blocked", "blocked_pct"}
	for _, k := range wantKeys {
		if _, ok := body.Detail[k]; !ok {
			t.Errorf("detail does not have the %q key: %v", k, body.Detail)
		}
	}
	if body.Detail["protection_enabled"] != "Active" {
		t.Errorf("protection_enabled = %v, want \"Active\"", body.Detail["protection_enabled"])
	}
	if body.Detail["version"] != "v0.107.5" {
		t.Errorf("version = %v, want \"v0.107.5\"", body.Detail["version"])
	}
	if body.Detail["blocked_pct"] != "15.0%" {
		t.Errorf("blocked_pct = %v, want \"15.0%%\"", body.Detail["blocked_pct"])
	}
}

// TestSecurityDevicesRows_WireShape pins
// {"rows":[{"id","name","uuid","exit","datasaver","created"}]}.
func TestSecurityDevicesRows_WireShape(t *testing.T) {
	mux := newSecurityRowsMux(fakeSecurityRowsDeps(), fakeNetworkRowsDeps())
	rec, body := doSecurityRowsRequest(t, mux, "/security/devices", "sec-admin")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if len(body.Rows) != 1 {
		t.Fatalf("rows = %v, want 1 row", body.Rows)
	}
	row := body.Rows[0]
	wantKeys := []string{"id", "name", "uuid", "exit", "datasaver", "created"}
	for _, k := range wantKeys {
		if _, ok := row[k]; !ok {
			t.Errorf("row does not have the %q key: %v", k, row)
		}
	}
	if row["exit"] != "vps" || row["datasaver"] != "no" {
		t.Errorf("row = %v, want exit=vps datasaver=no", row)
	}
}

// TestSecurityDataSaverRows_WireShape pins
// {"rows":[{"id","name","port","total_bytes","rate_bps","active_conns"}]},
// proving bytes/rate are pre-formatted strings, never raw numbers.
func TestSecurityDataSaverRows_WireShape(t *testing.T) {
	mux := newSecurityRowsMux(fakeSecurityRowsDeps(), fakeNetworkRowsDeps())
	rec, body := doSecurityRowsRequest(t, mux, "/security/economia", "sec-admin")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if len(body.Rows) != 1 {
		t.Fatalf("rows = %v, want 1 row", body.Rows)
	}
	row := body.Rows[0]
	wantKeys := []string{"id", "name", "port", "total_bytes", "rate_bps", "active_conns"}
	for _, k := range wantKeys {
		if _, ok := row[k]; !ok {
			t.Errorf("row does not have the %q key: %v", k, row)
		}
	}
	if _, isString := row["total_bytes"].(string); !isString {
		t.Errorf("total_bytes = %v (%T), want a pre-formatted string", row["total_bytes"], row["total_bytes"])
	}
	if _, isString := row["rate_bps"].(string); !isString {
		t.Errorf("rate_bps = %v (%T), want a pre-formatted string", row["rate_bps"], row["rate_bps"])
	}
}

// TestSecurityRows_UnavailableDoesNotBecomeEmptyTable pins the distinction this
// error channel exists to make: "nothing happened" and "it could not be read"
// are opposite answers for someone looking at a security screen, and both
// arrived as the same empty list — to the point where the empty-state copy
// had to admit the ambiguity in writing.
func TestSecurityRows_UnavailableDoesNotBecomeEmptyTable(t *testing.T) {
	t.Run("audit down doesn't answer 200", func(t *testing.T) {
		sec := fakeSecurityRowsDeps()
		sec.ListAuditEvents = func(AuditFilter) ([]AuditRow, error) {
			return nil, errors.New("audit unavailable")
		}
		mux := newSecurityRowsMux(sec, fakeNetworkRowsDeps())
		rec, _ := doSecurityRowsRequest(t, mux, "/security/audit", "sec-admin")
		if rec.Code == http.StatusOK {
			t.Fatalf("200 with audit down — the screen would say 'nothing happened' (body=%s)", rec.Body.String())
		}
	})

	t.Run("measurement down doesn't answer 200", func(t *testing.T) {
		net := fakeNetworkRowsDeps()
		net.UsageSnapshot = func() ([]UsageRow, error) {
			return nil, errors.New("measurement unavailable")
		}
		mux := newSecurityRowsMux(fakeSecurityRowsDeps(), net)
		rec, _ := doSecurityRowsRequest(t, mux, "/security/economia", "sec-admin")
		if rec.Code == http.StatusOK {
			t.Fatalf("200 with the collector down — the screen would say 'no traffic' (body=%s)", rec.Body.String())
		}
	})
}
