package mobilebff

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/config"
	"server-control-panel/internal/httpx"
)

// UserEmailMapper is this package's only point of contact with internal/auth
// beyond the package-level functions (UserFrom/UserFromContext) — the minimal
// slice of *auth.Service this endpoint needs (the username -> email mapping
// already computed by auth.LoadUUIDMap at boot). It is not a new computation:
// it is the same read internal/api/handlers_auth.go does in handleMe.
type UserEmailMapper interface {
	UUIDMap() *auth.UUIDMap
}

// MeResponse is the body of /me — a subset, with field names stable for the
// app, of what handleMe already computes in internal/api/handlers_auth.go,
// plus the server-authoritative answer to "what can this user do" (see
// CapabilitiesFor in capabilities.go). No field here is derived from new
// logic: identity/email come from auth.UserFrom +
// auth.UUIDMap().EmailFor (the same ones the web panel uses), and
// IsAdmin/Capabilities come from the same binary admin/non-admin gate that
// already protects every admin-only route of the panel (httpx.IsAdmin).
type MeResponse struct {
	User         string   `json:"user"`
	Email        string   `json:"email,omitempty"`
	ServerTime   int64    `json:"server_time"`
	IsAdmin      bool     `json:"is_admin"`
	Capabilities []string `json:"capabilities"`
}

type meOutput struct {
	Body MeResponse
}

// registerSession registers GET /me — the authenticated user's
// identity/session, shaped for the app's "who am I" screen. This handler must
// NEVER grow domain logic of its own (password, JWT, session): the previous
// mobile surface diverged from the web panel exactly because it duplicated
// auth logic in a second place, and the two versions silently drifted apart.
// This package's rule — a single contract, generated from the Go registry —
// only holds while handlers like this one stay restricted to calling
// auth.UserFrom/UserFromContext and authSvc.UUIDMap(), never reimplementing
// what those functions already do.
func init() { Register("session", registerSession) }

func registerSession(api huma.API, deps Deps) {
	authSvc := deps.Auth
	cfg := deps.Cfg
	huma.Register(api, huma.Operation{
		OperationID: "getMe",
		Method:      http.MethodGet,
		Path:        "/me",
		Summary:     "Identidade, sessão e capacidades do usuário autenticado",
		Tags:        []string{"mobile"},
		Middlewares: huma.Middlewares{requireAuth},
	}, meHandler(authSvc, cfg))
}

// requireAuth is the same gate as handleWSTicket (internal/api/handlers_auth.go):
// if there is no username in the request context, it replies 401 in the
// project's standard format (httpx.WriteErr) before letting the typed handler run.
// This is defence in depth — in production the route already sits behind the same
// auth.Middleware that protects /api/auth/me — but since internal/mobilebff does
// not own that Middleware, this package needs its own checkpoint so as not to
// depend silently on how the caller assembles the mux.
func requireAuth(ctx huma.Context, next func(huma.Context)) {
	req, w := humago.Unwrap(ctx)
	if auth.UserFrom(req) == "" {
		httpx.WriteErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	next(ctx)
}

// RequireAuth exposes requireAuth to screen packages outside this one (e.g.
// internal/mobilebff/screens), which register their own routes via
// Register but cannot see this package's unexported identifiers.
// Same gate, no duplication: an alias, not a second implementation.
var RequireAuth = requireAuth

// meHandler is the pure adapter: it reads the username already validated by
// Middleware (via auth.UserFromContext, the equivalent of auth.UserFrom for
// huma handlers, which only receive a context.Context), the mapped email (if
// any) — exactly as handleMe does for the web panel — and the user's
// server-authoritative capabilities via CapabilitiesFor (capabilities.go),
// which in turn delegates to httpx.IsAdmin. No authorization decision is taken
// here; this handler only serializes what CapabilitiesFor already decided.
func meHandler(authSvc UserEmailMapper, cfg *config.Config) func(ctx context.Context, input *struct{}) (*meOutput, error) {
	return func(ctx context.Context, _ *struct{}) (*meOutput, error) {
		user := auth.UserFromContext(ctx)
		isAdmin, capabilities := CapabilitiesFor(cfg, user)
		resp := MeResponse{
			User:         user,
			ServerTime:   time.Now().Unix(),
			IsAdmin:      isAdmin,
			Capabilities: capabilities,
		}
		if authSvc != nil {
			if um := authSvc.UUIDMap(); um != nil {
				resp.Email = um.EmailFor(user)
			}
		}
		return &meOutput{Body: resp}, nil
	}
}
