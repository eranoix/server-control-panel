package mobilebff

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"server-control-panel/internal/auth"
)

type clientMetaKey struct{}

type clientMeta struct {
	ip string
	ua string
}

func injectClientMeta(ctx huma.Context, next func(huma.Context)) {
	req, _ := humago.Unwrap(ctx)
	meta := clientMeta{}
	if req != nil {
		meta.ip = auth.ClientIP(req)
		meta.ua = req.UserAgent()
	}
	next(huma.WithValue(ctx, clientMetaKey{}, meta))
}

func clientMetaFrom(ctx context.Context) clientMeta {
	if m, ok := ctx.Value(clientMetaKey{}).(clientMeta); ok {
		return m
	}
	return clientMeta{}
}

type PasskeyBackend interface {
	BeginPasskeyRegistration(regToken string) (optionsJSON []byte, continuationToken string, err error)

	FinishPasskeyRegistration(continuationToken, label string, credentialJSON []byte) error

	BeginPasskeyLogin() (optionsJSON []byte, continuationToken string, err error)

	FinishPasskeyLogin(continuationToken string, credentialJSON []byte, ip, userAgent string) (MobileLoginResult, error)

	AllowLoginAttempt(ip string) bool
	ResetLoginAttempts(ip string)

	ConsumePairingTicket(ticket string) (regToken string, err error)

	MobileLogin(req *http.Request, username, password, totpCode, deviceLabel string) (MobileLoginResult, error)

	MobileRefresh(refreshToken string) (MobileLoginResult, error)
}

type MobileLoginResult struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int
	TOTPRequired bool
}

var (
	ErrPasskeyInvalidToken      = errors.New("ceremony token invalid, expired or already used")
	ErrPasskeyInvalidCredential = errors.New("invalid credential")
	ErrPasskeyPendingApproval   = errors.New("pending_approval")
	ErrPasskeyUnavailable       = errors.New("passkeys not configured on this server")
	ErrPairingTicketInvalid     = errors.New("pairing ticket invalid, expired or already used")

	ErrMobileLoginInvalidCredentials = errors.New("invalid credentials")
	ErrMobileLoginLocked             = errors.New("account temporarily locked after failures, try again later")
	ErrMobileLoginRateLimited        = errors.New("too many attempts, try again later")
	ErrMobileLoginMFAUnavailable     = errors.New("MFA verification unavailable right now — try again shortly")
	ErrMobileLoginInvalidCode        = errors.New("invalid 2FA code")
	ErrMobileRefreshInvalid          = errors.New("refresh token invalid, expired or revoked")
)

type passkeyRegisterBeginInput struct {
	Body struct {
		RegToken string `json:"reg_token" doc:"Authorization token for ONE registration ceremony, issued by QR code pairing."`
	}
}

type passkeyRegisterBeginOutput struct {
	Body struct {
		Options           json.RawMessage `json:"options" doc:"PublicKeyCredentialCreationOptions: pass to the authenticator (navigator.credentials.create() / WebAuthn Android)."`
		ContinuationToken string          `json:"continuation_token" doc:"Send unchanged to /register/finish."`
	}
}

type passkeyRegisterFinishInput struct {
	Body struct {
		ContinuationToken string          `json:"continuation_token"`
		Label             string          `json:"label,omitempty" doc:"Device label shown on the approval screen (e.g. 'Pixel 8 - Chrome')."`
		Credential        json.RawMessage `json:"credential" doc:"Raw authenticator response (serialized PublicKeyCredential)."`
	}
}

type passkeyRegisterFinishOutput struct {
	Status int
	Body   struct {
		Status string `json:"status"`
	}
}

type passkeyLoginBeginInput struct{}

type passkeyLoginBeginOutput struct {
	Body struct {
		Options           json.RawMessage `json:"options" doc:"PublicKeyCredentialRequestOptions: pass to the authenticator."`
		ContinuationToken string          `json:"continuation_token" doc:"Send unchanged to /login/finish."`
	}
}

type passkeyLoginFinishInput struct {
	Body struct {
		ContinuationToken string          `json:"continuation_token"`
		Credential        json.RawMessage `json:"credential" doc:"Raw authenticator response (serialized PublicKeyCredential)."`
	}
}

type passkeyLoginFinishOutput struct {
	Status int
	Body   struct {
		AccessToken  string `json:"access_token,omitempty" doc:"Session JWT, present only when the credential is already approved."`
		RefreshToken string `json:"refresh_token,omitempty" doc:"Rotating mobile refresh token (same family as auth_login.go), present together with access_token."`
		ExpiresIn    int    `json:"expires_in,omitempty" doc:"access_token lifetime in seconds."`
		Error        string `json:"error,omitempty" doc:"Stable error code (e.g. 'pending_approval'). Absent on success."`
		Message      string `json:"message,omitempty" doc:"Human-readable message to show the user. Absent on success."`
	}
}

func init() { RegisterPublic("passkey", registerPasskey) }

