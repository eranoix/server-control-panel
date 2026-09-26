// Package jira is a thin client for Atlassian Cloud REST v3 — just the
// surface the vps-manager UI needs (search, issue CRUD, transitions,
// comments, projects, attachments).
//
// Auth: HTTP Basic with email + API token. Tokens come from
// id.atlassian.com/manage-profile/security/api-tokens and are scoped to
// the user. Stored per-user in the secrets vault (never on disk in
// plaintext); see internal/api/handlers_jira.go for the wiring.
//
// We deliberately do NOT model the full Jira surface — only what the
// kanban + comment flows need. Adding fields is cheap; removing them is
// not. When in doubt, leave them out.
package jira

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client is a configured Jira Cloud client.
type Client struct {
	site  string // https://jordan.atlassian.net (no trailing slash)
	email string
	token string
	http  *http.Client
}

// Config carries the fields persisted in the vault.
type Config struct {
	Site         string `json:"site"`
	Email        string `json:"email"`
	Token        string `json:"token,omitempty"`         // never returned to UI
	ProjectKey   string `json:"project_key,omitempty"`   // default project for "new issue"
	BoardJQL     string `json:"board_jql,omitempty"`     // optional override for the kanban query
	BoardColumns string `json:"board_columns,omitempty"` // JSON array of BoardColumn (custom kanban columns by status NAME). Falls back to category-based 3 cols when empty.
	HasToken     bool   `json:"has_token,omitempty"`     // mirror returned to UI
}

// BoardColumn lets the operator override the default category-based
// kanban with explicit columns. Status names are matched case-insensitive
// against issue.fields.status.name — useful for workflows that have
// extra states like "EM REVISÃO" or "Code Review" that all fall under
// statusCategory=indeterminate.
type BoardColumn struct {
	Label       string   `json:"label"`
	StatusNames []string `json:"status_names"`
}

// Errors callers can switch on.
var (
	ErrNotConfigured = errors.New("jira not configured")
	ErrUnauthorized  = errors.New("jira unauthorized (check email/token)")
	ErrRateLimited   = errors.New("jira rate-limited (429)")
)

// ErrBadSite means the configured site host is not a recognised
// Atlassian Cloud subdomain — refusing prevents SSRF where a malicious
// vault entry could point at internal services (169.254/16, localhost,
// 10.0.0.0/8) and exfiltrate their responses through our handler.
var ErrBadSite = errors.New("jira site must be *.atlassian.net or *.jira.com")

