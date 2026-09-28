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

type UserEmailMapper interface {
	UUIDMap() *auth.UUIDMap
}

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

func init() { Register("session", registerSession) }

func registerSession(api huma.API, deps Deps) {
	authSvc := deps.Auth
	cfg := deps.Cfg
	huma.Register(api, huma.Operation{
		OperationID: "getMe",
		Method:      http.MethodGet,
		Path:        "/me",
		Summary:     "Identity, session and capabilities of the authenticated user",
		Tags:        []string{"mobile"},
		Middlewares: huma.Middlewares{requireAuth},
	}, meHandler(authSvc, cfg))
}

func requireAuth(ctx huma.Context, next func(huma.Context)) {
	req, w := humago.Unwrap(ctx)
	if auth.UserFrom(req) == "" {
		httpx.WriteErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	next(ctx)
}

var RequireAuth = requireAuth

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