func registerPasskey(api huma.API, deps Deps) {
	pk := deps.Passkey

	huma.Register(api, huma.Operation{
		OperationID: "passkeyRegisterBegin",
		Method:      http.MethodPost,
		Path:        "/auth/passkey/register/begin",
		Summary:     "Starts registering a passkey",
		Tags:        []string{"mobile", "auth"},
	}, func(_ context.Context, in *passkeyRegisterBeginInput) (*passkeyRegisterBeginOutput, error) {
		if pk == nil {
			return nil, huma.Error503ServiceUnavailable(ErrPasskeyUnavailable.Error())
		}
		optionsJSON, cont, err := pk.BeginPasskeyRegistration(in.Body.RegToken)
		if err != nil {
			return nil, mapPasskeyError(err)
		}
		out := &passkeyRegisterBeginOutput{}
		out.Body.Options = optionsJSON
		out.Body.ContinuationToken = cont
		return out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "passkeyRegisterFinish",
		Method:      http.MethodPost,
		Path:        "/auth/passkey/register/finish",
		Summary:     "Finishes registering a passkey (NEVER authenticates)",
		Description: "Success here ALWAYS returns {\"status\":\"pending_approval\"}. The credential starts inert and can only log in after it is approved from an already authenticated desktop session.",
		Tags:        []string{"mobile", "auth"},
	}, func(_ context.Context, in *passkeyRegisterFinishInput) (*passkeyRegisterFinishOutput, error) {
		if pk == nil {
			return nil, huma.Error503ServiceUnavailable(ErrPasskeyUnavailable.Error())
		}
		if err := pk.FinishPasskeyRegistration(in.Body.ContinuationToken, in.Body.Label, in.Body.Credential); err != nil {
			return nil, mapPasskeyError(err)
		}
		out := &passkeyRegisterFinishOutput{Status: http.StatusOK}
		out.Body.Status = "pending_approval"
		return out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "passkeyLoginBegin",
		Method:      http.MethodPost,
		Path:        "/auth/passkey/login/begin",
		Summary:     "Starts a passwordless login (discoverable passkey)",
		Tags:        []string{"mobile", "auth"},
		Middlewares: huma.Middlewares{injectClientMeta},
	}, func(ctx context.Context, _ *passkeyLoginBeginInput) (*passkeyLoginBeginOutput, error) {
		if pk == nil {
			return nil, huma.Error503ServiceUnavailable(ErrPasskeyUnavailable.Error())
		}
		ip := clientMetaFrom(ctx).ip
		if !pk.AllowLoginAttempt(ip) {
			return nil, huma.Error429TooManyRequests("too many attempts, try again later")
		}
		optionsJSON, cont, err := pk.BeginPasskeyLogin()
		if err != nil {
			return nil, mapPasskeyError(err)
		}
		out := &passkeyLoginBeginOutput{}
		out.Body.Options = optionsJSON
		out.Body.ContinuationToken = cont
		return out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "passkeyLoginFinish",
		Method:      http.MethodPost,
		Path:        "/auth/passkey/login/finish",
		Summary:     "Finishes the passwordless login",
		Description: "Success returns access_token + refresh_token (same shape as /auth/login). 403 {\"error\":\"pending_approval\"} when the credential exists but has not been approved in the panel yet.",
		Tags:        []string{"mobile", "auth"},
		Middlewares: huma.Middlewares{injectClientMeta},
	}, func(ctx context.Context, in *passkeyLoginFinishInput) (*passkeyLoginFinishOutput, error) {
		if pk == nil {
			return nil, huma.Error503ServiceUnavailable(ErrPasskeyUnavailable.Error())
		}
		meta := clientMetaFrom(ctx)
		if !pk.AllowLoginAttempt(meta.ip) {
			return nil, huma.Error429TooManyRequests("too many attempts, try again later")
		}
		res, err := pk.FinishPasskeyLogin(in.Body.ContinuationToken, in.Body.Credential, meta.ip, meta.ua)
		if err != nil {
			if errors.Is(err, ErrPasskeyPendingApproval) {
				out := &passkeyLoginFinishOutput{Status: http.StatusForbidden}
				out.Body.Error = "pending_approval"
				out.Body.Message = "Waiting for approval in the panel"
				return out, nil
			}
			return nil, mapPasskeyError(err)
		}
		pk.ResetLoginAttempts(meta.ip)
		out := &passkeyLoginFinishOutput{Status: http.StatusOK}
		out.Body.AccessToken = res.AccessToken
		out.Body.RefreshToken = res.RefreshToken
		out.Body.ExpiresIn = res.ExpiresIn
		return out, nil
	})
}

func mapPasskeyError(err error) error {
	switch {
	case errors.Is(err, ErrPasskeyInvalidToken):
		return huma.Error401Unauthorized(err.Error())
	case errors.Is(err, ErrPasskeyInvalidCredential):
		return huma.Error401Unauthorized("invalid credential")
	case errors.Is(err, ErrPasskeyUnavailable):
		return huma.Error503ServiceUnavailable(err.Error())
	case errors.Is(err, ErrPairingTicketInvalid):
		return huma.Error401Unauthorized(err.Error())
	case errors.Is(err, ErrMobileLoginInvalidCredentials):
		return huma.Error401Unauthorized(err.Error())
	case errors.Is(err, ErrMobileLoginLocked):
		return huma.Error423Locked(err.Error())
	case errors.Is(err, ErrMobileLoginRateLimited):
		return huma.Error429TooManyRequests(err.Error())
	case errors.Is(err, ErrMobileLoginMFAUnavailable):
		return huma.Error503ServiceUnavailable(err.Error())
	case errors.Is(err, ErrMobileLoginInvalidCode):
		return huma.Error401Unauthorized(err.Error())
	case errors.Is(err, ErrMobileRefreshInvalid):
		return huma.Error401Unauthorized(err.Error())
	default:
		log.Printf("mobilebff: passkey error: %v", err)
		return huma.Error400BadRequest("invalid request")
	}
}
