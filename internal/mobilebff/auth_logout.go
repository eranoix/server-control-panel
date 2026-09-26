package mobilebff

// auth_logout.go — POST /auth/logout, the symmetric counterpart of password
// login (auth_login.go) and of passkey login (auth_passkey.go): it ends the
// session of the CALLING device, and only that one. Never
// RevokeAllExcept/revoke-everything — that would silently drop the desktop
// panel and every other paired Android device of the same user, when the
// request only ever spoke about one.
//
// It lives in a file of its own (not inside auth_login.go) on purpose:
// login/refresh (MobileRefreshStore) and logout (JWT session) are surfaces
// of different features that evolve in parallel — one file per feature keeps
// concurrent work on one from overwriting the other in a shared file.
//
// It reuses internal/sessions.Store, the SAME session storage the web panel
// already uses for password/2FA login (see handleLogout in
// internal/api/handlers_auth.go) — not a separate MobileRefreshStore. Passkey
// login (auth_passkey.go, FinishPasskeyLogin) issues a session JWT through
// the SAME internal/auth.Service the panel uses, so revocation has to match:
// revoking by jti in that same Store is what guarantees this token stops
// authenticating on ANY protected route — the web panel included — not just
// on the mobile BFF's routes.
//
// NOTE for whoever touches the mobile refresh flow (MobileRefreshStore): if
// mobile login ever starts issuing a long-lived refresh token too (alongside
// the short JWT), this handler will probably have to revoke BOTH records of
// the same device — see the deferred items recorded for the auth work.

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/sessions"
)

// sessionMetaKey identifies, inside the context.Context handed to the typed
// handler, the identity of the request's CURRENT session (username + jti of
// the JWT validated by auth.Middleware) — the same pattern as clientMetaKey
// in auth_passkey.go: typed huma handlers only ever receive a bare
// context.Context, so injectSessionMeta is the only channel to the jti here.
type sessionMetaKey struct{}

type sessionMeta struct {
	user string
	jti  string
	ip   string
}

// injectSessionMeta unwraps the raw *http.Request exactly once and stores the
// user/jti/IP already validated by auth.Middleware (the "protected" mux, see
// internal/api/api.go) in the context.Context the typed handler actually
// receives. auth.JTIFrom/auth.UserFrom are the SAME source of truth
// handleLogout uses in the web panel — no new JWT extraction anywhere.
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
		Summary:     "Encerra a sessão do dispositivo chamador — nunca as demais sessões do usuário",
		Description: "Revoga SOMENTE o jti do token usado nesta chamada (mesmo internal/sessions.Store do painel web). Idempotente: chamar de novo com um token já revogado ainda responde 200.",
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

// revokeOwnSession isolates in one place the nil-check on deps.Sessions
// (absent in cmd/mobile-openapi-gen, see the Deps docstring in registry.go)
// and the empty-jti case (a token without that claim — nothing to revoke).
func revokeOwnSession(store *sessions.Store, jti string) {
	if store == nil || jti == "" {
		return
	}
	store.Revoke(jti)
}

// shortJTI avoids writing the whole jti into the audit log (it is, together
// with the rest of the JWT, a session-possession secret) — the same care
// handlers_auth.go already applies to credential IDs (r.auditEvent(...,
// body.ID[:min(8, len(body.ID))])).
func shortJTI(jti string) string {
	if len(jti) <= 8 {
		return jti
	}
	return jti[:8]
}
