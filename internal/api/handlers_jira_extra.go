// handlers_jira_extra.go — second-wave Jira HTTP surface (J2): inline edits,
// watchers, links, attachments (upload + stream), worklog, changelog,
// picker, priorities, and Confluence v2 spaces/pages.
//
// All routes wired in NewRouter alongside the J1 set. Authz model:
// per-user vault, scope.New + secrets.UserVault.
package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/httpmw"
	"server-control-panel/internal/jira"
)

// maxJiraAttachmentBytes is Jira's attachment ceiling — 32 MiB, the same value
// the front end checks BEFORE sending ("Hard cap aligns with the server's
// multipart limit (32MiB)", in uploadJiraAttachment) and that
// ParseMultipartForm uses below. httpmw.MaxBody applies 25 MiB by default on
// every route; without the RegisterLargeBody in init() below, any attachment between
// 25 and 32 MiB passed the client-side check and still died on the
// server — the panel promised 32 MiB and delivered 25.
const maxJiraAttachmentBytes = 32 << 20

func init() {
	httpmw.RegisterLargeBody(isJiraAttachmentUpload, maxJiraAttachmentBytes)
}

// isJiraAttachmentUpload matches exactly POST /api/jira/issue/{key}/attachments
// — the only route in the Jira family that receives a file (the rest are small
// JSON).
func isJiraAttachmentUpload(r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/jira/issue/")
	if rest == r.URL.Path {
		return false
	}
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	return len(parts) == 2 && parts[1] == "attachments"
}

// PATCH /api/jira/issue/{key}  body: UpdateIssueRequest
func (r *Router) handleJiraIssueUpdate(w http.ResponseWriter, req *http.Request, key string) {
	cli, _, err := r.jiraClientFor(req)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	var in jira.UpdateIssueRequest
	if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	if err := cli.UpdateIssue(req.Context(), key, in); err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	r.auditEvent(req, auth.UserFrom(req), "jira.issue.update", key)
	writeJSON(w, map[string]string{"status": "ok"})
}

// Watchers: GET, POST {account_id?}, DELETE ?account_id=
func (r *Router) handleJiraWatchers(w http.ResponseWriter, req *http.Request, key string) {
	cli, _, err := r.jiraClientFor(req)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	switch req.Method {
	case http.MethodGet:
		list, err := cli.Watchers(req.Context(), key)
		if err != nil {
			writeErr(w, 502, err.Error())
			return
		}
		writeJSON(w, list)
	case http.MethodPost:
		var body struct {
			AccountID string `json:"account_id"`
		}
		_ = json.NewDecoder(req.Body).Decode(&body)
		if err := cli.AddWatcher(req.Context(), key, body.AccountID); err != nil {
			writeErr(w, 502, err.Error())
			return
		}
		r.auditEvent(req, auth.UserFrom(req), "jira.issue.watch", key)
		writeJSON(w, map[string]string{"status": "ok"})
	case http.MethodDelete:
		acc := req.URL.Query().Get("account_id")
		if err := cli.RemoveWatcher(req.Context(), key, acc); err != nil {
			writeErr(w, 502, err.Error())
			return
		}
		r.auditEvent(req, auth.UserFrom(req), "jira.issue.unwatch", key)
		writeJSON(w, map[string]string{"status": "ok"})
	default:
		writeErr(w, 405, "method not allowed")
	}
}

// Issue links — GET /api/jira/linktypes, POST /api/jira/issuelink {type,inward_key,outward_key}, DELETE /api/jira/issuelink/{id}
func (r *Router) handleJiraLinkTypes(w http.ResponseWriter, req *http.Request) {
	cli, _, err := r.jiraClientFor(req)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	list, err := cli.IssueLinkTypes(req.Context())
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	writeJSON(w, map[string]any{"link_types": list})
}

