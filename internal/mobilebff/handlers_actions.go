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

const maxActionBodyBytes = 1 << 20

func init() { Register("actions", registerActions) }

func registerActions(api huma.API, deps Deps) {
	cfg := deps.Cfg
	audit := deps.Audit
	huma.Register(api, huma.Operation{
		OperationID: "runAction",
		Method:      http.MethodPost,
		Path:        "/actions/{action_id}",
		Summary:     "Runs a registered SDUI action, with destructive confirmation enforced by the server",
		Tags:        []string{"mobile", "sdui"},
		Middlewares: huma.Middlewares{requireAuth, handleAction(cfg, audit)},
	}, actionDocHandler)
}

type actionInput struct {
	ActionID string `path:"action_id" doc:"Id of an action registered in internal/mobilebff/sdui (e.g. scheduler.jobs.run_now)"`
}

type actionOutput struct {
	Body json.RawMessage
}

func actionDocHandler(ctx context.Context, in *actionInput) (*actionOutput, error) {
	return &actionOutput{Body: json.RawMessage("{}")}, nil
}

type actionRequestBody struct {
	Params       map[string]string `json:"params,omitempty"`
	Input        json.RawMessage   `json:"input,omitempty"`
	Confirmation sdui.Confirmation `json:"confirmation,omitempty"`
}

func handleAction(cfg *config.Config, audit *auth.AuditLog) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		req, w := humago.Unwrap(ctx)

		actionID := req.PathValue("action_id")
		username := auth.UserFrom(req)
		viewer := sdui.ViewerFrom(cfg, username)

		req.Body = http.MaxBytesReader(w, req.Body, maxActionBodyBytes)

		var body actionRequestBody
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				httpx.WriteErr(w, http.StatusRequestEntityTooLarge, "request_too_large")
				return
			}
			httpx.WriteErr(w, http.StatusBadRequest, "bad_json")
			return
		}

		result, err := sdui.RunAction(req.Context(), actionID, viewer, body.Params, body.Input, body.Confirmation)
		if err != nil {
			var fieldErrs sdui.FieldErrors
			if errors.As(err, &fieldErrs) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnprocessableEntity)
				if encErr := json.NewEncoder(w).Encode(fieldErrs); encErr != nil {
					log.Printf("mobilebff: error serializing the FieldErrors of action %q: %v", actionID, encErr)
				}
				return
			}
			if errors.Is(err, sdui.ErrActionNotFound) {
				httpx.WriteErr(w, http.StatusNotFound, "unknown_action")
				return
			}
			log.Printf("mobilebff: error running action %q for %q: %v", actionID, username, err)
			httpx.WriteErr(w, http.StatusInternalServerError, "internal_error")
			return
		}

		if verr := result.Validate(); verr != nil {
			log.Printf("mobilebff: malformed ActionResult from action %q for %q: %v", actionID, username, verr)
			httpx.WriteErr(w, http.StatusInternalServerError, "internal_error")
			return
		}

		httpx.AuditEvent(audit, req, username, "sdui.action", actionID)

		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if encErr := json.NewEncoder(w).Encode(result); encErr != nil {
			log.Printf("mobilebff: error serializing the result of action %q: %v", actionID, encErr)
		}
	}
}
