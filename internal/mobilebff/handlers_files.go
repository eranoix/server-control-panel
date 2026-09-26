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

// registerFiles registers the three routes of the app's file editor — the
// backend for listing, opening (with a language hint) and editing/saving
// without a silent overwrite. This file holds no path-validation logic and
// no file I/O of its own: all of that comes from
// internal/files.Mobile(List|Read|Write), which already reuses the same
// denylist (validatePath) as the desktop panel. All that lives here is HTTP
// translation: parsing the input, calling the domain package directly, and
// mapping errors to statuses.
func init() { Register("files", registerFiles) }

// FileEntry mirrors the fields of internal/files' `entry` type (unexported,
// which is why it cannot be referenced by name here) that matter to the app:
// name, size, whether it is a directory, and mtime. Mode/IsLink/Target are
// preserved when present so the app can show a link icon and the
// "(protegido)" warning the desktop panel already computes.
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
	Path string `query:"path" required:"true" doc:"Caminho absoluto do diretório a listar"`
}

type filesListOutput struct {
	Body FileListResponse
}

type FileReadResponse struct {
	Content  string `json:"content"`
	Mtime    int64  `json:"mtime" doc:"mtime do arquivo em disco (unix) — devolver em write.expected_mtime para detectar conflito"`
	Language string `json:"language" doc:"Hint de linguagem derivado da extensão, para syntax highlighting"`
	Size     int64  `json:"size"`
}

type filesReadInput struct {
	Path string `query:"path" required:"true" doc:"Caminho absoluto do arquivo a ler"`
}

type filesReadOutput struct {
	Body FileReadResponse
}

type FileWriteRequest struct {
	Path          string `json:"path"`
	Content       string `json:"content"`
	ExpectedMtime int64  `json:"expected_mtime,omitempty" doc:"mtime lido antes da edição; 0 ou omitido = sem leitura prévia, grava incondicionalmente"`
}

type filesWriteInput struct {
	Body FileWriteRequest
}

type FileWriteResponse struct {
	OK    bool  `json:"ok"`
	Mtime int64 `json:"mtime" doc:"Novo mtime do arquivo após a escrita"`
}

type filesWriteOutput struct {
	Body FileWriteResponse
}

// FileConflictResponse is the error body returned with 409 when the
// expected_mtime sent does not match the file's current mtime on disk — the
// mechanism that prevents a silent overwrite. It implements
// huma.StatusError (Error/GetStatus) so that huma.Register serves this type
// as the response body when the handler returns it as an error; the server
// has already re-read the current content from disk (files.MobileRead) so
// the app can offer reload/overwrite/cancel without a second request.
type FileConflictResponse struct {
	Reason        string `json:"error"`
	ServerContent string `json:"server_content"`
	ServerMtime   int64  `json:"server_mtime"`
}

func (e *FileConflictResponse) Error() string  { return e.Reason }
func (e *FileConflictResponse) GetStatus() int { return http.StatusConflict }

func registerFiles(api huma.API, deps Deps) {
	// The 409 body schema is registered by hand in Components — the same
	// mechanism huma uses internally to document the default ErrorModel
	// (defineErrors in huma.go), only for our own type rather than the
	// generic one, because the conflict body carries server_content/
	// server_mtime and ErrorModel has no field to hold them.
	registry := api.OpenAPI().Components.Schemas
	conflictSchema := registry.Schema(reflect.TypeOf(FileConflictResponse{}), true, "FileConflictResponse")

	huma.Register(api, huma.Operation{
		OperationID: "listFiles",
		Method:      http.MethodGet,
		Path:        "/files/list",
		Summary:     "Lista o conteúdo de um diretório do servidor",
		Tags:        []string{"mobile"},
		Middlewares: huma.Middlewares{requireAuth},
		Errors:      []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusNotFound},
	}, listFilesHandler)

	huma.Register(api, huma.Operation{
		OperationID: "readFile",
		Method:      http.MethodGet,
		Path:        "/files/read",
		Summary:     "Lê um arquivo de texto do servidor com hint de linguagem para highlight",
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
		Summary:     "Grava um arquivo; rejeita com 409 se o arquivo mudou em disco desde a leitura",
		Tags:        []string{"mobile"},
		Middlewares: huma.Middlewares{requireAuth},
		Errors:      []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusNotFound},
		Responses: map[string]*huma.Response{
			"409": {
				Description: "Conflito: o arquivo mudou em disco desde a última leitura",
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
			// One extra round-trip, deliberately: re-read the current content
			// from disk so the app does not need a second request just to build
			// the "reload / overwrite / cancel" screen.
			current, readErr := files.MobileRead(input.Body.Path)
			if readErr != nil {
				// The file vanished between the conflict check and this re-read (or
				// became binary/too large) — it is still a conflict, we just cannot
				// attach the current content.
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

// mapFileErr translates internal/files' errors (sentinels + os.IsNotExist)
// into the matching HTTP status. files.ErrConflict is handled separately in
// writeFileHandler because it carries a body of its own
// (FileConflictResponse). The sentinels (ErrBinary/ErrTooLarge) have fixed
// messages owned by this package — no risk of leaking a server detail. The
// branches below cover *os.PathError coming straight out of os.Stat/os.Open
// and by their nature carry the server's absolute path — never echo
// err.Error() to the client here, the same stance as
// handlers_screens.go/handlers_actions.go: a generic message for the client,
// the full error only in the server log.
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