// New constructs a Client. site can come in as either "jordan" or
// "https://jordan.atlassian.net" — we normalize both, then validate the
// final host is on the public Atlassian DNS namespace to prevent SSRF.
func New(site, email, token string) (*Client, error) {
	if site == "" || email == "" || token == "" {
		return nil, ErrNotConfigured
	}
	s := strings.TrimSpace(site)
	if !strings.HasPrefix(s, "http") {
		s = "https://" + s
		if !strings.Contains(s, ".") {
			s += ".atlassian.net"
		}
	}
	s = strings.TrimRight(s, "/")
	// SSRF guard: only allow public Atlassian Cloud hostnames. Self-hosted
	// Server/Data Center should add their host here explicitly after the
	// admin has reviewed network exposure — we deliberately don't accept
	// arbitrary URLs (which would let any authenticated user use this
	// process as a metadata-service / internal-network probe).
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return nil, ErrBadSite
	}
	host := strings.ToLower(u.Host)
	// strip optional :port
	if i := strings.Index(host, ":"); i >= 0 {
		host = host[:i]
	}
	switch {
	case strings.HasSuffix(host, ".atlassian.net"):
	case strings.HasSuffix(host, ".jira.com"):
	default:
		return nil, ErrBadSite
	}
	return &Client{
		site:  s,
		email: email,
		token: token,
		http:  &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// Site returns the normalised site URL — useful for building "open in Jira"
// links in the UI.
func (c *Client) Site() string { return c.site }

// do is the single request-builder. Sets basic auth, accept JSON, body
// encoding. Returns ErrUnauthorized on 401/403, ErrRateLimited on 429,
// and a generic error with the response body for anything else non-2xx.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var reqBody io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reqBody = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.site+path, reqBody)
	if err != nil {
		return err
	}
	cred := c.email + ":" + c.token
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(cred)))
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == 401 || resp.StatusCode == 403:
		return ErrUnauthorized
	case resp.StatusCode == 429:
		return ErrRateLimited
	case resp.StatusCode >= 300:
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("jira %s %s: %d %s", method, path, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// --- self / health ---

type Myself struct {
	AccountID    string `json:"accountId"`
	DisplayName  string `json:"displayName"`
	EmailAddress string `json:"emailAddress"`
}

func (c *Client) Myself(ctx context.Context) (*Myself, error) {
	var m Myself
	if err := c.do(ctx, http.MethodGet, "/rest/api/3/myself", nil, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// --- projects ---

type Project struct {
	ID   string `json:"id"`
	Key  string `json:"key"`
	Name string `json:"name"`
}

func (c *Client) Projects(ctx context.Context) ([]Project, error) {
	var page struct {
		Values []Project `json:"values"`
	}
	if err := c.do(ctx, http.MethodGet, "/rest/api/3/project/search?maxResults=100", nil, &page); err != nil {
		return nil, err
	}
	return page.Values, nil
}

// --- search ---

// Issue is the lean shape returned to the UI. Add fields as the UI grows.
type Issue struct {
	ID        string        `json:"id"`
	Key       string        `json:"key"`
	Summary   string        `json:"summary"`
	Status    Status        `json:"status"`
	Priority  *NamedRef     `json:"priority,omitempty"`
	IssueType *NamedRef     `json:"issuetype,omitempty"`
	Assignee  *User         `json:"assignee,omitempty"`
	Reporter  *User         `json:"reporter,omitempty"`
	Labels    []string      `json:"labels,omitempty"`
	Created   string        `json:"created,omitempty"`
	Updated   string        `json:"updated,omitempty"`
	DueDate   string        `json:"duedate,omitempty"`
	Project   *ProjectShort `json:"project,omitempty"`
}

type Status struct {
	Name           string         `json:"name"`
	StatusCategory StatusCategory `json:"statusCategory"`
}

// StatusCategory.Key is the stable identifier Jira guarantees: one of
// "new" (To Do), "indeterminate" (In Progress), "done" (Done). Status
// names are translatable / customisable; categories are not.
type StatusCategory struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

type NamedRef struct {
	ID      string `json:"id,omitempty"`
	Name    string `json:"name,omitempty"`
	IconURL string `json:"iconUrl,omitempty"`
}

type User struct {
	AccountID    string            `json:"accountId"`
	DisplayName  string            `json:"displayName"`
	EmailAddress string            `json:"emailAddress,omitempty"`
	AvatarURLs   map[string]string `json:"avatarUrls,omitempty"`
	Active       bool              `json:"active,omitempty"`
}

// AvatarURL returns the largest avatar Jira provided, or "" if none.
// Convenience for UI templates that want a single string.
func (u User) AvatarURL() string {
	if u.AvatarURLs == nil {
		return ""
	}
	for _, k := range []string{"48x48", "32x32", "24x24", "16x16"} {
		if v := u.AvatarURLs[k]; v != "" {
			return v
		}
	}
	return ""
}

type ProjectShort struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

// Search runs a JQL query against the new /rest/api/3/search/jql endpoint
// (Atlassian deprecated /search in 2024-12 and removed startAt-based
// paging — pagination is now cursor-based via nextPageToken).
//
// The signature keeps the legacy (startAt, maxResults) shape so callers
// don't have to change; startAt becomes pagination token slot (string-
// encoded int for compatibility) and the returned "total" is best-effort
// — Jira no longer returns a precise count, so we report len(issues)
// when the upstream omits it. Callers needing exact totals should call
// the older /search endpoint directly (deprecation warning ignored).
func (c *Client) Search(ctx context.Context, jql string, startAt, maxResults int) ([]Issue, int, error) {
	if jql == "" {
		jql = "assignee = currentUser() AND statusCategory != Done ORDER BY updated DESC"
	}
	if maxResults <= 0 || maxResults > 100 {
		maxResults = 50
	}
	body := map[string]any{
		"jql":        jql,
		"maxResults": maxResults,
		"fields":     []string{"summary", "status", "priority", "issuetype", "assignee", "reporter", "labels", "created", "updated", "duedate", "project"},
	}
	// nextPageToken would go here for the second page onward; we ignore
	// startAt for now since the UI also reloads from scratch on filter
	// changes. Plumb in token-based paging when a caller actually needs it.
	_ = startAt
	var resp struct {
		Issues        []rawIssue `json:"issues"`
		NextPageToken string     `json:"nextPageToken,omitempty"`
		IsLast        bool       `json:"isLast,omitempty"`
	}
	if err := c.do(ctx, http.MethodPost, "/rest/api/3/search/jql", body, &resp); err != nil {
		return nil, 0, err
	}
	out := make([]Issue, 0, len(resp.Issues))
	for _, ri := range resp.Issues {
		out = append(out, ri.flatten())
	}
	// Best-effort total: when isLast we know the count; otherwise it's
	// a lower bound. UI displays "N issues" so this is good enough.
	total := len(out)
	if !resp.IsLast && resp.NextPageToken != "" {
		// signal "+" — the UI shows N/total so any number > N reads as "more".
		total = len(out) + 1
	}
	return out, total, nil
}

// rawIssue mirrors the wire shape — fields live under .fields.
type rawIssue struct {
	ID     string `json:"id"`
	Key    string `json:"key"`
	Fields struct {
		Summary   string        `json:"summary"`
		Status    Status        `json:"status"`
		Priority  *NamedRef     `json:"priority,omitempty"`
		IssueType *NamedRef     `json:"issuetype,omitempty"`
		Assignee  *User         `json:"assignee,omitempty"`
		Reporter  *User         `json:"reporter,omitempty"`
		Labels    []string      `json:"labels,omitempty"`
		Created   string        `json:"created,omitempty"`
		Updated   string        `json:"updated,omitempty"`
		DueDate   string        `json:"duedate,omitempty"`
		Project   *ProjectShort `json:"project,omitempty"`
	} `json:"fields"`
}

func (r rawIssue) flatten() Issue {
	return Issue{
		ID:        r.ID,
		Key:       r.Key,
		Summary:   r.Fields.Summary,
		Status:    r.Fields.Status,
		Priority:  r.Fields.Priority,
		IssueType: r.Fields.IssueType,
		Assignee:  r.Fields.Assignee,
		Reporter:  r.Fields.Reporter,
		Labels:    r.Fields.Labels,
		Created:   r.Fields.Created,
		Updated:   r.Fields.Updated,
		DueDate:   r.Fields.DueDate,
		Project:   r.Fields.Project,
	}
}

// NewForTests builds a Client pointed at an arbitrary URL, bypassing the
// allowed-host list [New] enforces.
//
// It exists so the packages that USE this client can be exercised against a
// fake `httptest` Jira — in particular the mobile BFF's kanban board
// (internal/mobilebff/handlers_jira.go), whose transition-refusal rule is only
// observable through a real response.
//
// NEVER use it in production code: the host validation it skips is the SSRF
// protection described in [New]. The name is long and explicit on purpose — a
// call to this outside a test has to jump out in review.
func NewForTests(baseURL, email, token string, hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: 5 * time.Second}
	}
	return &Client{site: strings.TrimRight(baseURL, "/"), email: email, token: token, http: hc}
}
