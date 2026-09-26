package mobilebff

// auth_passkey.go — passkey (WebAuthn/FIDO2) login and registration for the
// native Android app. Registered through RegisterPublic (registry_public.go):
// these four routes sit OUTSIDE auth.Middleware, because login and the
// registration step happen before any session exists.
//
// CENTRAL SECURITY GUARANTEE: register/finish NEVER returns a session token —
// success on this call is, by construction, ALWAYS the same fixed body
// {"status":"pending_approval"} (see passkeyRegisterFinishOutput).
// Photographing the pairing QR code and completing registration with an
// authenticator of your own grants no access: it creates only an inert record,
// which turns into a usable credential only after being approved from an
// already-authenticated desktop session. There is no code path in this file
// where the passkeyRegisterFinish handler returns anything but that fixed body
// on success — the proof of which is TestPasskeyRegisterFinish_NeverReturnsToken
// in auth_passkey_test.go.
//
// PasskeyBackend (defined below) is this file's only point of contact with the
// real WebAuthn ceremonies (internal/auth/webauthn.go) and with the
// pending/approved credential storage — implemented by internal/api.Router, so
// that this package never has to import
// github.com/go-webauthn/webauthn.

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

// clientMetaKey identifies, inside the context.Context handed to a typed
// handler, the network metadata (IP, User-Agent) of the raw HTTP request —
// reachable in huma only from a Middleware's huma.Context (see
// injectClientMeta). Typed handlers receive a bare context.Context only
// (huma.Context is not exported beyond the Middleware), so this is the only
// channel to the client IP inside passkeyLoginBegin/Finish.
type clientMetaKey struct{}

type clientMeta struct {
	ip string
	ua string
}

// injectClientMeta is operation Middleware (not package-level): it unwraps the
// raw *http.Request exactly once, through humago.Unwrap, and stores IP/UA in
// the context.Context the typed handlers actually receive — the same pattern
// requireAuth (handlers_session.go) uses to expose auth.UserFromContext.
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

// PasskeyBackend is the minimal slice of internal/api.Router this file needs
// in order to complete the four ceremonies.
type PasskeyBackend interface {
	// BeginPasskeyRegistration consumes `regToken` (single-use, issued by
	// internal/auth.IssueWebAuthnRegToken out of the QR-code pairing) and
	// starts a registration ceremony. It returns the
	// PublicKeyCredentialCreationOptions JSON to hand to the authenticator and
	// an opaque continuation token that MUST be handed back, unaltered, to
	// FinishPasskeyRegistration.
	BeginPasskeyRegistration(regToken string) (optionsJSON []byte, continuationToken string, err error)

	// FinishPasskeyRegistration validates the authenticator's response. On
	// cryptographic success the credential is persisted with status "pending"
	// — THAT IS ALL. This function never authenticates; the caller must never
	// try to squeeze a token out of it.
	FinishPasskeyRegistration(continuationToken, label string, credentialJSON []byte) error

	// BeginPasskeyLogin starts a discoverable (userless) login ceremony.
	BeginPasskeyLogin() (optionsJSON []byte, continuationToken string, err error)

	// FinishPasskeyLogin validates the assertion. On success it returns the
	// SAME access+refresh pair as MobileLogin — passkey is the product's
	// primary path and cannot offer worse session continuity than password
	// login, which already renews silently through rotating refresh. If the
	// matched credential is still pending approval, it returns
	// ErrPasskeyPendingApproval and a zero-value MobileLoginResult — NO token
	// of any kind.
	FinishPasskeyLogin(continuationToken string, credentialJSON []byte, ip, userAgent string) (MobileLoginResult, error)

	// AllowLoginAttempt/ResetLoginAttempts reuse the SAME limiter as password
	// login.
	AllowLoginAttempt(ip string) bool
	ResetLoginAttempts(ip string)

	// ConsumePairingTicket trades a QR-code pairing ticket (issued by an
	// already-authenticated desktop session through
	// POST /api/auth/mobile-pair, one-shot, see internal/auth/tokens.go
	// IssuePairingTicket/ConsumePairingTicket) for a reg_token good for ONE
	// passkey registration ceremony (kind webauthn_reg, also one-shot). It
	// NEVER returns a session token: the reg_token only authorizes one call to
	// BeginPasskeyRegistration, whose success still ends in a "pending"
	// credential (see FinishPasskeyRegistration above). A consumed, expired or
	// never-issued ticket all fail the SAME way — a replay never "almost
	// works".
	ConsumePairingTicket(ticket string) (regToken string, err error)

	// MobileLogin authenticates with password + second factor EXACTLY the way
	// handleLogin does for the desktop panel (same verifyLoginMFA, same
	// trusted-device/backup-code policy) — never a second copy of the MFA
	// logic. On full success it issues an access token (auth.Issue) and a
	// rotating mobile refresh token (auth.MobileRefreshStore) bound to
	// deviceLabel. When the user has a Supabase factor enrolled and no code was
	// sent, it returns (MobileLoginResult{TOTPRequired:true}, nil) — that is
	// not an error, it is the SAME "totp_required" branch desktop login already
	// has; the app must ask for the code and call again with totpCode filled
	// in.
	MobileLogin(req *http.Request, username, password, totpCode, deviceLabel string) (MobileLoginResult, error)

	// MobileRefresh trades a valid mobile refresh token for a NEW access+refresh
	// pair — rotation is mandatory: the old token stops working in THIS very
	// call, even if the response is lost before it reaches the client.
	// Deliberately without any password/MFA check — it has to be light enough
	// for the app to call it proactively before the access token expires, the
	// mechanism that avoids the earlier failure shape of a token expiring while
	// a WS connection is open.
	MobileRefresh(refreshToken string) (MobileLoginResult, error)
}

