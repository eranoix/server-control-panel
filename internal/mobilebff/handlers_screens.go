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

func init() { Register("screens", registerScreens) }

type ScreenSection struct {
	ID    string `json:"id" doc:"Screen id, for GET /screens/{id}"`
	Group string `json:"group" doc:"Picker group (e.g. Docker, System, Security)"`
	Label string `json:"label" doc:"Human-readable label, understandable outside its group"`
}

type ScreensResponse struct {
	Sections []ScreenSection `json:"sections"`
}

type screensListOutput struct {
	Body ScreensResponse
}

func registerScreensList(api huma.API, deps Deps) {
	cfg := deps.Cfg
	huma.Register(api, huma.Operation{
		OperationID: "listScreens",
		Method:      http.MethodGet,
		Path:        "/screens",
		Summary:     "SDUI sections available to the authenticated user, grouped",
		Tags:        []string{"mobile", "sdui"},
		Middlewares: huma.Middlewares{requireAuth},
	}, func(ctx context.Context, _ *struct{}) (*screensListOutput, error) {
		viewer := sdui.ViewerFrom(cfg, auth.UserFromContext(ctx))
		entries := sdui.CatalogFor(viewer)
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
		Summary:     "SDUI screen descriptor, filtered by RBAC for the authenticated user",
		Tags:        []string{"mobile", "sdui"},
		Middlewares: huma.Middlewares{requireAuth, serveScreen(cfg)},
	}, screenDocHandler)
}

type screenInput struct {
	ID string `path:"id" doc:"Id of a screen registered in internal/mobilebff/sdui (e.g. scheduler.jobs)"`
}

type screenOutput struct {
	Body json.RawMessage
}

func screenDocHandler(ctx context.Context, in *screenInput) (*screenOutput, error) {
	return &screenOutput{Body: json.RawMessage("{}")}, nil
}

func serveScreen(cfg *config.Config) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		req, w := humago.Unwrap(ctx)

		id := req.PathValue("id")
		username := auth.UserFrom(req)
		viewer := sdui.ViewerFrom(cfg, username)

		env, err := sdui.Build(req.Context(), id, viewer)
		if err != nil {
			if errors.Is(err, sdui.ErrScreenNotFound) {
				httpx.WriteErr(w, http.StatusNotFound, "screen_not_found")
				return
			}
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
