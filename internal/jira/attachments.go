// attachments.go — upload/download/delete of attachments on Jira issues.
//
// Multipart streaming via io.Pipe (constant memory, independent of the file
// size). The X-Atlassian-Token: no-check header bypasses the CSRF guard Jira
// demands on any file POST.
//
// Split out of extra.go to keep the multipart logic isolated.
package jira

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
)

// --- Attachments ---

type Attachment struct {
	ID        string `json:"id"`
	Filename  string `json:"filename"`
	MIMEType  string `json:"mime_type"`
	Size      int64  `json:"size"`
	Created   string `json:"created"`
	Author    User   `json:"author"`
	Content   string `json:"content"` // download URL (needs auth)
	Thumbnail string `json:"thumbnail,omitempty"`
}

// UploadAttachment multipart-posts a file to the issue. Jira requires
// the `X-Atlassian-Token: no-check` header (CSRF guard bypass for API).
//
// Streaming: the previous version buffered the entire file into a
// bytes.Buffer in memory before sending, doubling RAM for every upload
// and OOM-killing the server on large attachments. We now use io.Pipe
// so the multipart encoding is consumed by the http transport as it
// reads from the caller's source — constant memory regardless of size.
func (c *Client) UploadAttachment(ctx context.Context, key, filename string, body io.Reader) ([]Attachment, error) {
	pr, pw := io.Pipe()
	mp := multipart.NewWriter(pw)
	go func() {
		var err error
		defer func() {
			// CloseWithError surfaces the multipart write failure as a
			// read error in the request — better than http hanging on
			// truncated body.
			_ = mp.Close()
			_ = pw.CloseWithError(err)
		}()
		fw, e := mp.CreateFormFile("file", filename)
		if e != nil {
			err = e
			return
		}
		if _, e := io.Copy(fw, body); e != nil {
			err = e
		}
	}()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.site+"/rest/api/3/issue/"+url.PathEscape(key)+"/attachments", pr)
	if err != nil {
		return nil, err
	}
	cred := c.email + ":" + c.token
	req.Header.Set("Authorization", "Basic "+basicAuth(cred))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Atlassian-Token", "no-check")
	req.Header.Set("Content-Type", mp.FormDataContentType())
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("upload: %d %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var raw []struct {
		ID        string `json:"id"`
		Filename  string `json:"filename"`
		MimeType  string `json:"mimeType"`
		Size      int64  `json:"size"`
		Created   string `json:"created"`
		Author    User   `json:"author"`
		Content   string `json:"content"`
		Thumbnail string `json:"thumbnail"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	out := make([]Attachment, 0, len(raw))
	for _, r := range raw {
		out = append(out, Attachment{
			ID: r.ID, Filename: r.Filename, MIMEType: r.MimeType, Size: r.Size,
			Created: r.Created, Author: r.Author, Content: r.Content, Thumbnail: r.Thumbnail,
		})
	}
	return out, nil
}

func (c *Client) DeleteAttachment(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/rest/api/3/attachment/"+url.PathEscape(id), nil, nil)
}

// AttachmentContent streams the file bytes. Caller pipes to the HTTP response.
func (c *Client) AttachmentContent(ctx context.Context, id string) (io.ReadCloser, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.site+"/rest/api/3/attachment/content/"+url.PathEscape(id), nil)
	if err != nil {
		return nil, "", err
	}
	cred := c.email + ":" + c.token
	req.Header.Set("Authorization", "Basic "+basicAuth(cred))
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		resp.Body.Close()
		return nil, "", fmt.Errorf("attachment %s: %d %s", id, resp.StatusCode, string(b))
	}
	return resp.Body, resp.Header.Get("Content-Type"), nil
}
