package mobilebff

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/config"
	"server-control-panel/internal/httpx"
	"server-control-panel/internal/mobilebff/sdui"
)

// registerScreens registers GET /screens/{id} — the only route through which
// the app fetches an SDUI screen. One call returns the whole screen, already
// filtered server-side for the authenticated Viewer (see
// internal/mobilebff/sdui).
//
// An unregistered id and a registered id not permitted for this Viewer return
// exactly the same 404 — never a 403. A 403 confirms the screen exists and
// enables enumeration of the administrative surface; the indistinguishable 404
// is the same stance this project already uses for terminal sessions (see
// internal/api/handlers_terminal_backup.go).
func init() { Register("screens", registerScreens) }

// ScreenSection is a section offered to the authenticated user in the app's
// picker. Unlike the body of /screens/{id} — polymorphic and deliberately
// schema-less (see screenOutput) — this shape is CLOSED and small, so a
// generated DTO is desirable here: the app needs to draw groups and labels, and
// a new field here is a contract change that deserves to show up in the OpenAPI.
type ScreenSection struct {
	ID    string `json:"id" doc:"Id da tela, para GET /screens/{id}"`
	Group string `json:"group" doc:"Grupo do seletor (ex.: Docker, Sistema, Segurança)"`
	Label string `json:"label" doc:"Rótulo em português, legível fora do grupo"`
}

// ScreensResponse wraps the list under a key instead of returning a bare array,
// following the same convention every row-returning endpoint in this package
// already uses: a top-level object lets a field be added later without breaking
// existing consumers, something a top-level array never allows.
type ScreensResponse struct {
	Sections []ScreenSection `json:"sections"`
}

type screensListOutput struct {
	Body ScreensResponse
}

// registerScreensList registers GET /screens — the catalogue of sections THIS
// user can open. It is what makes the app's picker server-driven: a new screen
// registered on the server shows up on the phone with no release, which is the
// reason SDUI exists.
//
// The catalogue is a NAVIGATION HINT, never authorization. The real decision is
// still GET /screens/{id}, and a 404 there is normal behaviour — the person lost
// permission between the listing and the tap. The app treats that 404 as "this
// section is no longer available", not as an error.
//
// Filtering is by OMISSION (sdui.CatalogFor): what the user may not see simply
// does not come in the list, it never comes marked as unavailable — a disabled
// item would confirm the screen exists and would hand back the enumeration of
// the administrative surface that serveScreen's 404 denies.
func registerScreensList(api huma.API, deps Deps) {
	cfg := deps.Cfg
	huma.Register(api, huma.Operation{
		OperationID: "listScreens",
		Method:      http.MethodGet,
		Path:        "/screens",
		Summary:     "Seções SDUI disponíveis para o usuário autenticado, agrupadas",
		Tags:        []string{"mobile", "sdui"},
		Middlewares: huma.Middlewares{requireAuth},
	}, func(ctx context.Context, _ *struct{}) (*screensListOutput, error) {
		viewer := sdui.ViewerFrom(cfg, auth.UserFromContext(ctx))
		entries := sdui.CatalogFor(viewer)
		// Never nil: an empty catalogue must serialize as `[]`, not as `null` —
		// the Kotlin client distinguishes an empty list (show the empty state)
		// from a missing field (malformed payload).
		sections := make([]ScreenSection, 0, len(entries))
		for _, e := range entries {
			sections = append(sections, ScreenSection{ID: e.ID, Group: e.Group, Label: e.Label})
		}
		return &screensListOutput{Body: ScreensResponse{Sections: sections}}, nil
	})
}

func registerScreens(api huma.API, deps Deps) {
	registerScreensList(api, deps)

	cfg := deps.Cfg
	huma.Register(api, huma.Operation{
		OperationID: "getScreen",
		Method:      http.MethodGet,
		Path:        "/screens/{id}",
		Summary:     "Descritor de tela SDUI, filtrado por RBAC para o usuário autenticado",
		Tags:        []string{"mobile", "sdui"},
		// requireAuth (handlers_session.go) replies 401 without calling next()
		// when there is no authenticated user. serveScreen does the rest —
		// resolving the Viewer, assembling the screen and writing the response —
		// and it never calls next() either: the typed handler below exists only
		// so that huma has a pair of input/output types to generate the OpenAPI
		// documentation from (path parameter and response shape), not to
		// actually run.
		Middlewares: huma.Middlewares{requireAuth, serveScreen(cfg)},
	}, screenDocHandler)
}

// screenInput exists only so huma documents the {id} path parameter in the
// generated OpenAPI — serveScreen reads req.PathValue("id") directly off the
// raw request, never through this struct.
type screenInput struct {
	ID string `path:"id" doc:"Id da tela registrada em internal/mobilebff/sdui (ex.: scheduler.jobs)"`
}

// screenOutput declares the response body as free-form JSON
// (json.RawMessage becomes, in the OpenAPI schema huma generates, an empty
// schema — "accepts any JSON" — never an object with fixed properties).
// That is ON PURPOSE: the SDUI payload is polymorphic and forward-compatible
// (7 component types closed in the Go vocabulary, but each screen emits a
// different subset); generating a strongly typed Kotlin DTO from this schema
// would reintroduce exactly the "old client breaks on a new server field"
// failure that the SDUI design exists to avoid. android/data passes the raw
// body to the SDUI vocabulary's own kotlinx.serialization schema in the
// :core module, not to a generated DTO.
type screenOutput struct {
	Body json.RawMessage
}

// screenDocHandler never runs in production — serveScreen intercepts and writes
// the response on its own, without calling next(). It exists only to give
// huma.Register a concrete pair of types to infer the Operation above.
func screenDocHandler(ctx context.Context, in *screenInput) (*screenOutput, error) {
	return &screenOutput{Body: json.RawMessage("{}")}, nil
}

// serveScreen is the middleware that does all the real work of GET
// /screens/{id}: it resolves the authenticated Viewer, assembles the screen via
// sdui.Build and writes the response — always uncached (Cache-Control:
// no-store), because a descriptor is filtered per user and must never be served
// by an intermediary to somebody else.
func serveScreen(cfg *config.Config) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		req, w := humago.Unwrap(ctx)

		id := req.PathValue("id")
		username := auth.UserFrom(req)
		viewer := sdui.ViewerFrom(cfg, username)

		env, err := sdui.Build(req.Context(), id, viewer)
		if err != nil {
			if errors.Is(err, sdui.ErrScreenNotFound) {
				// An unknown id and an id not permitted for this Viewer fall into
				// the SAME branch — the indistinguishability is the defence against
				// enumeration, not an implementation detail.
				httpx.WriteErr(w, http.StatusNotFound, "screen_not_found")
				return
			}
			// Never echo err.Error() to the mobile client: it may contain a file
			// path, a config value or an internal detail of the builder. The full
			// error goes only to the server log.
			log.Printf("mobilebff: error building screen %q for %q: %v", id, username, err)
			httpx.WriteErr(w, http.StatusInternalServerError, "internal_error")
			return
		}

		body, err := json.Marshal(env)
		if err != nil {
			log.Printf("mobilebff: error serializing screen %q: %v", id, err)
			httpx.WriteErr(w, http.StatusInternalServerError, "internal_error")
			return
		}

		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}
}
