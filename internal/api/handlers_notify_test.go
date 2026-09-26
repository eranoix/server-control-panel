package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/config"
	"server-control-panel/internal/notify"
)

func newNotifyTestRouter(t *testing.T) *Router {
	t.Helper()
	dir := t.TempDir()
	rt, err := notify.New(notify.Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(rt.Close)
	return &Router{cfg: &config.Config{DataDir: dir, Primary: "sam"}, notify: rt}
}

func notifyReq(method, target, user, body string) *http.Request {
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, target, strings.NewReader(body))
	} else {
		req = httptest.NewRequest(method, target, nil)
	}
	return req.WithContext(auth.WithUser(context.Background(), user))
}

// Non-primary users are forbidden from every notify endpoint.
func TestNotifyRulesForbiddenForNonPrimary(t *testing.T) {
	r := newNotifyTestRouter(t)
	rec := httptest.NewRecorder()
	r.handleNotifyRules(rec, notifyReq(http.MethodGet, "/api/notify/rules", "jordan", ""))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-primary GET rules: status=%d want 403 (body=%q)", rec.Code, rec.Body.String())
	}
}

// Full rule lifecycle through the handlers: create → list → delete → list.
func TestNotifyRulesCRUD(t *testing.T) {
	r := newNotifyTestRouter(t)

	// Create.
	rec := httptest.NewRecorder()
	r.handleNotifyRules(rec, notifyReq(http.MethodPost, "/api/notify/rules", "sam",
		`{"name":"jobs","enabled":true,"type_prefix":"job.","channels":[]}`))
	if rec.Code != 200 {
		t.Fatalf("create: status=%d body=%q", rec.Code, rec.Body.String())
	}
	var created notify.Rule
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil || created.ID == "" {
		t.Fatalf("create response: %v %q", err, rec.Body.String())
	}

	// List → 1.
	rec = httptest.NewRecorder()
	r.handleNotifyRules(rec, notifyReq(http.MethodGet, "/api/notify/rules", "sam", ""))
	var listed struct{ Rules []notify.Rule }
	_ = json.Unmarshal(rec.Body.Bytes(), &listed)
	if len(listed.Rules) != 1 {
		t.Fatalf("after create want 1 rule, got %d", len(listed.Rules))
	}

	// Delete.
	rec = httptest.NewRecorder()
	r.handleNotifyRuleDelete(rec, notifyReq(http.MethodPost, "/api/notify/rules/delete", "sam",
		`{"id":"`+created.ID+`"}`))
	if rec.Code != 200 {
		t.Fatalf("delete: status=%d body=%q", rec.Code, rec.Body.String())
	}

	// List → 0.
	rec = httptest.NewRecorder()
	r.handleNotifyRules(rec, notifyReq(http.MethodGet, "/api/notify/rules", "sam", ""))
	_ = json.Unmarshal(rec.Body.Bytes(), &listed)
	if len(listed.Rules) != 0 {
		t.Fatalf("after delete want 0 rules, got %d", len(listed.Rules))
	}
}

// Events endpoint returns the history+dropped envelope.
func TestNotifyEventsEnvelope(t *testing.T) {
	r := newNotifyTestRouter(t)
	rec := httptest.NewRecorder()
	r.handleNotifyEvents(rec, notifyReq(http.MethodGet, "/api/notify/events", "sam", ""))
	if rec.Code != 200 {
		t.Fatalf("events: status=%d", rec.Code)
	}
	var env struct {
		Events  []notify.Event `json:"events"`
		Dropped int64          `json:"dropped"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("events envelope: %v (%q)", err, rec.Body.String())
	}
}

// Testing an unknown channel id surfaces a delivery error (not a 200).
func TestNotifyChannelTestUnknownID(t *testing.T) {
	r := newNotifyTestRouter(t)
	rec := httptest.NewRecorder()
	r.handleNotifyChannelTest(rec, notifyReq(http.MethodPost, "/api/notify/channels/test", "sam",
		`{"id":"does-not-exist"}`))
	if rec.Code == 200 {
		t.Fatalf("test of unknown channel should not 200, got %d (%q)", rec.Code, rec.Body.String())
	}
}

// 503 when the spine is unavailable (boot failure).
func TestNotifyUnavailable503(t *testing.T) {
	r := &Router{cfg: &config.Config{Primary: "sam"}} // notify nil
	rec := httptest.NewRecorder()
	r.handleNotifyRules(rec, notifyReq(http.MethodGet, "/api/notify/rules", "sam", ""))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil notify: status=%d want 503", rec.Code)
	}
}
