package jira

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &Client{
		site:  srv.URL,
		email: "u@e.com",
		token: "tok",
		http:  srv.Client(),
	}
}

func TestNewNormalisesSite(t *testing.T) {
	cases := []struct{ in, want string }{
		{"jordan", "https://jordan.atlassian.net"},
		{"https://jordan.atlassian.net", "https://jordan.atlassian.net"},
		{"https://jordan.atlassian.net/", "https://jordan.atlassian.net"},
		{"acme.atlassian.net", "https://acme.atlassian.net"},
	}
	for _, c := range cases {
		got, err := New(c.in, "u@e.com", "t")
		if err != nil {
			t.Fatalf("%s: %v", c.in, err)
		}
		if got.site != c.want {
			t.Errorf("site(%q): got %q want %q", c.in, got.site, c.want)
		}
	}
}

func TestNewRequiresAll(t *testing.T) {
	if _, err := New("", "u", "t"); err != ErrNotConfigured {
		t.Errorf("missing site: %v", err)
	}
	if _, err := New("s", "", "t"); err != ErrNotConfigured {
		t.Errorf("missing email: %v", err)
	}
	if _, err := New("s", "u", ""); err != ErrNotConfigured {
		t.Errorf("missing token: %v", err)
	}
}

func TestDoSendsBasicAuth(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Basic ") {
			t.Errorf("missing basic auth: %q", auth)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	})
	var resp map[string]any
	if err := c.do(context.Background(), http.MethodGet, "/x", nil, &resp); err != nil {
		t.Fatal(err)
	}
}

func TestDoMapsErrors(t *testing.T) {
	cases := []struct {
		status int
		want   error
	}{
		{401, ErrUnauthorized},
		{403, ErrUnauthorized},
		{429, ErrRateLimited},
	}
	for _, c := range cases {
		client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(c.status)
		})
		if err := client.do(context.Background(), http.MethodGet, "/x", nil, nil); err != c.want {
			t.Errorf("status %d: got %v want %v", c.status, err, c.want)
		}
	}
}

func TestSearchBuildsRequest(t *testing.T) {
	var seen struct {
		path string
		body map[string]any
	}
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		seen.path = r.URL.Path
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &seen.body)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"issues":[{"id":"1","key":"X-1","fields":{"summary":"hi","status":{"name":"To Do","statusCategory":{"key":"new","name":"To Do"}}}}],"total":1}`))
	})
	out, total, err := c.Search(context.Background(), "project = X", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if seen.path != "/rest/api/3/search/jql" {
		t.Errorf("path: %s", seen.path)
	}
	if seen.body["jql"] != "project = X" {
		t.Errorf("jql lost: %v", seen.body["jql"])
	}
	if total != 1 || len(out) != 1 || out[0].Key != "X-1" || out[0].Summary != "hi" {
		t.Errorf("flatten: %+v total=%d", out, total)
	}
	if out[0].Status.StatusCategory.Key != "new" {
		t.Errorf("status category lost: %+v", out[0].Status)
	}
}

func TestADFRoundTrip(t *testing.T) {
	want := "hello\nworld"
	doc := textToADF(want)
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	got := adfToText(raw)
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestADFEmptyText(t *testing.T) {
	if adfToText(nil) != "" {
		t.Error("nil ADF should give empty string")
	}
	doc := textToADF("")
	raw, _ := json.Marshal(doc)
	if got := adfToText(raw); got != "" {
		t.Errorf("empty ADF: %q", got)
	}
}

func TestADFLegacyString(t *testing.T) {
	// Server occasionally returns a plain JSON string for description.
	if got := adfToText(json.RawMessage(`"just a string"`)); got != "just a string" {
		t.Errorf("legacy string fallback failed: %q", got)
	}
}

func TestCreateIssueRequiresFields(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {})
	_, err := c.CreateIssue(context.Background(), CreateIssueRequest{Summary: "x"})
	if err == nil {
		t.Error("missing project_key + issue_type should fail")
	}
}

// `parent` is the system field covering epic-child AND subtask on create.
func TestCreateIssueSetsParent(t *testing.T) {
	var body map[string]any
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &body)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"1","key":"X-2"}`))
	})
	_, err := c.CreateIssue(context.Background(), CreateIssueRequest{
		ProjectKey: "X", IssueType: "Sub-task", Summary: "s", ParentKey: "X-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	fields, _ := body["fields"].(map[string]any)
	parent, _ := fields["parent"].(map[string]any)
	if parent == nil || parent["key"] != "X-1" {
		t.Errorf("parent not set correctly: %+v", fields["parent"])
	}
}

// Critical guard rail. epic_link WITHOUT epic_link_field must emit no
// customfield at all (a dual set on our own create = 400). With both, it emits
// the field.
func TestCreateIssueEpicLinkGating(t *testing.T) {
	var body map[string]any
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &body)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"1","key":"X-3"}`))
	})
	// EpicLinkKey only (no field) → no customfield_ may appear.
	if _, err := c.CreateIssue(context.Background(), CreateIssueRequest{
		ProjectKey: "X", IssueType: "Story", Summary: "s", EpicLinkKey: "X-1",
	}); err != nil {
		t.Fatal(err)
	}
	fields, _ := body["fields"].(map[string]any)
	for k := range fields {
		if strings.HasPrefix(k, "customfield_") {
			t.Errorf("epic_link with no field must NOT emit a customfield, leaked %q", k)
		}
	}
	// Both filled in → the named field is emitted.
	body = nil
	if _, err := c.CreateIssue(context.Background(), CreateIssueRequest{
		ProjectKey: "X", IssueType: "Story", Summary: "s",
		EpicLinkKey: "X-9", EpicLinkField: "customfield_10014",
	}); err != nil {
		t.Fatal(err)
	}
	fields, _ = body["fields"].(map[string]any)
	if fields["customfield_10014"] != "X-9" {
		t.Errorf("epic_link with a field must emit it: %+v", fields["customfield_10014"])
	}
}

// The UI needs subtask/hierarchyLevel to require a parent / label an Epic.
func TestIssueTypesParsesHierarchy(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"issueTypes":[{"id":"10010","name":"Story","subtask":false,"hierarchyLevel":0},{"id":"10087","name":"Sub-task","subtask":true,"hierarchyLevel":-1},{"id":"10000","name":"Epic","subtask":false,"hierarchyLevel":1}]}`))
	})
	out, err := c.IssueTypesForProject(context.Background(), "X")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 3 {
		t.Fatalf("want 3 types, got %d", len(out))
	}
	var sub, epic *CreateMetaIssueType
	for i := range out {
		switch out[i].Name {
		case "Sub-task":
			sub = &out[i]
		case "Epic":
			epic = &out[i]
		}
	}
	if sub == nil || !sub.Subtask || sub.HierarchyLevel != -1 {
		t.Errorf("wrong sub-task meta: %+v", sub)
	}
	if epic == nil || epic.Subtask || epic.HierarchyLevel != 1 {
		t.Errorf("wrong epic meta: %+v", epic)
	}
}

func TestTransitions(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"transitions":[{"id":"31","name":"Done","to":{"name":"Done","statusCategory":{"key":"done"}}}]}`))
	})
	out, err := c.Transitions(context.Background(), "X-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].ToCat != "done" {
		t.Errorf("transitions: %+v", out)
	}
}
