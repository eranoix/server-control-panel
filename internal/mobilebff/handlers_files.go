package mobilebff

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"reflect"

	"github.com/danielgtaylor/huma/v2"

	"server-control-panel/internal/files"
)

func init() { Register("files", registerFiles) }

type FileEntry struct {
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Mode     string `json:"mode,omitempty"`
	Modified int64  `json:"modified"`
	IsDir    bool   `json:"is_dir"`
	IsLink   bool   `json:"is_link,omitempty"`
	Target   string `json:"target,omitempty"`
}

type FileListResponse struct {
	Path    string      `json:"path"`
	Parent  string      `json:"parent"`
	Entries []FileEntry `json:"entries"`
}

type filesListInput struct {
	Path string `query:"path" required:"true" doc:"Absolute path of the directory to list"`
}

type filesListOutput struct {
	Body FileListResponse
}

type FileReadResponse struct {
	Content  string `json:"content"`
	Mtime    int64  `json:"mtime" doc:"File mtime on disk (unix); send it back in write.expected_mtime to detect conflicts"`
	Language string `json:"language" doc:"Language hint derived from the extension, for syntax highlighting"`
	Size     int64  `json:"size"`
}

type filesReadInput struct {
	Path string `query:"path" required:"true" doc:"Absolute path of the file to read"`
}

type filesReadOutput struct {
	Body FileReadResponse
}

type FileWriteRequest struct {
	Path          string `json:"path"`
	Content       string `json:"content"`
	ExpectedMtime int64  `json:"expected_mtime,omitempty" doc:"mtime read before editing; 0 or omitted = no previous read, writes unconditionally"`
}

type filesWriteInput struct {
	Body FileWriteRequest
}

type FileWriteResponse struct {
	OK    bool  `json:"ok"`
	Mtime int64 `json:"mtime" doc:"New file mtime after the write"`
}

type filesWriteOutput struct {
	Body FileWriteResponse
}

type FileConflictResponse struct {
	Reason        string `json:"error"`
	ServerContent string `json:"server_content"`
	ServerMtime   int64  `json:"server_mtime"`
}

func (e *FileConflictResponse) Error() string  { return e.Reason }
func (e *FileConflictResponse) GetStatus() int { return http.StatusConflict }

func registerFiles(api huma.API, deps Deps) {
	registry := api.OpenAPI().Components.Schemas
	conflictSchema := registry.Schema(reflect.TypeOf(FileConflictResponse{}), true, "FileConflictResponse")

	huma.Register(api, huma.Operation{
		OperationID: "listFiles",
		Method:      http.MethodGet,
		Path:        "/files/list",
		Summary:     "Lists the contents of a directory on the server",
		Tags:        []string{"mobile"},
		Middlewares: huma.Middlewares{requireAuth},
		Errors:      []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusNotFound},
	}, listFilesHandler)

	huma.Register(api, huma.Operation{
		OperationID: "readFile",
		Method:      http.MethodGet,
		Path:        "/files/read",
		Summary:     "Reads a text file from the server with a language hint for highlighting",
		Tags:        []string{"mobile"},
		Middlewares: huma.Middlewares{requireAuth},
		Errors: []int{
			http.StatusBadRequest, http.StatusUnauthorized, http.StatusNotFound,
			http.StatusRequestEntityTooLarge, http.StatusUnsupportedMediaType,
		},
	}, readFileHandler)

	huma.Register(api, huma.Operation{
		OperationID: "writeFile",
		Method:      http.MethodPost,
		Path:        "/files/write",
		Summary:     "Writes a file; rejects with 409 if the file changed on disk since it was read",
		Tags:        []string{"mobile"},
		Middlewares: huma.Middlewares{requireAuth},
		Errors:      []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusNotFound},
		Responses: map[string]*huma.Response{
			"409": {
				Description: "Conflict: the file changed on disk since the last read",
				Content: map[string]*huma.MediaType{
					"application/json": {Schema: conflictSchema},
				},
			},
		},
	}, writeFileHandler)
}

func listFilesHandler(_ context.Context, input *filesListInput) (*filesListOutput, error) {
	res, err := files.MobileList(input.Path)
	if err != nil {
		return nil, mapFileErr(err)
	}
	entries := make([]FileEntry, 0, len(res.Entries))
	for _, e := range res.Entries {
		entries = append(entries, FileEntry{
			Name:     e.Name,
			Size:     e.Size,
			Mode:     e.Mode,
			Modified: e.Modified,
			IsDir:    e.IsDir,
			IsLink:   e.IsLink,
			Target:   e.Target,
		})
	}
	return &filesListOutput{Body: FileListResponse{Path: res.Path, Parent: res.Parent, Entries: entries}}, nil
}

func readFileHandler(_ context.Context, input *filesReadInput) (*filesReadOutput, error) {
	res, err := files.MobileRead(input.Path)
	if err != nil {
		return nil, mapFileErr(err)
	}
	return &filesReadOutput{Body: FileReadResponse{
		Content:  res.Content,
		Mtime:    res.Mtime,
		Language: res.Language,
		Size:     res.Size,
	}}, nil
}

func writeFileHandler(_ context.Context, input *filesWriteInput) (*filesWriteOutput, error) {
	mtime, err := files.MobileWrite(input.Body.Path, input.Body.Content, input.Body.ExpectedMtime)
	if err != nil {
		if errors.Is(err, files.ErrConflict) {
			current, readErr := files.MobileRead(input.Body.Path)
			if readErr != nil {
				return nil, &FileConflictResponse{Reason: "conflict"}
			}
			return nil, &FileConflictResponse{
				Reason:        "conflict",
				ServerContent: current.Content,
				ServerMtime:   current.Mtime,
			}
		}
		return nil, mapFileErr(err)
	}
	return &filesWriteOutput{Body: FileWriteResponse{OK: true, Mtime: mtime}}, nil
}

func mapFileErr(err error) error {
	switch {
	case errors.Is(err, files.ErrBinary):
		return huma.Error415UnsupportedMediaType(err.Error())
	case errors.Is(err, files.ErrTooLarge):
		return huma.Error413RequestEntityTooLarge(err.Error())
	case os.IsNotExist(err):
		log.Printf("mobilebff: files not-found: %v", err)
		return huma.Error404NotFound("not found")
	case os.IsPermission(err):
		log.Printf("mobilebff: files permission error: %v", err)
		return huma.Error500InternalServerError("internal error")
	default:
		log.Printf("mobilebff: files error: %v", err)
		return huma.Error400BadRequest("invalid request")
	}
}
