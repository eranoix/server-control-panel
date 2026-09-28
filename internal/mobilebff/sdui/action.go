package sdui

import "encoding/json"

type ActionDescriptor struct {
	ActionID                 string          `json:"action_id"`
	Method                   string          `json:"method"`
	Endpoint                 string          `json:"endpoint"`
	BodyTemplate             json.RawMessage `json:"body_template,omitempty"`
	Permission               string          `json:"permission"`
	Destructive              bool            `json:"destructive,omitempty"`
	RequireTypedConfirmation string          `json:"require_typed_confirmation,omitempty"`
}
