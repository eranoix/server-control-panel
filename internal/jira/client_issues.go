package jira

// client_issues.go — issue detail, create, transitions, comments,
// assignees, issue types.
//
// Covers GetIssue (with the rawIssue flatten), CreateIssue (POST /issue),
// Transitions / Transition (list + apply), Comments / AddComment,
// AssignableUsers (autocomplete for the form), IssueTypesForProject (the
// list of valid types per project).
//
// Split out of client.go (keeps the same *Client receiver).

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// --- issue detail ---

// IssueDetail extends Issue with description ADF (we flatten to text) plus
// the embedded collections the UI's detail drawer renders directly:
// subtasks, issuelinks (already paired with the "other" issue) and
// attachment metadata.
type IssueDetail struct {
	Issue
	Description string          `json:"description,omitempty"`
	Subtasks    []Issue         `json:"subtasks,omitempty"`
	IssueLinks  []FlatIssueLink `json:"issuelinks,omitempty"`
	Attachment  []Attachment    `json:"attachment,omitempty"`
}

// FlatIssueLink is the UI-friendly version: just "the other side" with
// a human label ("blocks", "is blocked by", "relates to") regardless of
// inward/outward direction.
type FlatIssueLink struct {
	ID       string       `json:"id"`
	Relation string       `json:"relation"` // e.g. "blocks", "is blocked by"
	Other    *LinkedIssue `json:"other,omitempty"`
}

func (c *Client) GetIssue(ctx context.Context, key string) (*IssueDetail, error) {
	var resp struct {
		ID     string `json:"id"`
		Key    string `json:"key"`
		Fields struct {
			Summary     string          `json:"summary"`
			Status      Status          `json:"status"`
			Priority    *NamedRef       `json:"priority,omitempty"`
			IssueType   *NamedRef       `json:"issuetype,omitempty"`
			Assignee    *User           `json:"assignee,omitempty"`
			Reporter    *User           `json:"reporter,omitempty"`
			Labels      []string        `json:"labels,omitempty"`
			Created     string          `json:"created,omitempty"`
			Updated     string          `json:"updated,omitempty"`
			DueDate     string          `json:"duedate,omitempty"`
			Project     *ProjectShort   `json:"project,omitempty"`
			Description json.RawMessage `json:"description,omitempty"`
			Subtasks    []struct {
				Key    string `json:"key"`
				Fields struct {
					Summary string `json:"summary"`
					Status  Status `json:"status"`
				} `json:"fields"`
			} `json:"subtasks,omitempty"`
			IssueLinks []struct {
				ID          string        `json:"id"`
				Type        IssueLinkType `json:"type"`
				InwardIssue *struct {
					Key    string `json:"key"`
					Fields struct {
						Summary string `json:"summary"`
						Status  Status `json:"status"`
					} `json:"fields"`
				} `json:"inwardIssue,omitempty"`
				OutwardIssue *struct {
					Key    string `json:"key"`
					Fields struct {
						Summary string `json:"summary"`
						Status  Status `json:"status"`
					} `json:"fields"`
				} `json:"outwardIssue,omitempty"`
			} `json:"issuelinks,omitempty"`
			Attachment []struct {
				ID        string `json:"id"`
				Filename  string `json:"filename"`
				MimeType  string `json:"mimeType"`
				Size      int64  `json:"size"`
				Created   string `json:"created"`
				Author    User   `json:"author"`
				Content   string `json:"content"`
				Thumbnail string `json:"thumbnail"`
			} `json:"attachment,omitempty"`
		} `json:"fields"`
	}
	if err := c.do(ctx, http.MethodGet, "/rest/api/3/issue/"+url.PathEscape(key), nil, &resp); err != nil {
		return nil, err
	}
	d := &IssueDetail{
		Issue: Issue{
			ID:        resp.ID,
			Key:       resp.Key,
			Summary:   resp.Fields.Summary,
			Status:    resp.Fields.Status,
			Priority:  resp.Fields.Priority,
			IssueType: resp.Fields.IssueType,
			Assignee:  resp.Fields.Assignee,
			Reporter:  resp.Fields.Reporter,
			Labels:    resp.Fields.Labels,
			Created:   resp.Fields.Created,
			Updated:   resp.Fields.Updated,
			DueDate:   resp.Fields.DueDate,
			Project:   resp.Fields.Project,
		},
		Description: adfToText(resp.Fields.Description),
	}
	for _, st := range resp.Fields.Subtasks {
		d.Subtasks = append(d.Subtasks, Issue{Key: st.Key, Summary: st.Fields.Summary, Status: st.Fields.Status})
	}
	for _, lnk := range resp.Fields.IssueLinks {
		fl := FlatIssueLink{ID: lnk.ID}
		if lnk.OutwardIssue != nil {
			fl.Relation = lnk.Type.Outward
			fl.Other = &LinkedIssue{Key: lnk.OutwardIssue.Key, Summary: lnk.OutwardIssue.Fields.Summary, Status: lnk.OutwardIssue.Fields.Status}
		} else if lnk.InwardIssue != nil {
			fl.Relation = lnk.Type.Inward
			fl.Other = &LinkedIssue{Key: lnk.InwardIssue.Key, Summary: lnk.InwardIssue.Fields.Summary, Status: lnk.InwardIssue.Fields.Status}
		}
		d.IssueLinks = append(d.IssueLinks, fl)
	}
	for _, att := range resp.Fields.Attachment {
		d.Attachment = append(d.Attachment, Attachment{
			ID: att.ID, Filename: att.Filename, MIMEType: att.MimeType, Size: att.Size,
			Created: att.Created, Author: att.Author, Content: att.Content, Thumbnail: att.Thumbnail,
		})
	}
	return d, nil
}

