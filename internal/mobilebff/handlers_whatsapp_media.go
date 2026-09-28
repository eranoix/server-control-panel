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

const maxMediaUploadBytes = 100 << 20

func init() {
	Register("whatsapp-media", registerWhatsappMedia)
	httpmw.RegisterLargeBody(isWhatsAppMediaUpload, maxMediaUploadBytes)
}

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
			return false
		}
	}
	return true
}

type mediaGetInput struct {
	JID   string `path:"jid"`
	MsgID string `path:"msg_id"`
}

type UploadMediaForm struct {
	File        huma.FormFile `form:"file" required:"true" doc:"Media file to send"`
	ClientMsgID string        `form:"client_msg_id" required:"true" doc:"Client-generated ID; resending with the same id does not duplicate the send"`
	Caption     string        `form:"caption" required:"false" doc:"Optional caption"`
	QuotedID    string        `form:"quoted_id" required:"false" doc:"ID of the quoted message, if any"`
	MsgType     string        `form:"msg_type" required:"false" doc:"image|video|audio|document; inferred from the mime/name when empty"`
}

type mediaUploadInput struct {
	JID     string `path:"jid"`
	RawBody huma.MultipartFormFiles[UploadMediaForm]
}

type UploadMediaResponse struct {
	ID string `json:"id"`
}

type mediaUploadOutput struct {
	Body UploadMediaResponse
}

func registerWhatsappMedia(api huma.API, deps Deps) {
	registerWhatsappMediaWithResolver(api, managerResolver(deps.WhatsAppMgr))
}

func registerWhatsappMediaWithResolver(api huma.API, resolve whatsappResolver) {
	huma.Register(api, huma.Operation{
		OperationID: "getWhatsAppMedia",
		Method:      http.MethodGet,
		Path:        "/whatsapp/chats/{jid}/media/{msg_id}",
		Summary:     "Downloads (on demand on a cache miss) and serves a media item's bytes, with Range support",
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
		Summary:     "Sends a media file; idempotent by client_msg_id, 100 MiB cap (same as the panel)",
		Tags:        []string{"mobile", "whatsapp"},
		Middlewares: huma.Middlewares{requireAuth},
		Errors: []int{
			http.StatusBadRequest, http.StatusUnauthorized, http.StatusUnprocessableEntity,
			http.StatusServiceUnavailable, http.StatusBadGateway,
		},
	}, mediaUploadHandler(resolve))
}

func mediaGetHandler(resolve whatsappResolver) func(ctx context.Context, input *mediaGetInput) (*huma.StreamResponse, error) {
	return func(ctx context.Context, input *mediaGetInput) (*huma.StreamResponse, error) {
		svc, err := resolve(ctx)
		if err != nil {
			return nil, err
		}
		rel, _, _, _, err := svc.DownloadMediaForMessage(input.JID, input.MsgID)
		if err != nil {
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
		data, err := io.ReadAll(io.LimitReader(form.File, maxMediaUploadBytes))
		if err != nil {
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