// MobileLoginResult is what MobileLogin/MobileRefresh return: either a fresh
// access+refresh pair, or a "needs a TOTP code" signal (not an error — the
// same shape as desktop login's `{"totp_required": true}` branch).
type MobileLoginResult struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int
	TOTPRequired bool
}

// Errors PasskeyBackend may return — auth_passkey.go translates them into
// specific HTTP responses.
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
		RegToken string `json:"reg_token" doc:"Token de autorização de UMA cerimônia de registro, emitido pelo pareamento por QR code (03-04)."`
	}
}

type passkeyRegisterBeginOutput struct {
	Body struct {
		Options           json.RawMessage `json:"options" doc:"PublicKeyCredentialCreationOptions — repassar ao autenticador (navigator.credentials.create() / WebAuthn Android)."`
		ContinuationToken string          `json:"continuation_token" doc:"Enviar sem alteração em /register/finish."`
	}
}

type passkeyRegisterFinishInput struct {
	Body struct {
		ContinuationToken string          `json:"continuation_token"`
		Label             string          `json:"label,omitempty" doc:"Rótulo do dispositivo, mostrado na tela de aprovação (ex.: 'Pixel 8 — Chrome')."`
		Credential        json.RawMessage `json:"credential" doc:"Resposta bruta do autenticador (PublicKeyCredential serializado)."`
	}
}

// passkeyRegisterFinishOutput.Body is the ONLY possible success body of this
// operation — no token field, no session field. This is not a convention a
// handler could forget to follow: it is the shape of the type.
type passkeyRegisterFinishOutput struct {
	Status int
	Body   struct {
		Status string `json:"status"`
	}
}

type passkeyLoginBeginInput struct{}

type passkeyLoginBeginOutput struct {
	Body struct {
		Options           json.RawMessage `json:"options" doc:"PublicKeyCredentialRequestOptions — repassar ao autenticador."`
		ContinuationToken string          `json:"continuation_token" doc:"Enviar sem alteração em /login/finish."`
	}
}

type passkeyLoginFinishInput struct {
	Body struct {
		ContinuationToken string          `json:"continuation_token"`
		Credential        json.RawMessage `json:"credential" doc:"Resposta bruta do autenticador (PublicKeyCredential serializado)."`
	}
}

// passkeyLoginFinishOutput uses exactly the same success shape as
// mobileLoginOutput/mobileRefreshOutput (auth_login.go) — access_token +
// refresh_token + expires_in, never the desktop panel's bare `token` (which
// has no refresh token to live beside it). One single field name for the same
// session JWT across the whole mobile BFF keeps the app from having to handle
// two response shapes for the same piece of data.
type passkeyLoginFinishOutput struct {
	Status int
	Body   struct {
		AccessToken  string `json:"access_token,omitempty" doc:"JWT de sessão — presente somente quando a credencial já está aprovada."`
		RefreshToken string `json:"refresh_token,omitempty" doc:"Refresh token mobile rotativo — mesma família de auth_login.go, presente junto com access_token."`
		ExpiresIn    int    `json:"expires_in,omitempty" doc:"Validade do access_token em segundos."`
		Error        string `json:"error,omitempty" doc:"Código de erro estável (ex.: 'pending_approval'). Ausente em caso de sucesso."`
		Message      string `json:"message,omitempty" doc:"Mensagem legível para exibir ao usuário. Ausente em caso de sucesso."`
	}
}

func init() { RegisterPublic("passkey", registerPasskey) }

func registerPasskey(api huma.API, deps Deps) {
	pk := deps.Passkey

	huma.Register(api, huma.Operation{
		OperationID: "passkeyRegisterBegin",
		Method:      http.MethodPost,
		Path:        "/auth/passkey/register/begin",
		Summary:     "Inicia o registro de uma passkey",
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
		Summary:     "Conclui o registro de uma passkey — NUNCA autentica",
		Description: "Sucesso aqui SEMPRE devolve {\"status\":\"pending_approval\"}. A credencial nasce inerte e só pode logar depois de aprovada a partir de uma sessão desktop já autenticada.",
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
		Summary:     "Inicia um login sem senha (passkey discoverable)",
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
		Summary:     "Conclui o login sem senha",
		Description: "Sucesso devolve access_token + refresh_token (mesma forma de /auth/login). 403 {\"error\":\"pending_approval\"} quando a credencial existe mas ainda não foi aprovada no painel.",
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

// mapPasskeyError translates PasskeyBackend's errors into the matching HTTP
// status. Every branch UP TO the default compares against a sentinel from this
// very package (see the var block above) — a fixed message, never a wrap
// carrying server detail, so echoing err.Error() in those cases is safe. The
// `default`, by contrast, is the safety net for ANY other error — including
// the ~18 fmt.Errorf("passkey: ...: %w", err) sites in internal/api/passkey.go,
// which wrap storage/IO errors and by their nature may carry a file path or
// internal detail. This is the most sensitive PUBLIC (unauthenticated) surface
// of the BFF — never echo err.Error() here, the same stance as
// handlers_screens.go/handlers_actions.go: a generic message for the client,
// the full error only in the server log.
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