// --- create issue ---

type CreateIssueRequest struct {
	ProjectKey  string   `json:"project_key"`
	IssueType   string   `json:"issue_type"` // "Task", "Bug", "Story"…
	Summary     string   `json:"summary"`
	Description string   `json:"description,omitempty"`
	Priority    string   `json:"priority,omitempty"`
	Labels      []string `json:"labels,omitempty"`
	AssigneeID  string   `json:"assignee_id,omitempty"`
	DueDate     string   `json:"due_date,omitempty"` // YYYY-MM-DD
	// --- hierarchy on creation ---
	// ParentKey is the `parent` system field: it covers BOTH epic-child (Story/Task
	// under an Epic) AND subtask (Sub-task under any issue). It is the single
	// correct path on the create POST (proven against the tenant's create screen).
	ParentKey string `json:"parent_key,omitempty"`
	// EpicLinkKey/EpicLinkField: ONLY for tenants that expose the legacy
	// gh-epic-link (customfield_10014) ON the create screen. Gated by construction:
	// the builder emits the customfield only when BOTH are filled in. NEVER set
	// customfield_10014 alongside `parent` on a create POST — this panel's own
	// project has no such field on its create screen and the dual set would give a
	// 400 "Field cannot be set, not on the appropriate screen" (≠ UpdateIssue/PUT,
	// where the dual set is safe).
	EpicLinkKey   string `json:"epic_link,omitempty"`
	EpicLinkField string `json:"epic_link_field,omitempty"`
}

type CreatedIssue struct {
	ID  string `json:"id"`
	Key string `json:"key"`
}

func (c *Client) CreateIssue(ctx context.Context, in CreateIssueRequest) (*CreatedIssue, error) {
	if in.ProjectKey == "" || in.Summary == "" || in.IssueType == "" {
		return nil, fmt.Errorf("project_key + summary + issue_type required")
	}
	fields := map[string]any{
		"project":   map[string]string{"key": in.ProjectKey},
		"summary":   in.Summary,
		"issuetype": map[string]string{"name": in.IssueType},
	}
	if in.Description != "" {
		fields["description"] = textToADF(in.Description)
	}
	if in.Priority != "" {
		fields["priority"] = map[string]string{"name": in.Priority}
	}
	if len(in.Labels) > 0 {
		fields["labels"] = in.Labels
	}
	if in.AssigneeID != "" {
		fields["assignee"] = map[string]string{"accountId": in.AssigneeID}
	}
	if in.DueDate != "" {
		fields["duedate"] = in.DueDate
	}
	// Hierarchy. `parent` is the system field — it serves epic-child
	// (Story/Task under an Epic) AND subtask (Sub-task under any issue).
	if in.ParentKey != "" {
		fields["parent"] = map[string]string{"key": in.ParentKey}
	}
	// Gated: it only fires on a FUTURE tenant that exposes gh-epic-link on the
	// create screen. Setting customfield_10014 here (as UpdateIssue does on the PUT)
	// = a 400 on our own create. That is why it stays strictly separate from
	// `parent`, never dual-set. DO NOT REMOVE the `&&` gate: it is the guard rail
	// that keeps the branch inert for us.
	if in.EpicLinkKey != "" && in.EpicLinkField != "" {
		fields[in.EpicLinkField] = in.EpicLinkKey
	}
	var resp CreatedIssue
	if err := c.do(ctx, http.MethodPost, "/rest/api/3/issue", map[string]any{"fields": fields}, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// --- transitions ---

type Transition struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	ToName string `json:"to_name,omitempty"` // status name after transition
	ToCat  string `json:"to_cat,omitempty"`  // status category key (new/indeterminate/done)
}

func (c *Client) Transitions(ctx context.Context, key string) ([]Transition, error) {
	var resp struct {
		Transitions []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			To   struct {
				Name           string         `json:"name"`
				StatusCategory StatusCategory `json:"statusCategory"`
			} `json:"to"`
		} `json:"transitions"`
	}
	if err := c.do(ctx, http.MethodGet, "/rest/api/3/issue/"+url.PathEscape(key)+"/transitions", nil, &resp); err != nil {
		return nil, err
	}
	out := make([]Transition, 0, len(resp.Transitions))
	for _, t := range resp.Transitions {
		out = append(out, Transition{ID: t.ID, Name: t.Name, ToName: t.To.Name, ToCat: t.To.StatusCategory.Key})
	}
	return out, nil
}

