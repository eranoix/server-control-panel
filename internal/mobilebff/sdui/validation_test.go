package sdui

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"
)

// validationErrorFixturePath is the golden fixture shared with the Kotlin
// consumer — see contracts/sdui/fixtures/README.md.
const validationErrorFixturePath = "../../../contracts/sdui/fixtures/validation-error.json"

// Test 1: FieldErrors marshals into exactly the committed fixture's shape —
// a structural comparison (parsing both sides), not raw bytes, so as not to
// depend on key order.
func TestFieldErrors_MarshalsToCommittedFixture(t *testing.T) {
	fe := FieldErrors{
		"name":     {"required"},
		"schedule": {"cron inválido: campo de minuto fora do intervalo 0-59"},
	}

	got, err := json.Marshal(fe)
	if err != nil {
		t.Fatalf("json.Marshal(FieldErrors): %v", err)
	}

	want, err := os.ReadFile(validationErrorFixturePath)
	if err != nil {
		t.Fatalf("lendo fixture %s: %v", validationErrorFixturePath, err)
	}

	var gotVal, wantVal any
	if err := json.Unmarshal(got, &gotVal); err != nil {
		t.Fatalf("decoding FieldErrors output: %v (%s)", err, got)
	}
	if err := json.Unmarshal(want, &wantVal); err != nil {
		t.Fatalf("decodificando fixture: %v (%s)", err, want)
	}
	if !reflect.DeepEqual(gotVal, wantVal) {
		t.Fatalf("produced FieldErrors diverges from the fixture:\nproduced: %s\nfixture:   %s", got, want)
	}
}

// Test 2: errors.Is(err, ErrValidation) is true for a FieldErrors value,
// including one returned directly as a function's error.
func TestFieldErrors_ErrorsIsErrValidation(t *testing.T) {
	var err error = FieldErrors{"name": {"required"}}
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("errors.Is(FieldErrors{...}, ErrValidation) = false, want true")
	}
}

// Test 3: Validate() refuses an empty key, an empty value and an empty message —
// a 422 with no message gives the renderer nothing to attach to the input.
func TestFieldErrors_Validate(t *testing.T) {
	cases := []struct {
		name    string
		fe      FieldErrors
		wantErr bool
	}{
		{"válido", FieldErrors{"name": {"required"}}, false},
		{"mapa vazio", FieldErrors{}, true},
		{"chave vazia", FieldErrors{"": {"x"}}, true},
		{"slice de mensagens vazio", FieldErrors{"name": {}}, true},
		{"mensagem vazia", FieldErrors{"name": {""}}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.fe.Validate()
			if c.wantErr && err == nil {
				t.Fatalf("Validate() = nil, wanted an error for %#v", c.fe)
			}
			if !c.wantErr && err != nil {
				t.Fatalf("Validate() = %v, wanted nil for %#v", err, c.fe)
			}
		})
	}
}

// Test 4: MatchesForm reports a FieldErrors key the form does not
// declare — the check that keeps inline field errors honest.
func TestFieldErrors_MatchesForm(t *testing.T) {
	form := &FormComponent{
		ComponentBase: ComponentBase{Type: ComponentTypeForm, ID: "f1"},
		Fields: []FormField{
			{Key: "name"},
			{Key: "schedule"},
		},
	}

	if err := (FieldErrors{"name": {"required"}, "schedule": {"bad cron"}}).MatchesForm(form); err != nil {
		t.Fatalf("MatchesForm with known keys: %v", err)
	}

	if err := (FieldErrors{"foo": {"required"}}).MatchesForm(form); err == nil {
		t.Fatal("MatchesForm with unknown key \"foo\" did not return an error")
	}

	// ConfirmationFieldKey is reserved and always passes, even when it is not
	// among the form's fields.
	if err := (FieldErrors{ConfirmationFieldKey: {"confirmação obrigatória"}}).MatchesForm(form); err != nil {
		t.Fatalf("MatchesForm with ConfirmationFieldKey: %v", err)
	}
}
