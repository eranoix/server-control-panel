package sdui

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

const ConfirmationFieldKey = "_confirmation"

var ErrValidation = errors.New("sdui: validation failed")

type FieldErrors map[string][]string

func (fe FieldErrors) Error() string {
	keys := make([]string, 0, len(fe))
	for k := range fe {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return "validation failed: " + strings.Join(keys, ", ")
}

func (fe FieldErrors) Is(target error) bool {
	return target == ErrValidation
}

func (fe FieldErrors) Add(key, msg string) FieldErrors {
	if fe == nil {
		fe = FieldErrors{}
	}
	fe[key] = append(fe[key], msg)
	return fe
}

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

type fieldErrorsWire struct {
	Error  string              `json:"error"`
	Fields map[string][]string `json:"fields"`
}

func (fe FieldErrors) MarshalJSON() ([]byte, error) {
	return json.Marshal(fieldErrorsWire{
		Error:  "validation_failed",
		Fields: map[string][]string(fe),
	})
}
