package mobilebff

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/config"
	"server-control-panel/internal/mobilebff/sdui"
)

func actionsTestCfg() *config.Config {
	return &config.Config{
		SchemaVersion: config.CurrentSchemaVersion,
		Primary:       "actionadmin",
		Users: []config.User{
			{Username: "actionadmin", PasswordHash: "h"},
			{Username: "actionviewer", PasswordHash: "h"},
		},
	}
}

func anyoneMayRun(sdui.Viewer) bool  { return true }
func adminMayRun(v sdui.Viewer) bool { return v.IsAdmin() }

func init() {
	sdui.RegisterAction(
		sdui.ActionDescriptor{ActionID: "test.actions.patch"},
		anyoneMayRun,
		func(ctx context.Context, v sdui.Viewer, params map[string]string, input json.RawMessage) (sdui.ActionResult, error) {
			return sdui.ActionResult{Patch: map[string]string{"id": params["id"], "status": "done"}}, nil
		},
	)

	sdui.RegisterAction(
		sdui.ActionDescriptor{ActionID: "test.actions.invalid"},
		anyoneMayRun,
		func(ctx context.Context, v sdui.Viewer, params map[string]string, input json.RawMessage) (sdui.ActionResult, error) {
			return sdui.ActionResult{}, sdui.FieldErrors{"name": {"required"}}
		},
	)

	sdui.RegisterAction(
		sdui.ActionDescriptor{ActionID: "test.actions.destructive", Destructive: true},
		anyoneMayRun,
		func(ctx context.Context, v sdui.Viewer, params map[string]string, input json.RawMessage) (sdui.ActionResult, error) {
			return sdui.ActionResult{Invalidate: []string{"containers-table"}}, nil
		},
	)

	sdui.RegisterAction(
		sdui.ActionDescriptor{ActionID: "test.actions.adminonly"},
		adminMayRun,
		func(ctx context.Context, v sdui.Viewer, params map[string]string, input json.RawMessage) (sdui.ActionResult, error) {
			return sdui.ActionResult{Invalidate: []string{"should-not-run"}}, nil
		},
	)
}

