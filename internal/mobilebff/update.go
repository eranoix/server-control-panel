package mobilebff

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"strconv"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"server-control-panel/internal/androidupdate"
)

func init() { Register("update", registerUpdate) }

type AppUpdateRelease struct {
	VersionName string `json:"version_name"`
	VersionCode int64  `json:"version_code"`
	SHA256      string `json:"sha256" doc:"SHA-256 (lowercase hex) of this version's signed APK"`
	SizeBytes   int64  `json:"size_bytes" doc:"Size of the reconstructed signed APK, not of the downloaded artifact"`
}

type AppUpdateArtifact struct {
	URL       string `json:"url" doc:"Absolute path of the bytes route (accepts Range, resumable download)"`
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256" doc:"SHA-256 (lowercase hex) of the .hdiff file itself"`
}

type AppUpdateResponse struct {
	Latest    AppUpdateRelease   `json:"latest"`
	UpToDate  bool               `json:"up_to_date" doc:"true when base_sha256 is already the newest version's APK"`
	Patch     *AppUpdateArtifact `json:"patch,omitempty" doc:"Incremental patch from base_sha256; ABSENT when there is no patch for that exact base"`
	Full      AppUpdateArtifact  `json:"full" doc:"Full reconstruction (hpatchz with an empty base); always available"`
	PatchTool string             `json:"patch_tool" doc:"Tool and options that produced the artifacts, for diagnosing incompatibilities"`
}

type appUpdateInput struct {
	BaseSHA256 string `query:"base_sha256" doc:"SHA-256 (hex) of the APK installed on the device; omitting it returns only the full path"`
}

type appUpdateOutput struct {
	Body AppUpdateResponse
}

type appUpdateArtifactInput struct {
	File string `query:"file" required:"true" doc:"The artifact's 'file' field, exactly as it came in the manifest"`
}

const artifactPath = "/app/update/artifact"

func registerUpdate(api huma.API, deps Deps) {
	var dataDir string
	if deps.Cfg != nil {
		dataDir = deps.Cfg.DataDir
	}

	huma.Register(api, huma.Operation{
		OperationID: "getAppUpdate",
		Method:      http.MethodGet,
		Path:        "/app/update",
		Summary:     "App update manifest: incremental patch for the given base, or a full reconstruction",
		Tags:        []string{"mobile"},
		Middlewares: huma.Middlewares{requireAuth},
		Errors:      []int{http.StatusUnauthorized, http.StatusServiceUnavailable},
	}, func(_ context.Context, input *appUpdateInput) (*appUpdateOutput, error) {
		m, err := androidupdate.Load(dataDir)
		if err != nil {
			return nil, mapUpdateErr(err)
		}
		resp := AppUpdateResponse{
			Latest: AppUpdateRelease{
				VersionName: m.Latest.VersionName,
				VersionCode: m.Latest.VersionCode,
				SHA256:      m.Latest.SHA256,
				SizeBytes:   m.Latest.SizeBytes,
			},
			UpToDate:  m.UpToDate(input.BaseSHA256),
			Full:      artifactDTO(m.Full),
			PatchTool: m.PatchTool,
		}
		if p := m.PatchFor(input.BaseSHA256); p != nil {
			dto := artifactDTO(*p)
			resp.Patch = &dto
		}
		return &appUpdateOutput{Body: resp}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "downloadAppUpdateArtifact",
		Method:      http.MethodGet,
		Path:        artifactPath,
		Summary:     "Downloads an update artifact (.hdiff), with Range support for resumable downloads",
		Tags:        []string{"mobile"},
		Middlewares: huma.Middlewares{requireAuth},
		Errors:      []int{http.StatusUnauthorized, http.StatusNotFound, http.StatusServiceUnavailable},
	}, func(_ context.Context, input *appUpdateArtifactInput) (*huma.StreamResponse, error) {
		f, fi, art, err := androidupdate.OpenArtifact(dataDir, input.File)
		if err != nil {
			return nil, mapUpdateErr(err)
		}
		return &huma.StreamResponse{
			Body: func(ctx huma.Context) {
				defer f.Close()
				ctx.SetHeader("Content-Type", "application/octet-stream")
				ctx.SetHeader("ETag", strconv.Quote(art.SHA256))
				ctx.SetHeader("Cache-Control", "private, max-age=31536000, immutable")
				ctx.SetHeader("Content-Disposition", "attachment; filename="+strconv.Quote(filenameFor(art)))
				r, w := humago.Unwrap(ctx)
				http.ServeContent(w, r, art.File, fi.ModTime(), f)
			},
		}, nil
	})
}

func filenameFor(a *androidupdate.Artifact) string {
	name := a.File
	for i := len(name) - 1; i >= 0; i-- {
		if name[i] == '/' {
			return name[i+1:]
		}
	}
	return name
}

func artifactDTO(a androidupdate.Artifact) AppUpdateArtifact {
	return AppUpdateArtifact{
		URL:       Prefix + artifactPath + "?file=" + urlQueryEscape(a.File),
		SizeBytes: a.SizeBytes,
		SHA256:    a.SHA256,
	}
}

func urlQueryEscape(s string) string {
	const hex = "0123456789ABCDEF"
	out := make([]byte, 0, len(s)+8)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == '.' || c == '~' {
			out = append(out, c)
			continue
		}
		out = append(out, '%', hex[c>>4], hex[c&0x0f])
	}
	return string(out)
}

func mapUpdateErr(err error) error {
	switch {
	case errors.Is(err, androidupdate.ErrNoManifest):
		return huma.Error503ServiceUnavailable("update channel not published yet")
	case errors.Is(err, os.ErrNotExist):
		return huma.Error404NotFound("update artifact not found")
	default:
		log.Printf("mobilebff: update error: %v", err)
		return huma.Error503ServiceUnavailable("update channel unavailable")
	}
}