func (r *Router) handleJiraIssueLinkCreate(w http.ResponseWriter, req *http.Request) {
	cli, _, err := r.jiraClientFor(req)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	var body struct {
		Type       string `json:"type"`
		InwardKey  string `json:"inward_key"`
		OutwardKey string `json:"outward_key"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	if err := cli.CreateIssueLink(req.Context(), body.Type, body.InwardKey, body.OutwardKey); err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	r.auditEvent(req, auth.UserFrom(req), "jira.link.create", body.InwardKey+" "+body.Type+" "+body.OutwardKey)
	writeJSON(w, map[string]string{"status": "ok"})
}

func (r *Router) handleJiraIssueLinkDelete(w http.ResponseWriter, req *http.Request) {
	cli, _, err := r.jiraClientFor(req)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	id := strings.TrimPrefix(req.URL.Path, "/api/jira/issuelink/")
	if id == "" {
		writeErr(w, 400, "id required")
		return
	}
	if err := cli.DeleteIssueLink(req.Context(), id); err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	r.auditEvent(req, auth.UserFrom(req), "jira.link.delete", id)
	writeJSON(w, map[string]string{"status": "ok"})
}

// Attachments — POST upload (multipart), DELETE /api/jira/attachment/{id},
// GET /api/jira/attachment/{id}/content streams the bytes.
func (r *Router) handleJiraAttachmentUpload(w http.ResponseWriter, req *http.Request, key string) {
	cli, _, err := r.jiraClientFor(req)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if err := req.ParseMultipartForm(maxJiraAttachmentBytes); err != nil {
		writeErr(w, 400, "multipart: "+err.Error())
		return
	}
	file, header, err := req.FormFile("file")
	if err != nil {
		writeErr(w, 400, "file field required")
		return
	}
	defer file.Close()
	out, err := cli.UploadAttachment(req.Context(), key, header.Filename, file)
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	r.auditEvent(req, auth.UserFrom(req), "jira.attach.upload", key+":"+header.Filename)
	writeJSON(w, map[string]any{"attachments": out})
}

// handleJiraAvatar proxies a Jira user's avatar through OUR origin, so that
// the browser never touches third-party CDNs (gravatar/wp.com) and never fires
// "Tracking Prevention blocked access to storage". The URL arrives in ?u= and is
// validated against an allowlist in the client (SSRF defence). No avatar / blocked
// host → 204 (the browser logs no error; the front end falls back to initials).
func (r *Router) handleJiraAvatar(w http.ResponseWriter, req *http.Request) {
	cli, _, err := r.jiraClientFor(req)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	raw := req.URL.Query().Get("u")
	if raw == "" {
		writeErr(w, 400, "u required")
		return
	}
	body, ctype, err := cli.AvatarContent(req.Context(), raw)
	if err != nil {
		w.Header().Set("Cache-Control", "private, max-age=300")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	defer body.Close()
	// ctype was already validated as a safe raster image in AvatarContent.
	if ctype != "" {
		w.Header().Set("Content-Type", ctype)
	}
	// Defence in depth: nosniff stops the browser from re-sniffing to HTML;
	// CSP sandbox + Content-Disposition inline neutralize any execution if
	// a type slips through. This matters for direct navigation to the URL (an img tag is
	// unaffected, but opening it in a new tab renders the response as a document).
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", `inline; filename="avatar"`)
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	_, _ = io.Copy(w, io.LimitReader(body, 2<<20)) // cap 2 MiB — an avatar is small
}

func (r *Router) handleJiraAttachment(w http.ResponseWriter, req *http.Request) {
	cli, _, err := r.jiraClientFor(req)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	rest := strings.TrimPrefix(req.URL.Path, "/api/jira/attachment/")
	parts := strings.SplitN(rest, "/", 2)
	id := parts[0]
	if id == "" {
		writeErr(w, 400, "id required")
		return
	}
	action := ""
	if len(parts) == 2 {
		action = parts[1]
	}
	switch action {
	case "":
		if req.Method != http.MethodDelete {
			writeErr(w, 405, "method not allowed")
			return
		}
		if err := cli.DeleteAttachment(req.Context(), id); err != nil {
			writeErr(w, 502, err.Error())
			return
		}
		r.auditEvent(req, auth.UserFrom(req), "jira.attach.delete", id)
		writeJSON(w, map[string]string{"status": "ok"})
	case "content":
		body, ctype, err := cli.AttachmentContent(req.Context(), id)
		if err != nil {
			writeErr(w, 502, err.Error())
			return
		}
		defer body.Close()
		if ctype != "" {
			w.Header().Set("Content-Type", ctype)
		}
		_, _ = io.Copy(w, body)
	default:
		writeErr(w, 404, "unknown action")
	}
}

// Worklog
func (r *Router) handleJiraWorklog(w http.ResponseWriter, req *http.Request, key string) {
	cli, _, err := r.jiraClientFor(req)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	switch req.Method {
	case http.MethodGet:
		list, err := cli.Worklogs(req.Context(), key)
		if err != nil {
			writeErr(w, 502, err.Error())
			return
		}
		writeJSON(w, map[string]any{"worklogs": list})
	case http.MethodPost:
		var in jira.AddWorklogRequest
		if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
			writeErr(w, 400, "bad json")
			return
		}
		if strings.TrimSpace(in.TimeSpent) == "" {
			writeErr(w, 400, "time_spent required (e.g. '1h 30m')")
			return
		}
		if err := cli.AddWorklog(req.Context(), key, in); err != nil {
			writeErr(w, 502, err.Error())
			return
		}
		r.auditEvent(req, auth.UserFrom(req), "jira.worklog.add", key+" "+in.TimeSpent)
		writeJSON(w, map[string]string{"status": "ok"})
	default:
		writeErr(w, 405, "method not allowed")
	}
}

// Changelog
func (r *Router) handleJiraChangelog(w http.ResponseWriter, req *http.Request, key string) {
	cli, _, err := r.jiraClientFor(req)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	list, err := cli.Changelog(req.Context(), key)
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	writeJSON(w, map[string]any{"changelog": list})
}

// Issue picker
func (r *Router) handleJiraPicker(w http.ResponseWriter, req *http.Request) {
	cli, _, err := r.jiraClientFor(req)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	q := req.URL.Query()
	out, err := cli.PickIssues(req.Context(), q.Get("query"), q.Get("currentJQL"))
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	writeJSON(w, map[string]any{"issues": out})
}

// Priorities
func (r *Router) handleJiraPriorities(w http.ResponseWriter, req *http.Request) {
	cli, _, err := r.jiraClientFor(req)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	out, err := cli.Priorities(req.Context())
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	writeJSON(w, map[string]any{"priorities": out})
}

// Confluence
func (r *Router) handleJiraConfluenceSpaces(w http.ResponseWriter, req *http.Request) {
	cli, _, err := r.jiraClientFor(req)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	list, err := cli.ConfluenceSpaces(req.Context())
	if err != nil {
		logJiraErr("confluence.spaces", "", err)
		writeErr(w, 502, err.Error())
		return
	}
	writeJSON(w, map[string]any{"spaces": list})
}

func (r *Router) handleJiraConfluencePages(w http.ResponseWriter, req *http.Request) {
	cli, _, err := r.jiraClientFor(req)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	spaceID := req.URL.Query().Get("space_id")
	if spaceID == "" {
		writeErr(w, 400, "space_id required")
		return
	}
	cursor := req.URL.Query().Get("cursor")
	pages, next, err := cli.ConfluencePages(req.Context(), spaceID, 50, cursor)
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	writeJSON(w, map[string]any{"pages": pages, "next_cursor": next})
}

// DELETE /api/jira/issue/{key}?with_subtasks=1
func (r *Router) handleJiraIssueDelete(w http.ResponseWriter, req *http.Request, key string) {
	cli, _, err := r.jiraClientFor(req)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	withSub := req.URL.Query().Get("with_subtasks") == "1"
	if err := cli.DeleteIssue(req.Context(), key, withSub); err != nil {
		logJiraErr("issue.delete", key, err)
		writeErr(w, 502, err.Error())
		return
	}
	r.auditEvent(req, auth.UserFrom(req), "jira.issue.delete", key)
	writeJSON(w, map[string]string{"status": "deleted"})
}

// POST /api/jira/issue/{key}/clone
func (r *Router) handleJiraIssueClone(w http.ResponseWriter, req *http.Request, key string) {
	cli, _, err := r.jiraClientFor(req)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	got, err := cli.CloneIssue(req.Context(), key)
	if err != nil {
		logJiraErr("issue.clone", key, err)
		writeErr(w, 502, err.Error())
		return
	}
	r.auditEvent(req, auth.UserFrom(req), "jira.issue.clone", key+" -> "+got.Key)
	writeJSON(w, got)
}

// PUT/DELETE /api/jira/issue/{key}/comment/{id}
func (r *Router) handleJiraCommentByID(w http.ResponseWriter, req *http.Request, key, commentID string) {
	cli, _, err := r.jiraClientFor(req)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	switch req.Method {
	case http.MethodPut, http.MethodPatch:
		var body struct {
			Body string `json:"body"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			writeErr(w, 400, "bad json")
			return
		}
		if strings.TrimSpace(body.Body) == "" {
			writeErr(w, 400, "body required")
			return
		}
		c, err := cli.UpdateComment(req.Context(), key, commentID, body.Body)
		if err != nil {
			writeErr(w, 502, err.Error())
			return
		}
		r.auditEvent(req, auth.UserFrom(req), "jira.comment.update", key+":"+commentID)
		writeJSON(w, c)
	case http.MethodDelete:
		if err := cli.DeleteComment(req.Context(), key, commentID); err != nil {
			writeErr(w, 502, err.Error())
			return
		}
		r.auditEvent(req, auth.UserFrom(req), "jira.comment.delete", key+":"+commentID)
		writeJSON(w, map[string]string{"status": "deleted"})
	default:
		writeErr(w, 405, "method not allowed")
	}
}

