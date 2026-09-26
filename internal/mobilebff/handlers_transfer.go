package mobilebff

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"syscall"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"server-control-panel/internal/files"
)

// registerTransfer registers the app's LARGE file transfer routes — download
// with Range (resumable) and chunked upload (init/chunk/complete), plus the
// discovery of the "inbox" directory that the share-target flow uses when the
// user does not pick an explicit destination. This is the backend for
// resumable download, resumable upload and share-target.
//
// All the I/O and session-protocol logic comes from
// internal/files.(OpenForRange|InitUpload|WriteChunk|CompleteUpload|
// SessionStatus|MobileInboxDir); all that lives here is HTTP translation.
func init() { Register("transfer", registerTransfer) }

type filesDownloadInput struct {
	Path string `query:"path" required:"true" doc:"Caminho absoluto do arquivo a baixar"`
}

type UploadInitRequest struct {
	DestDir   string `json:"dest_dir" doc:"Diretório de destino (já existente) onde o arquivo será gravado ao completar"`
	Filename  string `json:"filename" doc:"Nome do arquivo final; não pode conter '..' nem separador de caminho"`
	TotalSize int64  `json:"total_size" doc:"Tamanho total em bytes do arquivo a enviar"`
}

type uploadInitInput struct {
	Body UploadInitRequest
}

type UploadInitResponse struct {
	SessionID string `json:"session_id"`
}

type uploadInitOutput struct {
	Body UploadInitResponse
}

type uploadChunkInput struct {
	SessionID string `query:"session_id" required:"true"`
	Offset    int64  `query:"offset" required:"true" doc:"Posição, em bytes, de onde este pedaço começa no arquivo final"`
	RawBody   []byte `contentType:"application/octet-stream"`
}

type UploadChunkResponse struct {
	ReceivedBytes int64 `json:"received_bytes" doc:"Total de bytes já gravados de forma durável nesta sessão"`
}

type uploadChunkOutput struct {
	Body UploadChunkResponse
}

type UploadCompleteRequest struct {
	SessionID string `json:"session_id"`
}

type uploadCompleteInput struct {
	Body UploadCompleteRequest
}

type UploadCompleteResponse struct {
	OK   bool   `json:"ok"`
	Path string `json:"path"`
}

type uploadCompleteOutput struct {
	Body UploadCompleteResponse
}

// UploadIncompleteResponse is the error body returned with 409 when
// upload/complete is called before all the bytes have arrived — the app knows
// exactly how much is missing without needing a second status call.
type UploadIncompleteResponse struct {
	Reason        string `json:"error"`
	ReceivedBytes int64  `json:"received_bytes"`
	TotalSize     int64  `json:"total_size"`
}

func (e *UploadIncompleteResponse) Error() string  { return e.Reason }
func (e *UploadIncompleteResponse) GetStatus() int { return http.StatusConflict }

type InboxResponse struct {
	Path string `json:"path" doc:"Diretório onde um arquivo compartilhado por outro app pousa por padrão"`
}

type inboxOutput struct {
	Body InboxResponse
}

