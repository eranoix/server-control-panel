package mobilebff

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/sessions"
)

type sessionMetaKey struct{}

type sessionMeta struct {
	user string
	jti  string
	ip   string
}

func injectSessionMeta(ctx huma.Context, next func(huma.Context)) {
	req, _ := humago.Unwrap(ctx)
	meta := sessionMeta{}
	if req != nil {
		meta.user = auth.UserFrom(req)
		meta.jti = auth.JTIFrom(req)
		meta.ip = auth.ClientIP(req)
	}
	next(huma.WithValue(ctx, sessionMetaKey{}, meta))
}

func sessionMetaFrom(ctx context.Context) sessionMeta {
	if m, ok := ctx.Value(sessionMetaKey{}).(sessionMeta); ok {
		return m
	}
	return sessionMeta{}
}

type mobileLogoutOutput struct {
	Body struct {
		Status string `json:"status"`
	}
}

func init() { Register("logout", registerLogout) }

func registerLogout(api huma.API, deps Deps) {
	store := deps.Sessions
	audit := deps.Audit

	huma.Register(api, huma.Operation{
		OperationID: "mobileLogout",
		Method:      http.MethodPost,
		Path:        "/auth/logout",
		Summary:     "Ends the calling device's session, never the user's other sessions",
		Description: "Revokes ONLY the jti of the token used in this call (same internal/sessions.Store as the web panel). Idempotent: calling again with an already revoked token still answers 200.",
		Tags:        []string{"mobile", "auth"},
		Middlewares: huma.Middlewares{requireAuth, injectSessionMeta},
	}, func(ctx context.Context, _ *struct{}) (*mobileLogoutOutput, error) {
		meta := sessionMetaFrom(ctx)
		revokeOwnSession(store, meta.jti)
		if audit != nil {
			audit.Append(auth.Event{User: meta.user, Action: "mobile.logout", Target: shortJTI(meta.jti), IP: meta.ip})
		}
		out := &mobileLogoutOutput{}
		out.Body.Status = "ok"
		return out, nil
	})
}

func revokeOwnSession(store *sessions.Store, jti string) {
	if store == nil || jti == "" {
		return
	}
	store.Revoke(jti)
}

func shortJTI(jti string) string {
	if len(jti) <= 8 {
		return jti
	}
	return jti[:8]
}
