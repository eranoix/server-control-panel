package mobilebff

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

type mobileLoginInput struct {
	Body struct {
		Username    string `json:"username" doc:"Registered username or email."`
		Password    string `json:"password"`
		TOTPCode    string `json:"totp_code,omitempty" doc:"Second-factor code (Supabase MFA/TOTP or backup code). Omit on the first attempt."`
		DeviceLabel string `json:"device_label,omitempty" doc:"Device label, used in the mobile sessions list (e.g. 'Pixel 8')."`
	}
}

type mobileLoginOutput struct {
	Body struct {
		AccessToken  string `json:"access_token,omitempty"`
		RefreshToken string `json:"refresh_token,omitempty"`
		ExpiresIn    int    `json:"expires_in,omitempty"`
		TOTPRequired bool   `json:"totp_required,omitempty" doc:"When true, no token was issued: resend with totp_code filled in."`
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
		RefreshToken string `json:"refresh_token" doc:"New refresh token; the previous one was already invalidated by this call."`
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
		Summary:     "Password login + second factor (same MFA policy as the desktop panel)",
		Description: "Success returns access_token + refresh_token. When a second factor is enrolled and totp_code was not sent, returns {\"totp_required\":true} with no token.",
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
		Summary:     "Exchanges a mobile refresh token for a new access+refresh pair",
		Description: "Mandatory rotation: the refresh_token sent is invalidated by this call, whether it succeeds or not. Resending a token that was already rotated, revoked or never issued fails the same way (401).",
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
