package mobilebff

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/config"
)

// fakeUserEmailMapper simulates authSvc.UUIDMap() without going through
// LoadUUIDMap when the test does not need a real mapping.
type fakeUserEmailMapper struct {
	uuidMap *auth.UUIDMap
}

func (f *fakeUserEmailMapper) UUIDMap() *auth.UUIDMap { return f.uuidMap }

func newTestUUIDMap(t *testing.T, username, email string) *auth.UUIDMap {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "migration-uuid-map.json")
	content := `{"schema_version":1,"mappings":{"` + username + `":{"email":"` + email + `","supabase_uuid":"00000000-0000-0000-0000-000000000000"}}}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing the uuid map fixture: %v", err)
	}
	um, err := auth.LoadUUIDMap(path)
	if err != nil {
		t.Fatalf("LoadUUIDMap: %v", err)
	}
	return um
}

func TestHandleMe_Authenticated(t *testing.T) {
	um := newTestUUIDMap(t, "sam", "sam@northwind.example")
	mux := http.NewServeMux()
	Mount(mux, Deps{Auth: &fakeUserEmailMapper{uuidMap: um}})

	req := httptest.NewRequest(http.MethodGet, "/api/mobile/v1/me", nil)
	req = req.WithContext(auth.WithUser(req.Context(), "sam"))
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body MeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v (body=%s)", err, rec.Body.String())
	}
	if body.User != "sam" {
		t.Errorf("user = %q, want %q", body.User, "sam")
	}
	if body.Email != "sam@northwind.example" {
		t.Errorf("email = %q, want %q", body.Email, "sam@northwind.example")
	}
	if body.ServerTime <= 0 {
		t.Errorf("server_time = %d, want > 0", body.ServerTime)
	}
}

func TestHandleMe_Authenticated_NoUUIDMap_OmitsEmail(t *testing.T) {
	mux := http.NewServeMux()
	Mount(mux, Deps{})

	req := httptest.NewRequest(http.MethodGet, "/api/mobile/v1/me", nil)
	req = req.WithContext(auth.WithUser(req.Context(), "sam"))
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"email"`) {
		t.Errorf("body should omit email when there is no UUIDMap, got %s", rec.Body.String())
	}
}

func TestHandleMe_Authenticated_AdminGetsAdminCapability(t *testing.T) {
	cfg := &config.Config{
		SchemaVersion: config.CurrentSchemaVersion,
		Primary:       "meadmin",
		Users: []config.User{
			{Username: "meadmin", PasswordHash: "h"},
		},
	}
	mux := http.NewServeMux()
	Mount(mux, Deps{Cfg: cfg})

	req := httptest.NewRequest(http.MethodGet, "/api/mobile/v1/me", nil)
	req = req.WithContext(auth.WithUser(req.Context(), "meadmin"))
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body MeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v (body=%s)", err, rec.Body.String())
	}
	if !body.IsAdmin {
		t.Errorf("is_admin = false, want true for the Primary")
	}
	if len(body.Capabilities) != 1 || body.Capabilities[0] != AdminCapability {
		t.Errorf("capabilities = %v, want [%q]", body.Capabilities, AdminCapability)
	}
}

func TestHandleMe_Authenticated_NonAdminGetsNoCapability(t *testing.T) {
	cfg := &config.Config{
		SchemaVersion: config.CurrentSchemaVersion,
		Primary:       "meadmin",
		Users: []config.User{
			{Username: "meadmin", PasswordHash: "h"},
			{Username: "meuser", PasswordHash: "h"},
		},
	}
	mux := http.NewServeMux()
	Mount(mux, Deps{Cfg: cfg})

	req := httptest.NewRequest(http.MethodGet, "/api/mobile/v1/me", nil)
	req = req.WithContext(auth.WithUser(req.Context(), "meuser"))
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body MeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v (body=%s)", err, rec.Body.String())
	}
	if body.IsAdmin {
		t.Errorf("is_admin = true, want false for a regular user")
	}
	if !strings.Contains(rec.Body.String(), `"capabilities":[]`) {
		t.Errorf("body should carry an explicit capabilities:[] (never omitted/null) for a non-admin, got %s", rec.Body.String())
	}
}

func TestHandleMe_Unauthenticated(t *testing.T) {
	mux := http.NewServeMux()
	Mount(mux, Deps{})

	req := httptest.NewRequest(http.MethodGet, "/api/mobile/v1/me", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d (body=%s)", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}
	wantBody := `{"error":"unauthorized"}` + "\n"
	if rec.Body.String() != wantBody {
		t.Errorf("body = %q, want %q", rec.Body.String(), wantBody)
	}
}

// TestHandleMe_NoDuplicatedAuthLogic is the proxy test: the handler must not
// contain direct references to bcrypt, jwt.Parse or auth-store types — only
// calls to auth.UserFrom/UserFromContext and
// authSvc.UUIDMap()/EmailFor, exactly as internal/api/handlers_auth.go
// already does in handleMe.
func TestHandleMe_NoDuplicatedAuthLogic(t *testing.T) {
	src, err := os.ReadFile("handlers_session.go")
	if err != nil {
		t.Fatalf("lendo handlers_session.go: %v", err)
	}
	forbidden := []string{"bcrypt", "jwt.Parse", "SupabaseClient", "sessions.Store"}
	for _, f := range forbidden {
		if strings.Contains(string(src), f) {
			t.Errorf("handlers_session.go references %q directly — auth logic must come only from internal/auth", f)
		}
	}
}