func registerTransfer(api huma.API, deps Deps) {
	var dataDir string
	if deps.Cfg != nil {
		dataDir = deps.Cfg.DataDir
	}

	incompleteSchema := api.OpenAPI().Components.Schemas.Schema(
		reflect.TypeOf(UploadIncompleteResponse{}), true, "UploadIncompleteResponse",
	)

	huma.Register(api, huma.Operation{
		OperationID: "downloadFile",
		Method:      http.MethodGet,
		Path:        "/files/download",
		Summary:     "Baixa um arquivo do servidor, com suporte a Range (download parcial/retomável)",
		Tags:        []string{"mobile"},
		Middlewares: huma.Middlewares{requireAuth},
		Errors:      []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusNotFound},
	}, func(_ context.Context, input *filesDownloadInput) (*huma.StreamResponse, error) {
		f, fi, err := files.OpenForRange(input.Path)
		if err != nil {
			return nil, mapTransferErr(err)
		}
		name := filepath.Base(input.Path)
		modTime := fi.ModTime()
		return &huma.StreamResponse{
			Body: func(ctx huma.Context) {
				defer f.Close()
				ctx.SetHeader("Content-Disposition", `attachment; filename="`+name+`"`)
				r, w := humago.Unwrap(ctx)
				http.ServeContent(w, r, name, modTime, f)
			},
		}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "initUpload",
		Method:      http.MethodPost,
		Path:        "/files/upload/init",
		Summary:     "Inicia uma sessão de upload em pedaços, reservando o destino final",
		Tags:        []string{"mobile"},
		Middlewares: huma.Middlewares{requireAuth},
		Errors:      []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusRequestEntityTooLarge},
	}, func(_ context.Context, input *uploadInitInput) (*uploadInitOutput, error) {
		sessionID, err := files.InitUpload(dataDir, input.Body.DestDir, input.Body.Filename, input.Body.TotalSize)
		if err != nil {
			return nil, mapTransferErr(err)
		}
		return &uploadInitOutput{Body: UploadInitResponse{SessionID: sessionID}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "uploadChunk",
		Method:      http.MethodPost,
		Path:        "/files/upload/chunk",
		Summary:     "Grava um pedaço do upload na posição indicada (offset), retomável fora de ordem",
		Tags:        []string{"mobile"},
		Middlewares: huma.Middlewares{requireAuth},
		Errors:      []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusNotFound},
	}, func(_ context.Context, input *uploadChunkInput) (*uploadChunkOutput, error) {
		received, err := files.WriteChunk(dataDir, input.SessionID, input.Offset, input.RawBody)
		if err != nil {
			return nil, mapTransferErr(err)
		}
		return &uploadChunkOutput{Body: UploadChunkResponse{ReceivedBytes: received}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "completeUpload",
		Method:      http.MethodPost,
		Path:        "/files/upload/complete",
		Summary:     "Finaliza o upload, movendo o arquivo montado para o destino; 409 se ainda faltam bytes",
		Tags:        []string{"mobile"},
		Middlewares: huma.Middlewares{requireAuth},
		Errors:      []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusNotFound},
		Responses: map[string]*huma.Response{
			"409": {
				Description: "Sessão incompleta: ainda faltam bytes para completar o upload",
				Content: map[string]*huma.MediaType{
					"application/json": {Schema: incompleteSchema},
				},
			},
		},
	}, func(_ context.Context, input *uploadCompleteInput) (*uploadCompleteOutput, error) {
		path, err := files.CompleteUpload(dataDir, input.Body.SessionID)
		if err != nil {
			if errors.Is(err, files.ErrIncomplete) {
				received, total, statusErr := files.SessionStatus(dataDir, input.Body.SessionID)
				if statusErr != nil {
					return nil, &UploadIncompleteResponse{Reason: "incomplete"}
				}
				return nil, &UploadIncompleteResponse{Reason: "incomplete", ReceivedBytes: received, TotalSize: total}
			}
			return nil, mapTransferErr(err)
		}
		return &uploadCompleteOutput{Body: UploadCompleteResponse{OK: true, Path: path}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "filesInbox",
		Method:      http.MethodGet,
		Path:        "/files/inbox",
		Summary:     "Devolve o diretório padrão onde um arquivo compartilhado por outro app pousa",
		Tags:        []string{"mobile"},
		Middlewares: huma.Middlewares{requireAuth},
		Errors:      []int{http.StatusUnauthorized},
	}, func(_ context.Context, _ *struct{}) (*inboxOutput, error) {
		path, err := files.MobileInboxDir(dataDir)
		if err != nil {
			return nil, huma.Error500InternalServerError(err.Error())
		}
		return &inboxOutput{Body: InboxResponse{Path: path}}, nil
	})
}

// mapTransferErr translates internal/files' errors into the matching HTTP
// status. files.ErrIncomplete is handled separately in completeUpload
// because it carries a body of its own (UploadIncompleteResponse). The
// sentinels (ErrSessionNotFound/ErrInvalidChunk/ErrTooLarge) have fixed
// messages owned by this package — no risk of leaking a server detail, which is
// why they keep echoing err.Error(). The branches below
// (os.IsNotExist/os.IsPermission/default) cover *os.PathError coming straight
// out of os.Stat/os.Rename and by their nature carry the server's absolute
// path — never echo err.Error() to the client here, the same stance as
// handlers_screens.go/handlers_actions.go: a generic message for the client,
// the full error only in the server log.
func mapTransferErr(err error) error {
	switch {
	case errors.Is(err, files.ErrSessionNotFound):
		return huma.Error404NotFound(err.Error())
	case errors.Is(err, files.ErrInvalidChunk):
		return huma.Error400BadRequest(err.Error())
	case errors.Is(err, files.ErrTooLarge):
		return huma.Error413RequestEntityTooLarge(err.Error())
	// Disk full. It used to fall into `default` and reach the app as a 400
	// "invalid request" — the app retried forever an upload that was never
	// going to fit, and the operator had no way to know the problem was space.
	// 507 is the status that exists precisely for this, and it is what lets the
	// app stop trying and say what needs to be done. No echo of err.Error():
	// an ENOSPC coming from os.WriteFile carries the server's absolute path.
	case errors.Is(err, syscall.ENOSPC):
		log.Printf("mobilebff: transfer out of disk space: %v", err)
		return huma.Error507InsufficientStorage("no space left on device")
	case os.IsNotExist(err):
		log.Printf("mobilebff: transfer not-found: %v", err)
		return huma.Error404NotFound("not found")
	// Permission denied is a condition of the REQUEST (the chosen folder is not
	// writable), not a server defect: as a 500 it told the app to retry
	// forever against a folder that is never going to accept the write.
	case os.IsPermission(err):
		log.Printf("mobilebff: transfer permission error: %v", err)
		return huma.Error403Forbidden("permission denied")
	default:
		log.Printf("mobilebff: transfer error: %v", err)
		return huma.Error400BadRequest("invalid request")
	}
}
