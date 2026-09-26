package mobilebff

import (
	"context"
	"io"
	"log"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"server-control-panel/internal/httpmw"
)

// maxMediaUploadBytes is the mobile BFF's media upload cap — the same cap the
// panel uses in handleSendFile (see
// internal/whatsapp/handlers_messages.go). httpmw.MaxBody applies 25 MiB by
// default on every route; without the RegisterLargeBody in init() below, a real
// media upload (a photo, a short video) would blow up well before the cap the
// product promises — see the comment on RegisterLargeBody for why that cannot
// be solved inside the handler itself.
const maxMediaUploadBytes = 100 << 20

// registerWhatsappMedia registers WhatsApp's two media routes: serve (with
// on-demand download on a cache miss) and send. No domain logic lives here —
// each handler delegates straight to
// internal/whatsapp/service_export.go, the same wrappers
// handlers_whatsapp.go already uses for text.
func init() {
	Register("whatsapp-media", registerWhatsappMedia)
	httpmw.RegisterLargeBody(isWhatsAppMediaUpload, maxMediaUploadBytes)
}

// isWhatsAppMediaUpload matches exactly POST {Prefix}/whatsapp/chats/<jid>/media
// — the only BFF route that needs the 100 MiB cap. Compared segment by segment
// (not a plain HasPrefix) so as not to accidentally widen the cap for some
// future route starting with the same prefix, nor confuse it with the download
// route .../media/<msg_id> (which is a GET with no body, but it costs nothing to
// be explicit rather than relying on the Method check alone).
func isWhatsAppMediaUpload(r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}
	const prefix = Prefix + "/whatsapp/chats/"
	const suffix = "/media"
	p := r.URL.Path
	if len(p) <= len(prefix)+len(suffix) || p[:len(prefix)] != prefix || p[len(p)-len(suffix):] != suffix {
		return false
	}
	middle := p[len(prefix) : len(p)-len(suffix)]
	if middle == "" {
		return false
	}
	for i := 0; i < len(middle); i++ {
		if middle[i] == '/' {
			return false // would be .../media/<something>, not the upload route
		}
	}
	return true
}

type mediaGetInput struct {
	JID   string `path:"jid"`
	MsgID string `path:"msg_id"`
}

// UploadMediaForm is the multipart body of POST .../media. File is required;
// ClientMsgID guarantees idempotency (a network retry does not duplicate the
// send) — the same contract sendMessageHandler already uses for text.
type UploadMediaForm struct {
	File        huma.FormFile `form:"file" required:"true" doc:"Arquivo de mídia a enviar"`
	ClientMsgID string        `form:"client_msg_id" required:"true" doc:"ID gerado pelo cliente; reenviar com o mesmo id não duplica o envio"`
	Caption     string        `form:"caption" required:"false" doc:"Legenda opcional"`
	QuotedID    string        `form:"quoted_id" required:"false" doc:"ID da mensagem citada, se houver"`
	MsgType     string        `form:"msg_type" required:"false" doc:"image|video|audio|document; inferido do mime/nome quando vazio"`
}

type mediaUploadInput struct {
	JID     string `path:"jid"`
	RawBody huma.MultipartFormFiles[UploadMediaForm]
}

// UploadMediaResponse returns only the id — the same minimal shape
// SendMessageResponse already uses for text; the app gets the rest of the
// metadata (mime, size, url) on the next message poll, via MediaView.
type UploadMediaResponse struct {
	ID string `json:"id"`
}

type mediaUploadOutput struct {
	Body UploadMediaResponse
}

func registerWhatsappMedia(api huma.API, deps Deps) {
	registerWhatsappMediaWithResolver(api, managerResolver(deps.WhatsAppMgr))
}

