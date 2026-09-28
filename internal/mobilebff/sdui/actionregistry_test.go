package sdui

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func adminViewer() Viewer              { return Viewer{Username: "admin", isAdmin: true} }
func plainViewer() Viewer              { return Viewer{Username: "plain", isAdmin: false} }
func adminOnlyAuthorize(v Viewer) bool { return v.IsAdmin() }

func TestRunAction_InvokesHandlerAndReturnsResult(t *testing.T) {
	calls := 0
	RegisterAction(
		ActionDescriptor{ActionID: "test.registry.basic"},
		adminOnlyAuthorize,
		func(ctx context.Context, v Viewer, params map[string]string, input json.RawMessage) (ActionResult, error) {
			calls++
			return ActionResult{Patch: map[string]string{"id": params["id"]}}, nil
		},
	)

	res, err := RunAction(context.Background(), "test.registry.basic", adminViewer(), map[string]string{"id": "42"}, nil, Confirmation{})
	if err != nil {
		t.Fatalf("RunAction: %v", err)
	}
	if calls != 1 {
		t.Fatalf("handler called %d times, want 1", calls)
	}
	patch, ok := res.Patch.(map[string]string)
	if !ok || patch["id"] != "42" {
		t.Fatalf("Patch = %#v, want {id: 42}", res.Patch)
	}
}

func TestRunAction_UnregisteredID_ErrActionNotFound(t *testing.T) {
	_, err := RunAction(context.Background(), "test.registry.does.not.exist", adminViewer(), nil, nil, Confirmation{})
	if !errors.Is(err, ErrActionNotFound) {
		t.Fatalf("err = %v, want ErrActionNotFound", err)
	}
}

func TestRunAction_DestructiveWithoutConfirmation_RefusesBeforeHandler(t *testing.T) {
	calls := 0
	RegisterAction(
		ActionDescriptor{ActionID: "test.registry.destructive", Destructive: true},
		adminOnlyAuthorize,
		func(ctx context.Context, v Viewer, params map[string]string, input json.RawMessage) (ActionResult, error) {
			calls++
			return ActionResult{Invalidate: []string{"x"}}, nil
		},
	)

	_, err := RunAction(context.Background(), "test.registry.destructive", adminViewer(), nil, nil, Confirmation{})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("err = %v, want errors.Is(err, ErrValidation)", err)
	}
	fe, ok := err.(FieldErrors)
	if !ok {
		t.Fatalf("err is %T, want FieldErrors", err)
	}
	if len(fe[ConfirmationFieldKey]) == 0 {
		t.Fatalf("FieldErrors does not have the key %q: %#v", ConfirmationFieldKey, fe)
	}
	if calls != 0 {
		t.Fatalf("handler was called %d times — a destructive action without confirmation must not run the handler", calls)
	}
}

func TestRunAction_DestructiveWithConfirmation_RunsHandler(t *testing.T) {
	res, err := RunAction(context.Background(), "test.registry.destructive", adminViewer(), nil, nil, Confirmation{Confirmed: true})
	if err != nil {
		t.Fatalf("RunAction: %v", err)
	}
	if len(res.Invalidate) != 1 || res.Invalidate[0] != "x" {
		t.Fatalf("Invalidate = %#v, want [x]", res.Invalidate)
	}
}

func TestRunAction_RequireTypedConfirmation_ExactMatchOnly(t *testing.T) {
	RegisterAction(
		ActionDescriptor{ActionID: "test.registry.typed", Destructive: true, RequireTypedConfirmation: "prod-db"},
		adminOnlyAuthorize,
		func(ctx context.Context, v Viewer, params map[string]string, input json.RawMessage) (ActionResult, error) {
			return ActionResult{Invalidate: []string{"ran"}}, nil
		},
	)

	_, err := RunAction(context.Background(), "test.registry.typed", adminViewer(), nil, nil, Confirmation{Confirmed: true})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("confirmed on its own: err = %v, want ErrValidation", err)
	}

	_, err = RunAction(context.Background(), "test.registry.typed", adminViewer(), nil, nil, Confirmation{Confirmed: true, Typed: "prod-DB"})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("typed=prod-DB: err = %v, want ErrValidation", err)
	}

	res, err := RunAction(context.Background(), "test.registry.typed", adminViewer(), nil, nil, Confirmation{Confirmed: true, Typed: "prod-db"})
	if err != nil {
		t.Fatalf("typed=prod-db: err = %v, want nil", err)
	}
	if len(res.Invalidate) != 1 || res.Invalidate[0] != "ran" {
		t.Fatalf("Invalidate = %#v, want [ran]", res.Invalidate)
	}
}

