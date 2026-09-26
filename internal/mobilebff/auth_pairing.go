package mobilebff

// auth_pairing.go — public exchange of the QR-code pairing ticket for a token
// that authorizes ONE passkey registration ceremony. Registered through
// RegisterPublic (registry_public.go): this route sits OUTSIDE
// auth.Middleware, because the new phone still has no session at all when it
// scans the QR code.
//
// CENTRAL SECURITY GUARANTEE: this route NEVER returns a session token. The
// pairing ticket only buys a reg_token (kind webauthn_reg), which in turn
// only authorizes ONE call to POST /auth/passkey/register/begin
// (auth_passkey.go) — and that call, on success, ends in a "pending"
// credential, never in a session. Photographing the QR code is not, on its
// own, enough to log in anywhere: whoever scans it still has to complete a
// WebAuthn ceremony with an authenticator of their own, and even that
// credential is born inert until it is approved from an already-authenticated
// desktop session. An expired ticket, an already-consumed one, and one that
// was never issued all fail the SAME way (ErrPairingTicketInvalid) — never
// revealing which of the three happened.
//
// It deliberately does NOT duplicate BeginRegistrationForUser here: this
// route's only job is to validate the ticket and hand back the reg_token. The
// Android app then calls the ALREADY EXISTING /auth/passkey/register/begin
// endpoint with that reg_token to actually start the WebAuthn ceremony — it
// keeps mobilebff a thin consumer, without a second copy of the ceremony
// authorization logic.

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

type pairingConsumeInput struct {
	Body struct {
		Ticket string `json:"ticket" doc:"Ticket de pareamento mostrado no QR code do painel desktop/web (POST /api/auth/mobile-pair, one-shot, 5min)."`
	}
}

type pairingConsumeOutput struct {
	Body struct {
		RegToken string `json:"reg_token" doc:"Token de autorização de UMA cerimônia de registro de passkey — enviar a /auth/passkey/register/begin."`
	}
}

func init() { RegisterPublic("pairing", registerPairing) }

func registerPairing(api huma.API, deps Deps) {
	pk := deps.Passkey

	huma.Register(api, huma.Operation{
		OperationID: "mobilePairConsume",
		Method:      http.MethodPost,
		Path:        "/auth/pair",
		Summary:     "Troca um ticket de pareamento por QR code por um reg_token de passkey",
		Description: "Nunca devolve uma sessão. O reg_token só autoriza começar uma cerimônia de registro de passkey em /auth/passkey/register/begin, cujo sucesso ainda fica pendente de aprovação no painel.",
		Tags:        []string{"mobile", "auth"},
	}, func(_ context.Context, in *pairingConsumeInput) (*pairingConsumeOutput, error) {
		if pk == nil {
			return nil, huma.Error503ServiceUnavailable(ErrPasskeyUnavailable.Error())
		}
		regToken, err := pk.ConsumePairingTicket(in.Body.Ticket)
		if err != nil {
			return nil, mapPasskeyError(err)
		}
		out := &pairingConsumeOutput{}
		out.Body.RegToken = regToken
		return out, nil
	})
}
