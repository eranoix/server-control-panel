package jira

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

func strptr(s string) *string { return &s }

// Editing the issue type from the detail view → UpdateIssue must emit
// fields["issuetype"]={"name":...}. The type never "clears" (nil/"" = no-op).
func TestUpdateIssueSetsIssueType(t *testing.T) {
	var body map[string]any
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &body)
		w.WriteHeader(http.StatusNoContent)
	})
	if err := c.UpdateIssue(context.Background(), "X-1", UpdateIssueRequest{
		IssueType: strptr("Bug"),
	}); err != nil {
		t.Fatal(err)
	}
	fields, _ := body["fields"].(map[string]any)
	it, _ := fields["issuetype"].(map[string]any)
	if it == nil || it["name"] != "Bug" {
		t.Errorf("issuetype not set correctly: %+v", fields["issuetype"])
	}
}

func TestUpdateIssueOmitsIssueTypeWhenNil(t *testing.T) {
	var body map[string]any
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &body)
		w.WriteHeader(http.StatusNoContent)
	})
	// IssueType nil but some other field present → PUT without the issuetype key.
	if err := c.UpdateIssue(context.Background(), "X-1", UpdateIssueRequest{
		Summary: strptr("só o summary"),
	}); err != nil {
		t.Fatal(err)
	}
	fields, _ := body["fields"].(map[string]any)
	if _, ok := fields["issuetype"]; ok {
		t.Errorf("issuetype should not appear when nil: %+v", fields)
	}
}

func TestUpdateIssueEmptyNoPut(t *testing.T) {
	called := false
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	})
	// A wholly empty request (and IssueType:"" is a no-op too) → no PUT at all.
	if err := c.UpdateIssue(context.Background(), "X-1", UpdateIssueRequest{
		IssueType: strptr(""),
	}); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Errorf("UpdateIssue fired a PUT for an empty request")
	}
}
