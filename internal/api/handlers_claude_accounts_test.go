package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// claudeAcctReq fires an authenticated request at the full router (through
// auth.Middleware + mustPrimary). user="" sends no token.
func claudeAcctReq(t *testing.T, r *Router, method, path, body, user string) *httptest.ResponseRecorder {
	t.Helper()
	var br *strings.Reader
	if body != "" {
		br = strings.NewReader(body)
	} else {
		br = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, br)
	if user != "" {
		tok, _, err := r.auth.Issue(user, nil)
		if err != nil {
			t.Fatalf("auth.Issue(%q): %v", user, err)
		}
		req.AddCookie(&http.Cookie{Name: "vpsm_token", Value: tok})
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestClaudeAccountsGated(t *testing.T) {
	r := newSmokeRouter(t) // Primary="sam"

	// 1. No token → 401 (auth.Middleware). Never 404 (route must exist —
	//    guards against a heal silent-delete; see feedback_heal_deletion).
	if w := claudeAcctReq(t, r, "GET", "/api/claude/accounts", "", ""); w.Code != 401 {
		t.Fatalf("no-token GET: got %d, want 401; body=%s", w.Code, w.Body.String())
	}

	// /ratelimits is gated identically (guard the route; don't hit the live
	// network 200 path here — its data is validated by a throwaway smoke).
	if w := claudeAcctReq(t, r, "GET", "/api/claude/accounts/ratelimits", "", ""); w.Code != 401 {
		t.Fatalf("ratelimits no-token: got %d, want 401", w.Code)
	}
	if w := claudeAcctReq(t, r, "GET", "/api/claude/accounts/ratelimits", "", "bob"); w.Code != 403 {
		t.Fatalf("ratelimits non-admin: got %d, want 403", w.Code)
	}

	// 2. Non-admin token → 403 (mustPrimary). "bob" is not config.Primary.
	if w := claudeAcctReq(t, r, "GET", "/api/claude/accounts", "", "bob"); w.Code != 403 {
		t.Fatalf("non-admin GET: got %d, want 403; body=%s", w.Code, w.Body.String())
	}

	// 3. Admin (sam) → 200 + shape.
	w := claudeAcctReq(t, r, "GET", "/api/claude/accounts", "", "sam")
	if w.Code != 200 {
		t.Fatalf("admin GET: got %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var got struct {
		Accounts []struct {
			ID    string `json:"id"`
			Login struct {
				AccountID string `json:"account_id"`
			} `json:"login"`
		} `json:"accounts"`
		Consumers []struct {
			ID        string `json:"id"`
			AccountID string `json:"account_id"`
		} `json:"consumers"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v; body=%s", err, w.Body.String())
	}
	if len(got.Accounts) < 2 {
		t.Errorf("want >=2 accounts (jordan+sam), got %d", len(got.Accounts))
	}
	if len(got.Consumers) != 3 {
		t.Errorf("want 3 consumers, got %d", len(got.Consumers))
	}
	for _, c := range got.Consumers {
		if c.AccountID != "sam" {
			t.Errorf("consumer %q default = %q, want sam", c.ID, c.AccountID)
		}
	}
}

func TestClaudeAccountAssignRoundTrip(t *testing.T) {
	r := newSmokeRouter(t)

	// Assign jobs → jordan (explicitly non-default; the default is now sam).
	w := claudeAcctReq(t, r, "POST", "/api/claude/accounts/assign",
		`{"consumer":"jobs","account_id":"jordan"}`, "sam")
	if w.Code != 200 {
		t.Fatalf("assign: got %d, want 200; body=%s", w.Code, w.Body.String())
	}

	// GET reflects the new assignment; terminal stays no default sam.
	w = claudeAcctReq(t, r, "GET", "/api/claude/accounts", "", "sam")
	var got struct {
		Consumers []struct {
			ID        string `json:"id"`
			AccountID string `json:"account_id"`
		} `json:"consumers"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	seen := map[string]string{}
	for _, c := range got.Consumers {
		seen[c.ID] = c.AccountID
	}
	if seen["jobs"] != "jordan" {
		t.Errorf("jobs assignment not persisted: %q", seen["jobs"])
	}
	if seen["terminal"] != "sam" {
		t.Errorf("terminal should stay default sam, got %q", seen["terminal"])
	}

	// Invalid account → 400, no mutation.
	if w := claudeAcctReq(t, r, "POST", "/api/claude/accounts/assign",
		`{"consumer":"terminal","account_id":"ghost"}`, "sam"); w.Code != 400 {
		t.Errorf("assign bogus account: got %d, want 400", w.Code)
	}
}
