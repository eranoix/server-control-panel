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

type Client struct {
	site  string
	email string
	token string
	http  *http.Client
}

type Config struct {
	Site         string `json:"site"`
	Email        string `json:"email"`
	Token        string `json:"token,omitempty"`
	ProjectKey   string `json:"project_key,omitempty"`
	BoardJQL     string `json:"board_jql,omitempty"`
	BoardColumns string `json:"board_columns,omitempty"`
	HasToken     bool   `json:"has_token,omitempty"`
}

type BoardColumn struct {
	Label       string   `json:"label"`
	StatusNames []string `json:"status_names"`
}

var (
	ErrNotConfigured = errors.New("jira not configured")
	ErrUnauthorized  = errors.New("jira unauthorized (check email/token)")
	ErrRateLimited   = errors.New("jira rate-limited (429)")
)

var ErrBadSite = errors.New("jira site must be *.atlassian.net or *.jira.com")

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
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return nil, ErrBadSite
	}
	host := strings.ToLower(u.Host)
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

func (c *Client) Site() string { return c.site }

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
	total := len(out)
	if !resp.IsLast && resp.NextPageToken != "" {
		total = len(out) + 1
	}
	return out, total, nil
}

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

func NewForTests(baseURL, email, token string, hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: 5 * time.Second}
	}
	return &Client{site: strings.TrimRight(baseURL, "/"), email: email, token: token, http: hc}
}
