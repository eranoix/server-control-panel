package mobilebff

// auth_login.go — password login and session refresh for the native Android
// app. Registered through RegisterPublic (registry_public.go): both routes
// sit OUTSIDE auth.Middleware, because the phone still has no session at all
// when it logs in, or when it needs to trade in an expiring refresh token.
//
// CENTRAL GUARANTEE: /auth/login reuses the SAME second-factor check as the
// desktop panel (verifyLoginMFA, behind PasskeyBackend.MobileLogin) — never
// a second copy of the MFA policy. The second factor is EXACTLY the desktop
// one: Supabase MFA (TOTP through GoTrue) or a single-use backup code; there
// is no separate local TOTP for mobile. When the factor is enrolled and no
// code was sent, the answer is {"totp_required": true} — the same non-error
// branch handleLogin takes — and the app must ask for the code and call
// again.
//
// /auth/refresh rotates unconditionally: the token that was sent stops
// working in THIS very call, success or not — it never lingers as "almost
// valid" on a second attempt. Deliberately without any password/MFA check:
// it has to be light enough for the app to call it proactively, before the
// access token expires.

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

type mobileLoginInput struct {
	Body struct {
		Username    string `json:"username" doc:"Username ou email cadastrado."`
		Password    string `json:"password"`
		TOTPCode    string `json:"totp_code,omitempty" doc:"Código do segundo fator (Supabase MFA/TOTP ou backup code). Omitir na primeira tentativa."`
		DeviceLabel string `json:"device_label,omitempty" doc:"Rótulo do aparelho, usado na lista de sessões mobile (ex.: 'Pixel 8')."`
	}
}

type mobileLoginOutput struct {
	Body struct {
		AccessToken  string `json:"access_token,omitempty"`
		RefreshToken string `json:"refresh_token,omitempty"`
		ExpiresIn    int    `json:"expires_in,omitempty"`
		TOTPRequired bool   `json:"totp_required,omitempty" doc:"Quando true, nenhum token foi emitido — reenviar com totp_code preenchido."`
	}
}

type mobileRefreshInput struct {
	Body struct {
		RefreshToken string `json:"refresh_token"`
	}
}

type mobileRefreshOutput struct {
	Body struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token" doc:"Novo refresh token — o anterior já foi invalidado por esta chamada."`
		ExpiresIn    int    `json:"expires_in"`
	}
}

func init() { RegisterPublic("login", registerMobileLogin) }

func registerMobileLogin(api huma.API, deps Deps) {
	pk := deps.Passkey

	huma.Register(api, huma.Operation{
		OperationID: "mobileLogin",
		Method:      http.MethodPost,
		Path:        "/auth/login",
		Summary:     "Login por senha + segundo fator (mesma política MFA do painel desktop)",
		Description: "Sucesso devolve access_token + refresh_token. Quando o segundo fator está enrolado e totp_code não foi enviado, devolve {\"totp_required\":true} sem nenhum token.",
		Tags:        []string{"mobile", "auth"},
		Middlewares: huma.Middlewares{injectClientMeta},
	}, func(ctx context.Context, in *mobileLoginInput) (*mobileLoginOutput, error) {
		if pk == nil {
			return nil, huma.Error503ServiceUnavailable(ErrPasskeyUnavailable.Error())
		}
		meta := clientMetaFrom(ctx)
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "/auth/login", nil)
		req.Header.Set("X-Forwarded-For", meta.ip)
		req.Header.Set("User-Agent", meta.ua)

		res, err := pk.MobileLogin(req, in.Body.Username, in.Body.Password, in.Body.TOTPCode, in.Body.DeviceLabel)
		if err != nil {
			return nil, mapPasskeyError(err)
		}
		out := &mobileLoginOutput{}
		if res.TOTPRequired {
			out.Body.TOTPRequired = true
			return out, nil
		}
		out.Body.AccessToken = res.AccessToken
		out.Body.RefreshToken = res.RefreshToken
		out.Body.ExpiresIn = res.ExpiresIn
		return out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "mobileRefresh",
		Method:      http.MethodPost,
		Path:        "/auth/refresh",
		Summary:     "Troca um refresh token mobile por um novo par access+refresh",
		Description: "Rotação obrigatória: o refresh_token enviado é invalidado por esta chamada, com sucesso ou sem. Reenviar um token já rotacionado, revogado ou nunca emitido falha da mesma forma (401).",
		Tags:        []string{"mobile", "auth"},
	}, func(_ context.Context, in *mobileRefreshInput) (*mobileRefreshOutput, error) {
		if pk == nil {
			return nil, huma.Error503ServiceUnavailable(ErrPasskeyUnavailable.Error())
		}
		res, err := pk.MobileRefresh(in.Body.RefreshToken)
		if err != nil {
			return nil, mapPasskeyError(err)
		}
		out := &mobileRefreshOutput{}
		out.Body.AccessToken = res.AccessToken
		out.Body.RefreshToken = res.RefreshToken
		out.Body.ExpiresIn = res.ExpiresIn
		return out, nil
	})
}