func (c *Client) Transition(ctx context.Context, key, transitionID string) error {
	body := map[string]any{"transition": map[string]string{"id": transitionID}}
	return c.do(ctx, http.MethodPost, "/rest/api/3/issue/"+url.PathEscape(key)+"/transitions", body, nil)
}

// --- comments ---

type Comment struct {
	ID      string `json:"id"`
	Body    string `json:"body"`
	Author  User   `json:"author"`
	Created string `json:"created"`
	Updated string `json:"updated,omitempty"`
}

func (c *Client) Comments(ctx context.Context, key string) ([]Comment, error) {
	var resp struct {
		Comments []struct {
			ID      string          `json:"id"`
			Body    json.RawMessage `json:"body"`
			Author  User            `json:"author"`
			Created string          `json:"created"`
			Updated string          `json:"updated,omitempty"`
		} `json:"comments"`
	}
	if err := c.do(ctx, http.MethodGet, "/rest/api/3/issue/"+url.PathEscape(key)+"/comment", nil, &resp); err != nil {
		return nil, err
	}
	out := make([]Comment, 0, len(resp.Comments))
	for _, raw := range resp.Comments {
		out = append(out, Comment{
			ID:      raw.ID,
			Body:    adfToText(raw.Body),
			Author:  raw.Author,
			Created: raw.Created,
			Updated: raw.Updated,
		})
	}
	return out, nil
}

func (c *Client) AddComment(ctx context.Context, key, text string) (*Comment, error) {
	body := map[string]any{"body": textToADF(text)}
	var resp struct {
		ID      string          `json:"id"`
		Body    json.RawMessage `json:"body"`
		Author  User            `json:"author"`
		Created string          `json:"created"`
	}
	if err := c.do(ctx, http.MethodPost, "/rest/api/3/issue/"+url.PathEscape(key)+"/comment", body, &resp); err != nil {
		return nil, err
	}
	return &Comment{ID: resp.ID, Body: text, Author: resp.Author, Created: resp.Created}, nil
}

// --- assignee + priority lookups (for the create form) ---

type AssignableUser = User

func (c *Client) AssignableUsers(ctx context.Context, projectKey, query string) ([]AssignableUser, error) {
	if projectKey == "" {
		return nil, fmt.Errorf("project key required")
	}
	q := url.Values{}
	q.Set("project", projectKey)
	if query != "" {
		q.Set("query", query)
	}
	q.Set("maxResults", "20")
	var list []AssignableUser
	if err := c.do(ctx, http.MethodGet, "/rest/api/3/user/assignable/search?"+q.Encode(), nil, &list); err != nil {
		return nil, err
	}
	return list, nil
}

// CreateMeta returns the issue types + required fields for a project so
// the UI can populate the "issue type" dropdown without hardcoding names.
type CreateMetaIssueType struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	IconURL     string `json:"iconUrl,omitempty"`
	// The UI uses these for hierarchy on creation. `Subtask==true` → require a
	// parent issue (parent is mandatory). `HierarchyLevel==1` → it is an Epic
	// (hide the parent field; the issue is created at the top). They come straight
	// from Jira's /issue/createmeta/{key}/issuetypes payload.
	Subtask        bool `json:"subtask"`
	HierarchyLevel int  `json:"hierarchyLevel"`
}

func (c *Client) IssueTypesForProject(ctx context.Context, projectKey string) ([]CreateMetaIssueType, error) {
	var resp struct {
		IssueTypes []CreateMetaIssueType `json:"issueTypes"`
	}
	if err := c.do(ctx, http.MethodGet, "/rest/api/3/issue/createmeta/"+url.PathEscape(projectKey)+"/issuetypes", nil, &resp); err != nil {
		return nil, err
	}
	return resp.IssueTypes, nil
}
