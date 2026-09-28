package jira

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
)

type UpdateIssueRequest struct {
	Summary          *string   `json:"summary,omitempty"`
	Description      *string   `json:"description,omitempty"`
	Priority         *string   `json:"priority,omitempty"`
	AssigneeID       *string   `json:"assignee_id,omitempty"`
	Labels           *[]string `json:"labels,omitempty"`
	DueDate          *string   `json:"due_date,omitempty"`
	StoryPoints      *float64  `json:"story_points,omitempty"`
	StoryPointsField string    `json:"story_points_field,omitempty"`
	ComponentNames   *[]string `json:"components,omitempty"`
	FixVersionNames  *[]string `json:"fix_versions,omitempty"`
	EpicLinkKey      *string   `json:"epic_link,omitempty"`
	IssueType        *string   `json:"issue_type,omitempty"`
}

func (c *Client) UpdateIssue(ctx context.Context, key string, in UpdateIssueRequest) error {
	fields := map[string]any{}
	if in.Summary != nil {
		fields["summary"] = *in.Summary
	}
	if in.Description != nil {
		fields["description"] = textToADF(*in.Description)
	}
	if in.Priority != nil {
		if *in.Priority == "" {
			fields["priority"] = nil
		} else {
			fields["priority"] = map[string]string{"name": *in.Priority}
		}
	}
	if in.AssigneeID != nil {
		if *in.AssigneeID == "" {
			fields["assignee"] = nil
		} else {
			fields["assignee"] = map[string]string{"accountId": *in.AssigneeID}
		}
	}
	if in.Labels != nil {
		fields["labels"] = *in.Labels
	}
	if in.DueDate != nil {
		if *in.DueDate == "" {
			fields["duedate"] = nil
		} else {
			fields["duedate"] = *in.DueDate
		}
	}
	if in.StoryPoints != nil {
		f := in.StoryPointsField
		if f == "" {
			f = "customfield_10016"
		}
		fields[f] = *in.StoryPoints
	}
	if in.ComponentNames != nil {
		arr := make([]map[string]string, 0, len(*in.ComponentNames))
		for _, n := range *in.ComponentNames {
			arr = append(arr, map[string]string{"name": n})
		}
		fields["components"] = arr
	}
	if in.FixVersionNames != nil {
		arr := make([]map[string]string, 0, len(*in.FixVersionNames))
		for _, n := range *in.FixVersionNames {
			arr = append(arr, map[string]string{"name": n})
		}
		fields["fixVersions"] = arr
	}
	if in.EpicLinkKey != nil {
		if *in.EpicLinkKey == "" {
			fields["parent"] = nil
			fields["customfield_10014"] = nil
		} else {
			fields["parent"] = map[string]string{"key": *in.EpicLinkKey}
			fields["customfield_10014"] = *in.EpicLinkKey
		}
	}
	if in.IssueType != nil && *in.IssueType != "" {
		fields["issuetype"] = map[string]string{"name": *in.IssueType}
	}
	if len(fields) == 0 {
		return nil
	}
	return c.do(ctx, http.MethodPut, "/rest/api/3/issue/"+url.PathEscape(key), map[string]any{"fields": fields}, nil)
}

type WatcherList struct {
	Watching   bool   `json:"watching"`
	WatchCount int    `json:"watch_count"`
	Watchers   []User `json:"watchers"`
}

func (c *Client) Watchers(ctx context.Context, key string) (*WatcherList, error) {
	var resp struct {
		IsWatching bool   `json:"isWatching"`
		WatchCount int    `json:"watchCount"`
		Watchers   []User `json:"watchers"`
	}
	if err := c.do(ctx, http.MethodGet, "/rest/api/3/issue/"+url.PathEscape(key)+"/watchers", nil, &resp); err != nil {
		return nil, err
	}
	return &WatcherList{Watching: resp.IsWatching, WatchCount: resp.WatchCount, Watchers: resp.Watchers}, nil
}

func (c *Client) AddWatcher(ctx context.Context, key, accountID string) error {
	var body any
	if accountID != "" {
		body = accountID
	}
	return c.do(ctx, http.MethodPost, "/rest/api/3/issue/"+url.PathEscape(key)+"/watchers", body, nil)
}

func (c *Client) RemoveWatcher(ctx context.Context, key, accountID string) error {
	q := ""
	if accountID != "" {
		q = "?accountId=" + url.QueryEscape(accountID)
	}
	return c.do(ctx, http.MethodDelete, "/rest/api/3/issue/"+url.PathEscape(key)+"/watchers"+q, nil, nil)
}

type IssueLinkType struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Inward  string `json:"inward"`
	Outward string `json:"outward"`
}

func (c *Client) IssueLinkTypes(ctx context.Context) ([]IssueLinkType, error) {
	var resp struct {
		IssueLinkTypes []IssueLinkType `json:"issueLinkTypes"`
	}
	if err := c.do(ctx, http.MethodGet, "/rest/api/3/issueLinkType", nil, &resp); err != nil {
		return nil, err
	}
	return resp.IssueLinkTypes, nil
}

