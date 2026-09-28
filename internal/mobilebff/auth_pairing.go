package mobilebff

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

type pairingConsumeInput struct {
	Body struct {
		Ticket string `json:"ticket" doc:"Pairing ticket shown in the desktop/web panel QR code (POST /api/auth/mobile-pair, one-shot, 5min)."`
	}
}

type pairingConsumeOutput struct {
	Body struct {
		RegToken string `json:"reg_token" doc:"Authorization token for ONE passkey registration ceremony: send it to /auth/passkey/register/begin."`
	}
}

func init() { RegisterPublic("pairing", registerPairing) }

func registerPairing(api huma.API, deps Deps) {
	pk := deps.Passkey

	huma.Register(api, huma.Operation{
		OperationID: "mobilePairConsume",
		Method:      http.MethodPost,
		Path:        "/auth/pair",
		Summary:     "Exchanges a QR code pairing ticket for a passkey reg_token",
		Description: "Never returns a session. The reg_token only authorizes starting a passkey registration ceremony at /auth/passkey/register/begin, whose success still awaits approval in the panel.",
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
