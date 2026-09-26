package sdui

import "encoding/json"

// ActionDescriptor is the server-side resolution of an action: method,
// endpoint and body already concrete. The client never assembles a URL out of
// data — it only fills in "{id}" from the row/resource the action was bound
// to and calls exactly what the descriptor dictates.
//
// Destructive is what the server-side executor actually enforces: the
// ConfirmDestructive component is only the UI half of the same rule, not the
// rule itself — the UI can be bypassed (an outdated client, a direct call),
// the server-side check cannot.
type ActionDescriptor struct {
	ActionID                 string          `json:"action_id"`
	Method                   string          `json:"method"`
	Endpoint                 string          `json:"endpoint"`
	BodyTemplate             json.RawMessage `json:"body_template,omitempty"`
	Permission               string          `json:"permission"`
	Destructive              bool            `json:"destructive,omitempty"`
	RequireTypedConfirmation string          `json:"require_typed_confirmation,omitempty"`
}
