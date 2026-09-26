package sdui

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"sync"
)

// ActionResult is what an ActionHandler returns on success: EITHER a Patch
// (the updated resource, for the client to apply by id in its local cache) OR
// an Invalidate list (the component ids the client must refetch) — never
// both, never neither. Patch is preferable for single-resource actions;
// Invalidate for wide blast-radius actions (e.g. one that changes several
// rows of a table). internal/mobilebff/handlers_actions.go calls Validate
// before serializing the response: returning both fields filled in is a
// programming error in the domain handler, not a valid variation of the
// contract.
type ActionResult struct {
	Patch      any      `json:"patch,omitempty"`
	Invalidate []string `json:"invalidate,omitempty"`
}

// ErrActionResultShape is returned by Validate when an ActionResult has both
// Patch and Invalidate filled in at once, or neither — the two invalid shapes
// of the success contract.
var ErrActionResultShape = errors.New("sdui: ActionResult must have exactly one of Patch and Invalidate")

// Validate reports ErrActionResultShape if r does not have exactly one of
// Patch and Invalidate filled in.
func (r ActionResult) Validate() error {
	hasPatch := r.Patch != nil
	hasInvalidate := len(r.Invalidate) > 0
	if hasPatch == hasInvalidate {
		return ErrActionResultShape
	}
	return nil
}

// ActionHandler runs the real domain operation behind an SDUI action. params
// comes from the path of the action already resolved in the ActionDescriptor
// (e.g. "{id}" filled in by the client from the row/resource the action was
// bound to) — the handler MUST look the resource up through the domain
// package's own accessor (which already does its ownership check), never
// interpolate the received value into a query, file path or shell command.
// input is the raw body the BFF does not interpret: each action decodes the
// format it expects.
type ActionHandler func(ctx context.Context, v Viewer, params map[string]string, input json.RawMessage) (ActionResult, error)

// ErrActionNotFound is returned by RunAction both for an action_id that was
// never registered and for an action_id that exists but whose Authorize
// refuses the Viewer — the two cases are DELIBERATELY indistinguishable, for
// the same reason ErrScreenNotFound (registry.go) unifies "unknown screen"
// and "screen this Viewer cannot see": a different error per case would let a
// client enumerate which administrative actions exist just by watching the
// response, even without being able to run them. The posture of this whole
// package is 404-never-403 (see handlers_screens.go); this is the same
// principle applied to actions instead of screens.
var ErrActionNotFound = errors.New("sdui: action not found")

// Confirmation is the confirmation envelope that accompanies every RunAction
// call. Confirmed alone is enough for an ordinary Destructive action; Typed
// is only compared when the ActionDescriptor demands
// RequireTypedConfirmation, and the comparison is always exact (==), never
// case-folded nor trimmed — confirming by accident because of whitespace or
// capitalization would be the very bug typed confirmation exists to prevent.
type Confirmation struct {
	Confirmed bool   `json:"confirmed"`
	Typed     string `json:"typed"`
}

// registeredAction is the registry's internal entry: the public descriptor
// (metadata a screen Builder also consults via ActionsFor), the authorization
// function — MANDATORY, see RegisterAction — and the handler that actually
// runs the domain operation.
type registeredAction struct {
	Desc      ActionDescriptor
	Authorize func(Viewer) bool
	Handle    ActionHandler
}

var (
	actionRegistryMu sync.Mutex
	actionRegistry   = map[string]registeredAction{}
)

