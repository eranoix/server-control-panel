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

// maxActionBodyBytes caps the body of POST /actions/{action_id} — the same
// 1 MiB policy other JSON endpoints in this project use for non-upload
// bodies (see internal/api/agent_hook.go). A client that sends a larger body
// is refused with 413 by http.MaxBytesReader itself, BEFORE the whole body
// is read into memory.
const maxActionBodyBytes = 1 << 20

// registerActions registers POST /actions/{action_id} — the single mutation
// point of every SDUI screen. The app never calls another endpoint to run a
// screen action: the ActionDescriptor the screen emitted carries the
// action_id, and this is the only path that knows how to resolve it into a
// real domain operation.
func init() { Register("actions", registerActions) }

func registerActions(api huma.API, deps Deps) {
	cfg := deps.Cfg
	audit := deps.Audit
	huma.Register(api, huma.Operation{
		OperationID: "runAction",
		Method:      http.MethodPost,
		Path:        "/actions/{action_id}",
		Summary:     "Executa uma ação SDUI registrada, com confirmação destrutiva reforçada pelo servidor",
		Tags:        []string{"mobile", "sdui"},
		// requireAuth (handlers_session.go) replies 401 without calling next()
		// for an unauthenticated request. handleAction does the rest —
		// decoding the body, resolving the Viewer, dispatching via
		// sdui.RunAction and writing the response — and it never calls
		// next() either: the typed handler below exists only so huma can
		// generate the OpenAPI documentation (path parameter and input/output
		// shape), never to actually run.
		Middlewares: huma.Middlewares{requireAuth, handleAction(cfg, audit)},
	}, actionDocHandler)
}

// actionInput exists only so huma documents the path parameter
// {action_id} in the generated OpenAPI — handleAction reads
// req.PathValue("action_id") straight off the raw request, never through
// this struct.
type actionInput struct {
	ActionID string `path:"action_id" doc:"Id da ação registrada em internal/mobilebff/sdui (ex.: scheduler.jobs.run_now)"`
}

// actionOutput declares the request/response body as free-form JSON — for
// the same reason screenOutput (handlers_screens.go) does: the request body
// carries an "input" whose shape is specific to each action, and the
// response body is either {"patch": ...} or {"invalidate": [...]} or the
// 422 error body of sdui.FieldErrors. None of those three shapes is a fixed
// DTO worth generating as a strong Kotlin schema — the same reasoning as the
// SDUI vocabulary itself.
type actionOutput struct {
	Body json.RawMessage
}

// actionDocHandler never runs in production — handleAction intercepts and
// writes the response on its own, without calling next(). It exists only to
// give huma.Register a concrete pair of types to infer the Operation above.
func actionDocHandler(ctx context.Context, in *actionInput) (*actionOutput, error) {
	return &actionOutput{Body: json.RawMessage("{}")}, nil
}

// actionRequestBody is the exact shape of the POST /actions/{action_id} body:
//
//	{"params": {"id": "..."}, "input": {...}, "confirmation": {"confirmed": bool, "typed": "..."}}
//
// Input stays a json.RawMessage because the BFF does not interpret domain
// payloads — each ActionHandler decodes the shape it expects itself.
type actionRequestBody struct {
	Params       map[string]string `json:"params,omitempty"`
	Input        json.RawMessage   `json:"input,omitempty"`
	Confirmation sdui.Confirmation `json:"confirmation,omitempty"`
}

// handleAction is the middleware that does all the real work of POST
// /actions/{action_id}. It re-resolves the caller's Viewer from the
// authenticated request on EVERY request — it never trusts any identity,
// role or "permission_hint" field the body might echo back, because that
// shortcut (the client echoes the hint the server itself sent, and the
// server trusts it) is exactly what would reintroduce authorization as a
// client-side decision.
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
				// An unknown id and an id not permitted for this Viewer fall into
				// the SAME branch — 404 never 403, the same stance as
				// handlers_screens.go (see the comment on
				// sdui.ErrActionNotFound).
				httpx.WriteErr(w, http.StatusNotFound, "unknown_action")
				return
			}
			// A domain that returned a real "not permitted" here would be
			// the descriptor filtered by ActionsFor and RunAction's executor
			// disagreeing — a server bug, not a normal answer to a
			// client. The client's documented path for a 403 is the same as
			// a screen's: refetch. Never echo err.Error() to the client — it
			// may carry a file path, config or an internal detail of the
			// domain handler.
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
