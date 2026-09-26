package mobilebff

// update.go — the Android app's incremental update channel: a manifest that
// says what to download, and a RESUMABLE byte route that delivers the
// artifact.
//
// WHY THIS IS AUTHENTICATED
// It is not encryption and does not pretend to be: the APK signature is still
// the final door deciding what Android agrees to install. The reason for
// serving it behind the BFF session is another one — a binary patch IS a map of
// which parts of the code changed between two versions, and the repository is
// private. Publishing the patch openly would hand a source diff for free to
// people with no access to it. Encrypting at rest would solve none of that
// (whoever has a session already has the key); taking it out of the anonymous
// path does.
//
// WHY THE BYTE ROUTE USES http.ServeContent
// The server owner's internet connection is bad. A 10 MB download that restarts
// from zero on every drop never finishes. ServeContent gives you, for free and
// correctly, Range/If-Range/If-None-Match/206 — that is, real resumption: the
// app asks for "Range: bytes=<already downloaded>-" and carries on from where
// it stopped. Writing that by hand (Content-Length + io.Copy) is precisely what
// does NOT resume.

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

// AppUpdateRelease is the newest published version. SHA256 is the hash of the
// SIGNED APK — the same value the app computes from the file it has installed in
// order to identify itself in base_sha256.
type AppUpdateRelease struct {
	VersionName string `json:"version_name"`
	VersionCode int64  `json:"version_code"`
	SHA256      string `json:"sha256" doc:"SHA-256 (hex minúsculo) do APK assinado desta versão"`
	SizeBytes   int64  `json:"size_bytes" doc:"Tamanho do APK assinado reconstruído, não do artefato baixado"`
}

// AppUpdateArtifact is one .hdiff file to download. SizeBytes/SHA256 describe
// the ARTIFACT (what travels), not the APK it reconstructs — they are what the
// app uses to show progress and to verify the download before applying it.
type AppUpdateArtifact struct {
	URL       string `json:"url" doc:"Caminho absoluto da rota de bytes (aceita Range, download retomável)"`
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256" doc:"SHA-256 (hex minúsculo) do próprio arquivo .hdiff"`
}

// AppUpdateResponse is the manifest's response.
//
// Patch is absent (null) whenever there is no patch generated from exactly the
// base that was supplied — unknown base, a version outside the retention
// window, or no base_sha256 sent at all. Full is ALWAYS present: it is the path
// that works from any state, and it uses the same hpatchz with an empty base, so
// the app has a single code path for both cases.
type AppUpdateResponse struct {
	Latest    AppUpdateRelease   `json:"latest"`
	UpToDate  bool               `json:"up_to_date" doc:"true quando base_sha256 já é o APK da versão mais nova"`
	Patch     *AppUpdateArtifact `json:"patch,omitempty" doc:"Patch incremental a partir de base_sha256; AUSENTE quando não há patch para essa base exata"`
	Full      AppUpdateArtifact  `json:"full" doc:"Reconstrução completa (hpatchz com base vazia); sempre disponível"`
	PatchTool string             `json:"patch_tool" doc:"Ferramenta e opções que geraram os artefatos, para diagnóstico de incompatibilidade"`
}

type appUpdateInput struct {
	BaseSHA256 string `query:"base_sha256" doc:"SHA-256 (hex) do APK instalado no aparelho; omitir devolve só o caminho completo"`
}

type appUpdateOutput struct {
	Body AppUpdateResponse
}

type appUpdateArtifactInput struct {
	File string `query:"file" required:"true" doc:"Campo 'file' do artefato, exatamente como veio no manifesto"`
}

// artifactPath is the byte route. It sits under the BFF's same Prefix, and
// therefore under the same authentication middleware as every route above.
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
		Summary:     "Manifesto de atualização do app: patch incremental para a base informada, ou reconstrução completa",
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
		Summary:     "Baixa um artefato de atualização (.hdiff), com suporte a Range — download retomável",
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
				// The artifact's name ALREADY is its content (it is derived
				// from the SHA-256 of base and target), so the file is
				// immutable: once downloaded, it never changes content under
				// the same name. Hence the aggressive cache and the strong
				// ETag — which is what lets the client's If-Range safely resume
				// a download interrupted days earlier.
				ctx.SetHeader("ETag", strconv.Quote(art.SHA256))
				ctx.SetHeader("Cache-Control", "private, max-age=31536000, immutable")
				ctx.SetHeader("Content-Disposition", "attachment; filename="+strconv.Quote(filenameFor(art)))
				r, w := humago.Unwrap(ctx)
				// ServeContent is what does the work: it negotiates Range,
				// answers 206 with Content-Range, honours If-Range/
				// If-None-Match and returns 416 on an impossible range.
				http.ServeContent(w, r, art.File, fi.ModTime(), f)
			},
		}, nil
	})
}

// filenameFor returns only the artifact's base name, without the directory —
// the File field is "patches/<a>-<b>.hdiff", and a Content-Disposition with a
// slash in it confuses clients.
func filenameFor(a *androidupdate.Artifact) string {
	nome := a.File
	for i := len(nome) - 1; i >= 0; i-- {
		if nome[i] == '/' {
			return nome[i+1:]
		}
	}
	return nome
}

func artifactDTO(a androidupdate.Artifact) AppUpdateArtifact {
	return AppUpdateArtifact{
		URL:       Prefix + artifactPath + "?file=" + urlQueryEscape(a.File),
		SizeBytes: a.SizeBytes,
		SHA256:    a.SHA256,
	}
}

// urlQueryEscape escapes the `file` parameter's value. It uses the same rule as
// net/url, but avoids importing url just for this in a file that is already the
// only place that builds that URL.
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

// mapUpdateErr translates the domain's errors into HTTP statuses.
//
// ErrSemManifesto becomes 503, not 404, on purpose: "no update channel has been
// published yet" is a transient state of the SERVER (no release has gone through
// scripts/android-patches.sh yet), not a resource the client asked for wrongly.
// The app tells the two apart and does not show "update not found" to an
// operator who simply has not published anything yet.
//
// No branch echoes err.Error() back to the client: the disk errors here carry
// absolute server paths. A generic message in the response, the full error only
// in the log — the same stance as mapTransferErr.
func mapUpdateErr(err error) error {
	switch {
	case errors.Is(err, androidupdate.ErrSemManifesto):
		return huma.Error503ServiceUnavailable("update channel not published yet")
	case errors.Is(err, os.ErrNotExist):
		return huma.Error404NotFound("update artifact not found")
	default:
		log.Printf("mobilebff: update error: %v", err)
		return huma.Error503ServiceUnavailable("update channel unavailable")
	}
}