// RegisterAction enrolls an action under desc.ActionID. Call it from an
// init() in the file that defines the action, next to sdui.Register for the
// screen that references it — two registrations for the same id are a
// programming error and blow up at boot, never silently on the first request
// (the same policy as registry.go).
//
// authorize is MANDATORY: a nil here makes RegisterAction panic at
// registration. That is deliberate and structural, not a code-review
// convention — "I forgot to guard this action" cannot be indistinguishable
// from "this action is deliberately public" just because nobody noticed a
// missing check during a review. Every genuinely public action still needs an
// explicit authorize that always returns true, documenting the decision in
// the code itself instead of by omission.
//
// h cannot be nil either: an action without a handler has nothing to run, and
// a nil here would only surface as a panic at request time instead of at
// boot.
func RegisterAction(desc ActionDescriptor, authorize func(Viewer) bool, h ActionHandler) {
	if desc.ActionID == "" {
		panic("sdui: RegisterAction: ActionID vazio")
	}
	if authorize == nil {
		panic("sdui: RegisterAction(" + desc.ActionID + "): authorize is required — no action may be registered without an explicit authorization decision")
	}
	if h == nil {
		panic("sdui: RegisterAction(" + desc.ActionID + "): handler is required")
	}

	actionRegistryMu.Lock()
	defer actionRegistryMu.Unlock()
	if _, exists := actionRegistry[desc.ActionID]; exists {
		panic("sdui: action already registered: " + desc.ActionID)
	}
	actionRegistry[desc.ActionID] = registeredAction{Desc: desc, Authorize: authorize, Handle: h}
}

// RunAction resolves, authorizes, applies the destructive-confirmation gate
// and only then runs an action's handler. The order of the steps is itself
// the security guarantee:
//
//  1. unknown id -> ErrActionNotFound.
//  2. Authorize(v) == false -> ErrActionNotFound (the same error as step 1 —
//     see the ErrActionNotFound comment).
//  3. Desc.Destructive and !confirm.Confirmed -> FieldErrors{ConfirmationFieldKey}
//     wrapping ErrValidation, WITHOUT calling the handler.
//  4. Desc.RequireTypedConfirmation != "" and confirm.Typed does not match
//     (exact comparison) -> the same FieldErrors, WITHOUT calling the handler.
//  5. only now does the handler run.
//
// Steps 3 and 4 happen before any call to Handle: a destructive action that
// only refused AFTER running would not be a refusal.
func RunAction(ctx context.Context, id string, v Viewer, params map[string]string, input json.RawMessage, confirm Confirmation) (ActionResult, error) {
	actionRegistryMu.Lock()
	ra, ok := actionRegistry[id]
	actionRegistryMu.Unlock()
	if !ok {
		return ActionResult{}, ErrActionNotFound
	}

	if !ra.Authorize(v) {
		return ActionResult{}, ErrActionNotFound
	}

	if ra.Desc.Destructive && !confirm.Confirmed {
		return ActionResult{}, FieldErrors{
			ConfirmationFieldKey: {"confirmation is required to run this action"},
		}
	}

	if ra.Desc.RequireTypedConfirmation != "" && confirm.Typed != ra.Desc.RequireTypedConfirmation {
		return ActionResult{}, FieldErrors{
			ConfirmationFieldKey: {"type \"" + ra.Desc.RequireTypedConfirmation + "\" to confirm"},
		}
	}

	return ra.Handle(ctx, v, params, input)
}

// ActionsFor returns, in alphabetical ActionID order, the descriptors of
// every action for which v.Authorize returns true. A screen Builder uses this
// — never a list of its own — to decide which ActionRef to emit: the set a
// Builder may REFERENCE and the set RunAction will EXECUTE come from the same
// per-action Authorize function. That identity is what makes a 403
// structurally unreachable under normal conditions: if the two lists ever
// diverge, it is because something outside this package decided otherwise —
// never because this package kept two sources of truth.
func ActionsFor(v Viewer) []ActionDescriptor {
	actionRegistryMu.Lock()
	defer actionRegistryMu.Unlock()

	ids := make([]string, 0, len(actionRegistry))
	for id := range actionRegistry {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	out := make([]ActionDescriptor, 0, len(ids))
	for _, id := range ids {
		ra := actionRegistry[id]
		if ra.Authorize(v) {
			out = append(out, ra.Desc)
		}
	}
	return out
}