type IssueLink struct {
	ID      string        `json:"id"`
	Type    IssueLinkType `json:"type"`
	Inward  *LinkedIssue  `json:"inward_issue,omitempty"`
	Outward *LinkedIssue  `json:"outward_issue,omitempty"`
}

type LinkedIssue struct {
	Key     string `json:"key"`
	Summary string `json:"summary"`
	Status  Status `json:"status"`
}

func (c *Client) CreateIssueLink(ctx context.Context, linkType, inwardKey, outwardKey string) error {
	body := map[string]any{
		"type":         map[string]string{"name": linkType},
		"inwardIssue":  map[string]string{"key": inwardKey},
		"outwardIssue": map[string]string{"key": outwardKey},
	}
	return c.do(ctx, http.MethodPost, "/rest/api/3/issueLink", body, nil)
}

func (c *Client) DeleteIssueLink(ctx context.Context, linkID string) error {
	return c.do(ctx, http.MethodDelete, "/rest/api/3/issueLink/"+url.PathEscape(linkID), nil, nil)
}

type Worklog struct {
	ID               string `json:"id"`
	Author           User   `json:"author"`
	Comment          string `json:"comment,omitempty"`
	Started          string `json:"started,omitempty"`
	TimeSpent        string `json:"time_spent,omitempty"`
	TimeSpentSeconds int64  `json:"time_spent_seconds,omitempty"`
}

func (c *Client) Worklogs(ctx context.Context, key string) ([]Worklog, error) {
	var resp struct {
		Worklogs []struct {
			ID               string          `json:"id"`
			Author           User            `json:"author"`
			Comment          json.RawMessage `json:"comment,omitempty"`
			Started          string          `json:"started,omitempty"`
			TimeSpent        string          `json:"timeSpent,omitempty"`
			TimeSpentSeconds int64           `json:"timeSpentSeconds,omitempty"`
		} `json:"worklogs"`
	}
	if err := c.do(ctx, http.MethodGet, "/rest/api/3/issue/"+url.PathEscape(key)+"/worklog", nil, &resp); err != nil {
		return nil, err
	}
	out := make([]Worklog, 0, len(resp.Worklogs))
	for _, raw := range resp.Worklogs {
		out = append(out, Worklog{
			ID: raw.ID, Author: raw.Author,
			Comment: adfToText(raw.Comment), Started: raw.Started,
			TimeSpent: raw.TimeSpent, TimeSpentSeconds: raw.TimeSpentSeconds,
		})
	}
	return out, nil
}

type AddWorklogRequest struct {
	TimeSpent string `json:"time_spent"`
	Comment   string `json:"comment,omitempty"`
	Started   string `json:"started,omitempty"`
}

func (c *Client) AddWorklog(ctx context.Context, key string, in AddWorklogRequest) error {
	body := map[string]any{"timeSpent": in.TimeSpent}
	if in.Comment != "" {
		body["comment"] = textToADF(in.Comment)
	}
	if in.Started != "" {
		body["started"] = in.Started
	}
	return c.do(ctx, http.MethodPost, "/rest/api/3/issue/"+url.PathEscape(key)+"/worklog", body, nil)
}

type ChangeEntry struct {
	ID      string       `json:"id"`
	Author  User         `json:"author"`
	Created string       `json:"created"`
	Items   []ChangeItem `json:"items"`
}

type ChangeItem struct {
	Field      string `json:"field"`
	FromString string `json:"from_string,omitempty"`
	ToString   string `json:"to_string,omitempty"`
}

func (c *Client) Changelog(ctx context.Context, key string) ([]ChangeEntry, error) {
	var resp struct {
		Values []struct {
			ID      string `json:"id"`
			Author  User   `json:"author"`
			Created string `json:"created"`
			Items   []struct {
				Field      string `json:"field"`
				FromString string `json:"fromString"`
				ToString   string `json:"toString"`
			} `json:"items"`
		} `json:"values"`
	}
	if err := c.do(ctx, http.MethodGet, "/rest/api/3/issue/"+url.PathEscape(key)+"/changelog?maxResults=100", nil, &resp); err != nil {
		return nil, err
	}
	out := make([]ChangeEntry, 0, len(resp.Values))
	for _, raw := range resp.Values {
		items := make([]ChangeItem, 0, len(raw.Items))
		for _, it := range raw.Items {
			items = append(items, ChangeItem{Field: it.Field, FromString: it.FromString, ToString: it.ToString})
		}
		out = append(out, ChangeEntry{ID: raw.ID, Author: raw.Author, Created: raw.Created, Items: items})
	}
	return out, nil
}

