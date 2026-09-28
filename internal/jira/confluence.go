package jira

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

type ConfluenceSpace struct {
	ID     string `json:"id"`
	Key    string `json:"key"`
	Name   string `json:"name"`
	Type   string `json:"type"`
	Status string `json:"status,omitempty"`
}

func (c *Client) ConfluenceSpaces(ctx context.Context) ([]ConfluenceSpace, error) {
	var resp struct {
		Results []ConfluenceSpace `json:"results"`
	}
	if err := c.do(ctx, http.MethodGet, "/wiki/api/v2/spaces?limit=100", nil, &resp); err != nil {
		return nil, err
	}
	return resp.Results, nil
}

type ConfluencePage struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Status    string `json:"status,omitempty"`
	SpaceID   string `json:"spaceId,omitempty"`
	ParentID  string `json:"parentId,omitempty"`
	CreatedAt string `json:"createdAt,omitempty"`
}

func (c *Client) ConfluencePages(ctx context.Context, spaceID string, limit int, cursor string) ([]ConfluencePage, string, error) {
	if limit <= 0 || limit > 250 {
		limit = 50
	}
	q := url.Values{}
	q.Set("limit", fmt.Sprintf("%d", limit))
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	path := "/wiki/api/v2/spaces/" + url.PathEscape(spaceID) + "/pages?" + q.Encode()
	var resp struct {
		Results []ConfluencePage `json:"results"`
		Links   struct {
			Next string `json:"next"`
		} `json:"_links"`
	}
	if err := c.do(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, "", err
	}
	next := ""
	if resp.Links.Next != "" {
		if u, err := url.Parse(resp.Links.Next); err == nil {
			next = u.Query().Get("cursor")
		}
	}
	return resp.Results, next, nil
}

type ConfluencePageDetail struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	SpaceID   string `json:"spaceId,omitempty"`
	ParentID  string `json:"parentId,omitempty"`
	Status    string `json:"status,omitempty"`
	Body      string `json:"body,omitempty"`
	BodyADF   any    `json:"body_adf,omitempty"`
	WebURL    string `json:"web_url,omitempty"`
	CreatedAt string `json:"createdAt,omitempty"`
	Version   int    `json:"version,omitempty"`
}

func (c *Client) ConfluencePage(ctx context.Context, pageID string) (*ConfluencePageDetail, error) {
	path := "/wiki/api/v2/pages/" + url.PathEscape(pageID) + "?body-format=atlas_doc_format"
	var resp struct {
		ID       string `json:"id"`
		Title    string `json:"title"`
		SpaceID  string `json:"spaceId"`
		ParentID string `json:"parentId"`
		Status   string `json:"status"`
		Body     struct {
			AtlasDocFormat struct {
				Value json.RawMessage `json:"value"`
			} `json:"atlas_doc_format"`
		} `json:"body"`
		Version struct {
			Number int `json:"number"`
		} `json:"version"`
		Links struct {
			WebUI string `json:"webui"`
		} `json:"_links"`
		CreatedAt string `json:"createdAt"`
	}
	if err := c.do(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, err
	}
	d := &ConfluencePageDetail{
		ID: resp.ID, Title: resp.Title, SpaceID: resp.SpaceID, ParentID: resp.ParentID,
		Status: resp.Status, Body: adfToText(resp.Body.AtlasDocFormat.Value),
		Version: resp.Version.Number, CreatedAt: resp.CreatedAt,
	}
	if resp.Links.WebUI != "" {
		d.WebURL = c.site + "/wiki" + resp.Links.WebUI
	}
	return d, nil
}
