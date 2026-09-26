package sdui

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ConfirmationFieldKey is the pseudo-field key reserved for the destructive
// confirmation error (see actionregistry.go). The Kotlin renderer maps this
// key specifically to the native confirmation dialog, not to a form input —
// which is why no real FormField may declare this key (a form with a field
// named "_confirmation" would collide with the reserved error response). It
// is not enforced by a runtime test because that would belong to the
// component vocabulary validator (vocabulary_test.go), not to this error
// package.
const ConfirmationFieldKey = "_confirmation"

// ErrValidation is the sentinel every FieldErrors satisfies via Is — it lets
// a handler test `errors.Is(err, sdui.ErrValidation)` without a type switch
// in every caller (see handlers_actions.go).
var ErrValidation = errors.New("sdui: validation failed")

// FieldErrors is the ONE type that produces the body of a 422 from the mobile BFF:
// {"error": "validation_failed", "fields": {"name": ["required"], ...}}.
// The envelope's shape ({"error":..., "fields":...}) lives in MarshalJSON, not
// in each handler — no handler may invent a variant of the 422 shape by
// calling json.Marshal directly on a map or struct of its own.
//
// By design, this package never validates business rules: FormField's fields
// carry "required" purely as a UI affordance (disabling the submit button,
// underlining in red before the round trip), and every real rule is decided
// by the domain package (e.g. internal/scheduler validates cron, name, etc.)
// — the client receives the RESULT of a server decision, never the inputs to
// a decision it would take itself (see the documented pitfall). Translating
// the domain's typed error (fmt.Errorf("%w: field", ErrBadInput)) into the
// corresponding FieldErrors key is the action handler's responsibility
// (internal/mobilebff/handlers_actions.go), never the domain package's.
type FieldErrors map[string][]string

// Error satisfies the error interface with a stable summary (keys in
// alphabetical order) — useful only for logging; the real body the client
// receives is MarshalJSON's, never this text.
func (fe FieldErrors) Error() string {
	keys := make([]string, 0, len(fe))
	for k := range fe {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return "validation failed: " + strings.Join(keys, ", ")
}

// Is allows `errors.Is(err, ErrValidation)` for any FieldErrors value,
// including when wrapped by fmt.Errorf("%w", ...) somewhere along the
// way.
func (fe FieldErrors) Is(target error) bool {
	return target == ErrValidation
}

// Add accumulates a message under key and returns fe — since FieldErrors is
// a map that is nil-safe only on reads, not on writes, the calling pattern is
// always `fe = fe.Add(key, msg)`, never a bare `fe.Add(key, msg)` (which
// would lose the freshly allocated map when fe starts out nil).
func (fe FieldErrors) Add(key, msg string) FieldErrors {
	if fe == nil {
		fe = FieldErrors{}
	}
	fe[key] = append(fe[key], msg)
	return fe
}

// Validate reports an error if fe is empty, has an empty key, or has a key
// whose message slice is empty or contains an empty message — a 422 with an
// empty message list gives the renderer nothing to attach to the
// corresponding input, which is worse than not sending that field at
// all.
func (fe FieldErrors) Validate() error {
	if len(fe) == 0 {
		return fmt.Errorf("sdui: FieldErrors empty")
	}
	for key, msgs := range fe {
		if key == "" {
			return fmt.Errorf("sdui: FieldErrors has an empty key")
		}
		if len(msgs) == 0 {
			return fmt.Errorf("sdui: FieldErrors[%q] has no message", key)
		}
		for _, m := range msgs {
			if m == "" {
				return fmt.Errorf("sdui: FieldErrors[%q] has an empty message", key)
			}
		}
	}
	return nil
}

// MatchesForm returns an error naming every key of fe that matches no
// FormField.Key of f — the check that keeps inline field errors honest: an
// error keyed to a field the form does not declare cannot be attached to any
// input, so it is always a bug (in the handler that assembled the
// FieldErrors, or in the form that fell out of date). ConfirmationFieldKey is
// the only exception — a reserved key the renderer handles separately (the
// confirmation dialog), never expected among a real form's FormField.Key values.
func (fe FieldErrors) MatchesForm(f *FormComponent) error {
	known := make(map[string]bool, len(f.Fields))
	for _, field := range f.Fields {
		known[field.Key] = true
	}

	var unknown []string
	for key := range fe {
		if key == ConfirmationFieldKey {
			continue
		}
		if !known[key] {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return fmt.Errorf("sdui: FieldErrors references field(s) the form %q does not have: %s", f.ID, strings.Join(unknown, ", "))
	}
	return nil
}

// fieldErrorsWire is the exact wire shape of a 422 body — the field names and
// the order MarshalJSON writes them in.
type fieldErrorsWire struct {
	Error  string              `json:"error"`
	Fields map[string][]string `json:"fields"`
}

// MarshalJSON serializes fe as {"error": "validation_failed", "fields":
// {...}} — the same shape pinned by the committed fixture
// contracts/sdui/fixtures/validation-error.json (see validation_test.go).
func (fe FieldErrors) MarshalJSON() ([]byte, error) {
	return json.Marshal(fieldErrorsWire{
		Error:  "validation_failed",
		Fields: map[string][]string(fe),
	})
}
