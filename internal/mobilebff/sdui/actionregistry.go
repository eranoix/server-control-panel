package sdui

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"sync"
)

type ActionResult struct {
	Patch      any      `json:"patch,omitempty"`
	Invalidate []string `json:"invalidate,omitempty"`
}

var ErrActionResultShape = errors.New("sdui: ActionResult must have exactly one of Patch and Invalidate")

func (r ActionResult) Validate() error {
	hasPatch := r.Patch != nil
	hasInvalidate := len(r.Invalidate) > 0
	if hasPatch == hasInvalidate {
		return ErrActionResultShape
	}
	return nil
}

type ActionHandler func(ctx context.Context, v Viewer, params map[string]string, input json.RawMessage) (ActionResult, error)

var ErrActionNotFound = errors.New("sdui: action not found")

type Confirmation struct {
	Confirmed bool   `json:"confirmed"`
	Typed     string `json:"typed"`
}

type registeredAction struct {
	Desc      ActionDescriptor
	Authorize func(Viewer) bool
	Handle    ActionHandler
}

var (
	actionRegistryMu sync.Mutex
	actionRegistry   = map[string]registeredAction{}
)

func RegisterAction(desc ActionDescriptor, authorize func(Viewer) bool, h ActionHandler) {
	if desc.ActionID == "" {
		panic("sdui: RegisterAction: empty ActionID")
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