// /api/jira/issue/{key}/votes — GET/POST/DELETE
func (r *Router) handleJiraVotes(w http.ResponseWriter, req *http.Request, key string) {
	cli, _, err := r.jiraClientFor(req)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	switch req.Method {
	case http.MethodGet:
		v, err := cli.GetVotes(req.Context(), key)
		if err != nil {
			writeErr(w, 502, err.Error())
			return
		}
		writeJSON(w, v)
	case http.MethodPost:
		if err := cli.AddVote(req.Context(), key); err != nil {
			writeErr(w, 502, err.Error())
			return
		}
		r.auditEvent(req, auth.UserFrom(req), "jira.vote.add", key)
		writeJSON(w, map[string]string{"status": "voted"})
	case http.MethodDelete:
		if err := cli.RemoveVote(req.Context(), key); err != nil {
			writeErr(w, 502, err.Error())
			return
		}
		r.auditEvent(req, auth.UserFrom(req), "jira.vote.remove", key)
		writeJSON(w, map[string]string{"status": "unvoted"})
	default:
		writeErr(w, 405, "method not allowed")
	}
}

// GET /api/jira/project/{key}/{versions|components|epics}
func (r *Router) handleJiraProjectMeta(w http.ResponseWriter, req *http.Request) {
	cli, _, err := r.jiraClientFor(req)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	rest := strings.TrimPrefix(req.URL.Path, "/api/jira/project/")
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) != 2 {
		writeErr(w, 400, "expected /api/jira/project/{key}/{kind}")
		return
	}
	key, kind := parts[0], parts[1]
	switch kind {
	case "versions":
		v, err := cli.Versions(req.Context(), key)
		if err != nil {
			writeErr(w, 502, err.Error())
			return
		}
		writeJSON(w, map[string]any{"versions": v})
	case "components":
		v, err := cli.Components(req.Context(), key)
		if err != nil {
			writeErr(w, 502, err.Error())
			return
		}
		writeJSON(w, map[string]any{"components": v})
	case "epics":
		v, err := cli.Epics(req.Context(), key)
		if err != nil {
			writeErr(w, 502, err.Error())
			return
		}
		writeJSON(w, map[string]any{"epics": v})
	default:
		writeErr(w, 404, "unknown kind: "+kind)
	}
}

func (r *Router) handleJiraConfluencePage(w http.ResponseWriter, req *http.Request) {
	cli, _, err := r.jiraClientFor(req)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	id := strings.TrimPrefix(req.URL.Path, "/api/jira/confluence/page/")
	if id == "" {
		writeErr(w, 400, "id required")
		return
	}
	page, err := cli.ConfluencePage(req.Context(), id)
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	writeJSON(w, page)
}