func (c *Client) PickIssues(ctx context.Context, query, currentJQL string) ([]Issue, error) {
	q := url.Values{}
	q.Set("query", query)
	if currentJQL != "" {
		q.Set("currentJQL", currentJQL)
	}
	var resp struct {
		Sections []struct {
			Issues []struct {
				Key       string `json:"key"`
				Summary   string `json:"summaryText"`
				Status    string `json:"status"`
				IssueType string `json:"issueTypeIconUrl"`
			} `json:"issues"`
		} `json:"sections"`
	}
	if err := c.do(ctx, http.MethodGet, "/rest/api/3/issue/picker?"+q.Encode(), nil, &resp); err != nil {
		return nil, err
	}
	out := []Issue{}
	for _, sec := range resp.Sections {
		for _, i := range sec.Issues {
			out = append(out, Issue{Key: i.Key, Summary: i.Summary})
		}
	}
	return out, nil
}

func (c *Client) Priorities(ctx context.Context) ([]NamedRef, error) {
	var out []NamedRef
	if err := c.do(ctx, http.MethodGet, "/rest/api/3/priority", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) DeleteIssue(ctx context.Context, key string, deleteSubtasks bool) error {
	path := "/rest/api/3/issue/" + url.PathEscape(key)
	if deleteSubtasks {
		path += "?deleteSubtasks=true"
	}
	return c.do(ctx, http.MethodDelete, path, nil, nil)
}

func (c *Client) CloneIssue(ctx context.Context, key string) (*CreatedIssue, error) {
	src, err := c.GetIssue(ctx, key)
	if err != nil {
		return nil, err
	}
	in := CreateIssueRequest{
		ProjectKey:  src.Project.Key,
		Summary:     src.Summary + " (copy)",
		Description: src.Description,
		Labels:      src.Labels,
	}
	if src.IssueType != nil {
		in.IssueType = src.IssueType.Name
	}
	if src.Priority != nil {
		in.Priority = src.Priority.Name
	}
	if src.Assignee != nil {
		in.AssigneeID = src.Assignee.AccountID
	}
	if src.DueDate != "" {
		in.DueDate = src.DueDate
	}
	return c.CreateIssue(ctx, in)
}

func (c *Client) UpdateComment(ctx context.Context, key, commentID, body string) (*Comment, error) {
	payload := map[string]any{"body": textToADF(body)}
	var resp struct {
		ID      string `json:"id"`
		Author  User   `json:"author"`
		Created string `json:"created"`
		Updated string `json:"updated"`
	}
	if err := c.do(ctx, http.MethodPut, "/rest/api/3/issue/"+url.PathEscape(key)+"/comment/"+url.PathEscape(commentID), payload, &resp); err != nil {
		return nil, err
	}
	return &Comment{ID: resp.ID, Body: body, Author: resp.Author, Created: resp.Created, Updated: resp.Updated}, nil
}

func (c *Client) DeleteComment(ctx context.Context, key, commentID string) error {
	return c.do(ctx, http.MethodDelete, "/rest/api/3/issue/"+url.PathEscape(key)+"/comment/"+url.PathEscape(commentID), nil, nil)
}

type VoteInfo struct {
	Votes    int  `json:"votes"`
	HasVoted bool `json:"has_voted"`
}

func (c *Client) GetVotes(ctx context.Context, key string) (*VoteInfo, error) {
	var resp struct {
		Votes    int  `json:"votes"`
		HasVoted bool `json:"hasVoted"`
	}
	if err := c.do(ctx, http.MethodGet, "/rest/api/3/issue/"+url.PathEscape(key)+"/votes", nil, &resp); err != nil {
		return nil, err
	}
	return &VoteInfo{Votes: resp.Votes, HasVoted: resp.HasVoted}, nil
}

func (c *Client) AddVote(ctx context.Context, key string) error {
	return c.do(ctx, http.MethodPost, "/rest/api/3/issue/"+url.PathEscape(key)+"/votes", nil, nil)
}

func (c *Client) RemoveVote(ctx context.Context, key string) error {
	return c.do(ctx, http.MethodDelete, "/rest/api/3/issue/"+url.PathEscape(key)+"/votes", nil, nil)
}

type Version struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Released    bool   `json:"released,omitempty"`
	Archived    bool   `json:"archived,omitempty"`
	ReleaseDate string `json:"releaseDate,omitempty"`
}

func (c *Client) Versions(ctx context.Context, projectKey string) ([]Version, error) {
	var out []Version
	if err := c.do(ctx, http.MethodGet, "/rest/api/3/project/"+url.PathEscape(projectKey)+"/versions", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

type Component struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (c *Client) Components(ctx context.Context, projectKey string) ([]Component, error) {
	var out []Component
	if err := c.do(ctx, http.MethodGet, "/rest/api/3/project/"+url.PathEscape(projectKey)+"/components", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) Epics(ctx context.Context, projectKey string) ([]Issue, error) {
	jql := "project = " + projectKey + " AND issuetype = Epic AND statusCategory != Done ORDER BY summary"
	out, _, err := c.Search(ctx, jql, 0, 50)
	return out, err
}

func basicAuth(cred string) string {
	return base64.StdEncoding.EncodeToString([]byte(cred))
}