// registerWhatsappMediaWithResolver exists separately from registerWhatsappMedia
// for the same reason as registerWhatsappWithResolver: tests inject a fake
// whatsappResolver without needing a real *whatsapp.Manager.
func registerWhatsappMediaWithResolver(api huma.API, resolve whatsappResolver) {
	huma.Register(api, huma.Operation{
		OperationID: "getWhatsAppMedia",
		Method:      http.MethodGet,
		Path:        "/whatsapp/chats/{jid}/media/{msg_id}",
		Summary:     "Baixa (sob demanda em cache miss) e serve os bytes de uma mídia, com suporte a Range",
		Tags:        []string{"mobile", "whatsapp"},
		Middlewares: huma.Middlewares{requireAuth},
		Errors: []int{
			http.StatusBadRequest, http.StatusUnauthorized, http.StatusNotFound,
			http.StatusBadGateway, http.StatusServiceUnavailable,
		},
	}, mediaGetHandler(resolve))

	huma.Register(api, huma.Operation{
		OperationID: "uploadWhatsAppMedia",
		Method:      http.MethodPost,
		Path:        "/whatsapp/chats/{jid}/media",
		Summary:     "Envia um arquivo de mídia; idempotente por client_msg_id, teto de 100 MiB (mesmo do painel)",
		Tags:        []string{"mobile", "whatsapp"},
		Middlewares: huma.Middlewares{requireAuth},
		Errors: []int{
			http.StatusBadRequest, http.StatusUnauthorized, http.StatusUnprocessableEntity,
			http.StatusServiceUnavailable, http.StatusBadGateway,
		},
	}, mediaUploadHandler(resolve))
}

// mediaGetHandler resolves the cache miss BEFORE returning the StreamResponse:
// DownloadMediaForMessage (singleflight inside) may block on the network if
// the media is not local yet, and only after that does ServeMediaRel step in
// for the real Range/206/416 — the same pattern as downloadFile in
// handlers_transfer.go and avatarHandler in handlers_whatsapp.go.
func mediaGetHandler(resolve whatsappResolver) func(ctx context.Context, input *mediaGetInput) (*huma.StreamResponse, error) {
	return func(ctx context.Context, input *mediaGetInput) (*huma.StreamResponse, error) {
		svc, err := resolve(ctx)
		if err != nil {
			return nil, err
		}
		rel, _, _, _, err := svc.DownloadMediaForMessage(input.JID, input.MsgID)
		if err != nil {
			// *whatsapp.DownloadMediaError implements huma.StatusError — returned
			// directly, without rewriting the status mapping here.
			return nil, err
		}
		return &huma.StreamResponse{
			Body: func(hctx huma.Context) {
				r, w := humago.Unwrap(hctx)
				svc.ServeMediaRel(w, r, rel)
			},
		}, nil
	}
}

func mediaUploadHandler(resolve whatsappResolver) func(ctx context.Context, input *mediaUploadInput) (*mediaUploadOutput, error) {
	return func(ctx context.Context, input *mediaUploadInput) (*mediaUploadOutput, error) {
		svc, err := resolve(ctx)
		if err != nil {
			return nil, err
		}
		form := input.RawBody.Data()
		if !form.File.IsSet {
			return nil, huma.Error400BadRequest("file is required")
		}
		// The same per-file cap the panel applies in handleSendFile
		// (io.LimitReader(...,100<<20)) — the whole request body is already
		// limited to maxMediaUploadBytes by httpmw.MaxBody (via
		// RegisterLargeBody above); this LimitReader is the belt-and-braces
		// against a multipart with several fields whose sum exceeds the size of
		// ONE file, not a second line of defence against the same
		// attack.
		data, err := io.ReadAll(io.LimitReader(form.File, maxMediaUploadBytes))
		if err != nil {
			// Never echo err.Error() to the client — the same stance as
			// handlers_screens.go/handlers_actions.go.
			log.Printf("mobilebff: error reading the media file for %q: %v", input.JID, err)
			return nil, huma.Error500InternalServerError("internal error")
		}
		id, err := svc.SendFileDedup(
			input.JID, form.MsgType, form.File.Filename, form.File.ContentType,
			form.Caption, form.QuotedID, form.ClientMsgID, data,
		)
		if err != nil {
			log.Printf("mobilebff: error sending media to %q: %v", input.JID, err)
			return nil, huma.Error502BadGateway("upstream error")
		}
		return &mediaUploadOutput{Body: UploadMediaResponse{ID: id}}, nil
	}
}