func TestRunAction_UnauthorizedViewer_SameErrorAsUnregistered(t *testing.T) {
	RegisterAction(
		ActionDescriptor{ActionID: "test.registry.adminonly"},
		adminOnlyAuthorize,
		func(ctx context.Context, v Viewer, params map[string]string, input json.RawMessage) (ActionResult, error) {
			return ActionResult{Invalidate: []string{"should-not-run"}}, nil
		},
	)

	_, unauthorizedErr := RunAction(context.Background(), "test.registry.adminonly", plainViewer(), nil, nil, Confirmation{})
	_, unregisteredErr := RunAction(context.Background(), "test.registry.does.not.exist.either", plainViewer(), nil, nil, Confirmation{})

	if !errors.Is(unauthorizedErr, ErrActionNotFound) {
		t.Fatalf("unauthorizedErr = %v, want ErrActionNotFound", unauthorizedErr)
	}
	if !errors.Is(unregisteredErr, ErrActionNotFound) {
		t.Fatalf("unregisteredErr = %v, want ErrActionNotFound", unregisteredErr)
	}
	if !errors.Is(unauthorizedErr, unregisteredErr) || unauthorizedErr.Error() != unregisteredErr.Error() {
		t.Fatalf("the two errors diverge: unauthorized=%v, unregistered=%v", unauthorizedErr, unregisteredErr)
	}

	res, err := RunAction(context.Background(), "test.registry.adminonly", adminViewer(), nil, nil, Confirmation{})
	if err != nil {
		t.Fatalf("admin: RunAction: %v — without this, the test above would be vacuous", err)
	}
	if len(res.Invalidate) != 1 || res.Invalidate[0] != "should-not-run" {
		t.Fatalf("admin: Invalidate = %#v", res.Invalidate)
	}
}

func TestRegisterAction_DuplicateID_Panics(t *testing.T) {
	RegisterAction(
		ActionDescriptor{ActionID: "test.registry.dup"},
		adminOnlyAuthorize,
		func(ctx context.Context, v Viewer, params map[string]string, input json.RawMessage) (ActionResult, error) {
			return ActionResult{Invalidate: []string{"x"}}, nil
		},
	)

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("second RegisterAction for the same id did not panic")
		}
	}()
	RegisterAction(
		ActionDescriptor{ActionID: "test.registry.dup"},
		adminOnlyAuthorize,
		func(ctx context.Context, v Viewer, params map[string]string, input json.RawMessage) (ActionResult, error) {
			return ActionResult{Invalidate: []string{"y"}}, nil
		},
	)
}

func TestRegisterAction_NilAuthorize_Panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("RegisterAction with a nil authorize did not panic")
		}
	}()
	RegisterAction(
		ActionDescriptor{ActionID: "test.registry.noauthorize"},
		nil,
		func(ctx context.Context, v Viewer, params map[string]string, input json.RawMessage) (ActionResult, error) {
			return ActionResult{Invalidate: []string{"x"}}, nil
		},
	)
}

func TestActionsFor_OnlyAuthorizedDescriptors(t *testing.T) {
	descs := ActionsFor(adminViewer())
	foundAdminOnly := false
	for _, d := range descs {
		if d.ActionID == "test.registry.adminonly" {
			foundAdminOnly = true
		}
	}
	if !foundAdminOnly {
		t.Fatalf("ActionsFor(admin) does not contain test.registry.adminonly: %#v", descs)
	}

	descsPlain := ActionsFor(plainViewer())
	for _, d := range descsPlain {
		if d.ActionID == "test.registry.adminonly" {
			t.Fatalf("ActionsFor(plain) contains test.registry.adminonly, which only admin can run")
		}
	}
}