func postAction(mux *http.ServeMux, actionID, username, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/mobile/v1/actions/"+actionID, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if username != "" {
		req = req.WithContext(auth.WithUser(req.Context(), username))
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// Test 1: an unauthenticated POST returns 401 — inherited from the protected
// mux (requireAuth), not reimplemented here.
func TestHandleAction_Unauthenticated_401(t *testing.T) {
	mux := http.NewServeMux()
	Mount(mux, Deps{Cfg: actionsTestCfg()})

	req := httptest.NewRequest(http.MethodPost, "/api/mobile/v1/actions/test.actions.patch", strings.NewReader("{}"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (body=%s)", rec.Code, rec.Body.String())
	}
}

// Test 2: a successful action returns 200 with a body containing "patch" or
// "invalidate" — never both, never neither.
func TestHandleAction_Success_ReturnsPatchXorInvalidate(t *testing.T) {
	mux := http.NewServeMux()
	Mount(mux, Deps{Cfg: actionsTestCfg()})

	rec := postAction(mux, "test.actions.patch", "actionviewer", `{"params":{"id":"7"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, rec.Body.String())
	}
	_, hasPatch := raw["patch"]
	_, hasInvalidate := raw["invalidate"]
	if hasPatch == hasInvalidate {
		t.Fatalf("body has patch=%v invalidate=%v — want exactly one of the two: %s", hasPatch, hasInvalidate, rec.Body.String())
	}
	if !hasPatch {
		t.Fatalf("expected \"patch\" in the body: %s", rec.Body.String())
	}
}

// Test 3: an action that returns FieldErrors produces HTTP 422 with the exact
// body {"error":"validation_failed","fields":{...}}, Content-Type: application/json.
func TestHandleAction_ValidationFailure_422FieldKeyed(t *testing.T) {
	mux := http.NewServeMux()
	Mount(mux, Deps{Cfg: actionsTestCfg()})

	rec := postAction(mux, "test.actions.invalid", "actionviewer", `{}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (body=%s)", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, rec.Body.String())
	}
	if got["error"] != "validation_failed" {
		t.Errorf("error = %v, want validation_failed", got["error"])
	}
	fields, ok := got["fields"].(map[string]any)
	if !ok {
		t.Fatalf("fields missing or not an object: %s", rec.Body.String())
	}
	nameErrs, ok := fields["name"].([]any)
	if !ok || len(nameErrs) != 1 || nameErrs[0] != "required" {
		t.Errorf("fields.name = %v, want [\"required\"]", fields["name"])
	}
}

// Test 4: an unknown action_id and a known but not-permitted action_id return
// the SAME 404 {"error":"unknown_action"} for the Viewer — byte for byte,
// for the same reason GET /screens/{id} unifies both cases (see
// sdui.ErrActionNotFound).
func TestHandleAction_UnknownAndUnauthorized_Are404Identical(t *testing.T) {
	mux := http.NewServeMux()
	Mount(mux, Deps{Cfg: actionsTestCfg()})

	unknownRec := postAction(mux, "test.actions.does.not.exist", "actionviewer", `{}`)
	unauthorizedRec := postAction(mux, "test.actions.adminonly", "actionviewer", `{}`)

	if unknownRec.Code != http.StatusNotFound || unauthorizedRec.Code != http.StatusNotFound {
		t.Fatalf("status = %d (unknown) / %d (unauthorized), want both 404", unknownRec.Code, unauthorizedRec.Code)
	}
	if unknownRec.Body.String() != unauthorizedRec.Body.String() {
		t.Errorf("bodies differ — unknown: %q, unauthorized: %q", unknownRec.Body.String(), unauthorizedRec.Body.String())
	}
	wantBody := `{"error":"unknown_action"}` + "\n"
	if unknownRec.Body.String() != wantBody {
		t.Errorf("body = %q, want %q", unknownRec.Body.String(), wantBody)
	}

	// Non-vacuity proof: an admin DOES run the same action.
	adminRec := postAction(mux, "test.actions.adminonly", "actionadmin", `{}`)
	if adminRec.Code != http.StatusOK {
		t.Fatalf("admin: status = %d, want 200 (body=%s) — without this the test above would be vacuous", adminRec.Code, adminRec.Body.String())
	}
}

// Test 5 (destructive-action confirmation end to end over HTTP): POST to a
// destructive action with {} returns 422 with _confirmation; the same POST
// with {"confirmation":{"confirmed":true}} returns 200.
func TestHandleAction_DestructiveOverHTTP_RequiresConfirmation(t *testing.T) {
	mux := http.NewServeMux()
	Mount(mux, Deps{Cfg: actionsTestCfg()})

	noConfirmRec := postAction(mux, "test.actions.destructive", "actionviewer", `{}`)
	if noConfirmRec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("without confirmation: status = %d, want 422 (body=%s)", noConfirmRec.Code, noConfirmRec.Body.String())
	}
	if !strings.Contains(noConfirmRec.Body.String(), sdui.ConfirmationFieldKey) {
		t.Errorf("body without confirmation does not contain %q: %s", sdui.ConfirmationFieldKey, noConfirmRec.Body.String())
	}

	confirmedRec := postAction(mux, "test.actions.destructive", "actionviewer", `{"confirmation":{"confirmed":true}}`)
	if confirmedRec.Code != http.StatusOK {
		t.Fatalf("confirmed: status = %d, want 200 (body=%s)", confirmedRec.Code, confirmedRec.Body.String())
	}
}

// Test 6: a body larger than the limit (1 MiB) returns 413 instead of being
// read into memory in full — MaxBytesReader stops the read before that.
func TestHandleAction_BodyOverLimit_413(t *testing.T) {
	mux := http.NewServeMux()
	Mount(mux, Deps{Cfg: actionsTestCfg()})

	huge := bytes.Repeat([]byte("a"), maxActionBodyBytes+1024)
	body := `{"input":"` + string(huge) + `"}`

	req := httptest.NewRequest(http.MethodPost, "/api/mobile/v1/actions/test.actions.patch", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.ContentLength = int64(len(body))
	req = req.WithContext(auth.WithUser(req.Context(), "actionviewer"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413 (body len=%s)", rec.Code, strconv.Itoa(rec.Body.Len()))
	}
}

// Test 7: GET/PUT/DELETE on the same route return 405 — behaviour inherited
// from http.ServeMux's method-based routing (Go 1.22+), not reimplemented
// in the handler.
func TestHandleAction_WrongMethod_405(t *testing.T) {
	mux := http.NewServeMux()
	Mount(mux, Deps{Cfg: actionsTestCfg()})

	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		req := httptest.NewRequest(method, "/api/mobile/v1/actions/test.actions.patch", nil)
		req = req.WithContext(auth.WithUser(req.Context(), "actionviewer"))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: status = %d, want 405 (body=%s)", method, rec.Code, rec.Body.String())
		}
	}
}
