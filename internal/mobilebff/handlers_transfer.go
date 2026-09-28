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

func init() { Register("transfer", registerTransfer) }

type filesDownloadInput struct {
	Path string `query:"path" required:"true" doc:"Absolute path of the file to download"`
}

type UploadInitRequest struct {
	DestDir   string `json:"dest_dir" doc:"Destination directory (must exist) where the file is written on completion"`
	Filename  string `json:"filename" doc:"Final file name; may not contain '..' or a path separator"`
	TotalSize int64  `json:"total_size" doc:"Total size in bytes of the file to upload"`
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
	Offset    int64  `query:"offset" required:"true" doc:"Byte position where this chunk starts in the final file"`
	RawBody   []byte `contentType:"application/octet-stream"`
}

type UploadChunkResponse struct {
	ReceivedBytes int64 `json:"received_bytes" doc:"Total bytes already durably written in this session"`
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

type UploadIncompleteResponse struct {
	Reason        string `json:"error"`
	ReceivedBytes int64  `json:"received_bytes"`
	TotalSize     int64  `json:"total_size"`
}

func (e *UploadIncompleteResponse) Error() string  { return e.Reason }
func (e *UploadIncompleteResponse) GetStatus() int { return http.StatusConflict }

type InboxResponse struct {
	Path string `json:"path" doc:"Directory where a file shared from another app lands by default"`
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
		Summary:     "Downloads a file from the server, with Range support (partial/resumable download)",
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
		Summary:     "Starts a chunked upload session, reserving the final destination",
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
		Summary:     "Writes an upload chunk at the given offset; resumable and order-independent",
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
		Summary:     "Finishes the upload, moving the assembled file to its destination; 409 if bytes are still missing",
		Tags:        []string{"mobile"},
		Middlewares: huma.Middlewares{requireAuth},
		Errors:      []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusNotFound},
		Responses: map[string]*huma.Response{
			"409": {
				Description: "Incomplete session: bytes are still missing to complete the upload",
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
		Summary:     "Returns the default directory where a file shared from another app lands",
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

func mapTransferErr(err error) error {
	switch {
	case errors.Is(err, files.ErrSessionNotFound):
		return huma.Error404NotFound(err.Error())
	case errors.Is(err, files.ErrInvalidChunk):
		return huma.Error400BadRequest(err.Error())
	case errors.Is(err, files.ErrTooLarge):
		return huma.Error413RequestEntityTooLarge(err.Error())
	case errors.Is(err, syscall.ENOSPC):
		log.Printf("mobilebff: transfer out of disk space: %v", err)
		return huma.Error507InsufficientStorage("no space left on device")
	case os.IsNotExist(err):
		log.Printf("mobilebff: transfer not-found: %v", err)
		return huma.Error404NotFound("not found")
	case os.IsPermission(err):
		log.Printf("mobilebff: transfer permission error: %v", err)
		return huma.Error403Forbidden("permission denied")
	default:
		log.Printf("mobilebff: transfer error: %v", err)
		return huma.Error400BadRequest("invalid request")
	}
}
