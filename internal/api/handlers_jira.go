// handlers_jira.go — HTTP layer for the Jira Cloud kanban (J1).
//
// Config (site, email, token, project_key, board_jql) is per-user, stored
// in the secrets vault under keys jira_site / jira_email / jira_token /
// jira_project / jira_board_jql. The token never leaves the server; the
// /config GET returns a `has_token` boolean instead.
//
// Routes wired in NewRouter:
//
//	GET    /api/jira/config                         current config (no token)
//	POST   /api/jira/config                         save config (token only persisted when set)
//	DELETE /api/jira/config                         wipe config (deletes vault keys)
//	GET    /api/jira/health                         myself() probe — validates creds
//	GET    /api/jira/projects                       list projects
//	GET    /api/jira/issuetypes?project=KEY         issue types for a project
//	GET    /api/jira/board?jql=...&start=&max=      kanban issues
//	GET    /api/jira/issue/{key}                    detail
//	POST   /api/jira/issue                          create
//	GET    /api/jira/issue/{key}/transitions        available transitions
//	POST   /api/jira/issue/{key}/transition         {transition_id}
//	GET    /api/jira/issue/{key}/comments           list comments
//	POST   /api/jira/issue/{key}/comment            {body}
//	GET    /api/jira/users?project=KEY&q=foo        assignable users picker
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/jira"
	"server-control-panel/internal/scope"
)

// logJiraErr is a small helper so every handler that calls into the Jira
// client surfaces upstream failures into journalctl with the same shape.
func logJiraErr(where, ctx string, err error) {
	log.Printf("[jira] %s failed (%s): %v", where, ctx, err)
}

// jiraClientFor returns a configured client for the caller, or
// (nil, ErrNotConfigured) if no token has been saved yet.
func (r *Router) jiraClientFor(req *http.Request) (*jira.Client, *jira.Config, error) {
	user := auth.UserFrom(req)
	if user == "" {
		return nil, nil, errors.New("unauthorized")
	}
	cli, err := r.jiraClientForOwner(user)
	if err != nil {
		return nil, nil, err
	}
	// Return a Config snapshot too — handler /api/jira/health uses it.
	u, _ := scope.New(user)
	uv := scope.NewUserVault(r.secrets, u)
	cfg := &jira.Config{
		Site:       valOf(uv, "jira_site"),
		Email:      valOf(uv, "jira_email"),
		ProjectKey: valOf(uv, "jira_project"),
		BoardJQL:   valOf(uv, "jira_board_jql"),
		HasToken:   valOf(uv, "jira_token") != "",
	}
	return cli, cfg, nil
}

func valOf(uv *scope.UserVault, k string) string { v, _ := uv.Get(k); return v }

// userVault opens a user's PERSONAL vault for writing.
//
// Extracted because three paths write a Jira credential into the same box (the
// POST branch of handleJiraConfig, and the JiraConnect/JiraSetProject closures
// the mobile BFF uses) and each one repeated scope.New + the vault check. One
// more copy of that sequence is one more chance for someone to forget the
// `r.secrets == nil` guard and write into a vault that does not exist.
func (r *Router) userVault(user string) (*scope.UserVault, error) {
	u, err := scope.New(user)
	if err != nil {
		return nil, err
	}
	if r.secrets == nil {
		return nil, errors.New("vault unavailable")
	}
	return scope.NewUserVault(r.secrets, u), nil
}

// jiraClientForOwner is the request-less variant used by the queue
// worker (which has no http.Request — only the job owner). Same vault
// lookup as jiraClientFor but factored so background jobs can run with
// the operator's credentials without re-authenticating.
func (r *Router) jiraClientForOwner(user string) (*jira.Client, error) {
	if user == "" {
		return nil, errors.New("unauthorized")
	}
	u, err := scope.New(user)
	if err != nil {
		return nil, err
	}
	if r.secrets == nil {
		return nil, errors.New("vault unavailable")
	}
	uv := scope.NewUserVault(r.secrets, u)
	site, _ := uv.Get("jira_site")
	email, _ := uv.Get("jira_email")
	token, _ := uv.Get("jira_token")
	if site == "" || email == "" || token == "" {
		return nil, jira.ErrNotConfigured
	}
	return jira.New(site, email, token)
}

func (r *Router) handleJiraConfig(w http.ResponseWriter, req *http.Request) {
	user := auth.UserFrom(req)
	if user == "" {
		writeErr(w, 401, "unauthorized")
		return
	}
	u, err := scope.New(user)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	uv := scope.NewUserVault(r.secrets, u)

	switch req.Method {
	case http.MethodGet:
		// no-store so the SW / browser never serves a stale board layout
		// (no-cache/must-revalidate/Pragma are redundant or deprecated legacy).
		w.Header().Set("Cache-Control", "no-store")
		site, _ := uv.Get("jira_site")
		email, _ := uv.Get("jira_email")
		token, _ := uv.Get("jira_token")
		project, _ := uv.Get("jira_project")
		jql, _ := uv.Get("jira_board_jql")
		cols, _ := uv.Get("jira_board_columns")
		writeJSON(w, jira.Config{
			Site:         site,
			Email:        email,
			ProjectKey:   project,
			BoardJQL:     jql,
			BoardColumns: cols,
			HasToken:     token != "",
		})
	case http.MethodPost:
		var body struct {
			Site         string `json:"site"`
			Email        string `json:"email"`
			Token        string `json:"token"`
			ProjectKey   string `json:"project_key"`
			BoardJQL     string `json:"board_jql"`
			BoardColumns string `json:"board_columns"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			writeErr(w, 400, "bad json")
			return
		}
		// Setters that empty-string skip — UI can save only the fields it
		// changed without wiping the token. board_columns explicit set to
		// "[]" wipes; "null"/empty skips.
		//
		// Errors are NO LONGER swallowed: if vault.Set fails (disk full,
		// fsync error) the UI used to say "saved" while nothing changed.
		// Now we abort on first failure and report 500 with the key that
		// failed so the operator can recover.
		var lastErr error
		setIf := func(key, val string) {
			if lastErr != nil || strings.TrimSpace(val) == "" {
				return
			}
			if err := uv.Set(key, strings.TrimSpace(val)); err != nil {
				lastErr = fmt.Errorf("vault.set %s: %w", key, err)
			}
		}
		setIf("jira_site", body.Site)
		setIf("jira_email", body.Email)
		setIf("jira_token", body.Token) // only overwrite when user typed a new one
		setIf("jira_project", body.ProjectKey)
		setIf("jira_board_jql", body.BoardJQL)
		setIf("jira_board_columns", body.BoardColumns)
		if lastErr != nil {
			r.auditEvent(req, user, "jira.config.save.failed", lastErr.Error())
			writeErr(w, 500, lastErr.Error())
			return
		}
		r.auditEvent(req, user, "jira.config.save", "site="+body.Site)
		writeJSON(w, map[string]string{"status": "saved"})
	case http.MethodDelete:
		for _, k := range []string{"jira_site", "jira_email", "jira_token", "jira_project", "jira_board_jql", "jira_board_columns"} {
			_ = uv.Set(k, "")
		}
		r.auditEvent(req, user, "jira.config.delete", "")
		writeJSON(w, map[string]string{"status": "deleted"})
	default:
		writeErr(w, 405, "method not allowed")
	}
}

func (r *Router) handleJiraHealth(w http.ResponseWriter, req *http.Request) {
	cli, cfg, err := r.jiraClientFor(req)
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error(), "config": cfg})
		return
	}
	me, err := cli.Myself(req.Context())
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error(), "config": cfg})
		return
	}
	writeJSON(w, map[string]any{"ok": true, "me": me, "config": cfg, "site": cli.Site()})
}

func (r *Router) handleJiraProjects(w http.ResponseWriter, req *http.Request) {
	cli, _, err := r.jiraClientFor(req)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	list, err := cli.Projects(req.Context())
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	writeJSON(w, map[string]any{"projects": list})
}

func (r *Router) handleJiraIssueTypes(w http.ResponseWriter, req *http.Request) {
	cli, _, err := r.jiraClientFor(req)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	project := req.URL.Query().Get("project")
	if project == "" {
		writeErr(w, 400, "project required")
		return
	}
	list, err := cli.IssueTypesForProject(req.Context(), project)
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	writeJSON(w, map[string]any{"issue_types": list})
}

func (r *Router) handleJiraBoard(w http.ResponseWriter, req *http.Request) {
	cli, cfg, err := r.jiraClientFor(req)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	q := req.URL.Query()
	jql := q.Get("jql")
	if jql == "" {
		jql = cfg.BoardJQL
	}
	if jql == "" && cfg.ProjectKey != "" {
		jql = "project = " + cfg.ProjectKey + " AND assignee = currentUser() ORDER BY status, updated DESC"
	}
	start, _ := strconv.Atoi(q.Get("start"))
	max, _ := strconv.Atoi(q.Get("max"))
	issues, total, err := cli.Search(req.Context(), jql, start, max)
	if err != nil {
		// log upstream errors so we can diagnose (vault token expired,
		// JQL rejected, API version mismatch, etc.). 502 to client.
		logJiraErr("board", "jql="+jql, err)
		writeErr(w, 502, err.Error())
		return
	}
	writeJSON(w, map[string]any{"issues": issues, "total": total, "jql": jql})
}

func (r *Router) handleJiraUsers(w http.ResponseWriter, req *http.Request) {
	cli, _, err := r.jiraClientFor(req)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	project := req.URL.Query().Get("project")
	query := req.URL.Query().Get("q")
	if project == "" {
		writeErr(w, 400, "project required")
		return
	}
	list, err := cli.AssignableUsers(req.Context(), project, query)
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	writeJSON(w, map[string]any{"users": list})
}

// handleJiraIssue dispatches /api/jira/issue/{key}[/transitions|/transition|/comments|/comment]
func (r *Router) handleJiraIssue(w http.ResponseWriter, req *http.Request) {
	cli, _, err := r.jiraClientFor(req)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	rest := strings.TrimPrefix(req.URL.Path, "/api/jira/issue")
	rest = strings.TrimPrefix(rest, "/")

	if rest == "" {
		// POST /api/jira/issue — create
		if req.Method != http.MethodPost {
			writeErr(w, 405, "method not allowed")
			return
		}
		var in jira.CreateIssueRequest
		if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
			writeErr(w, 400, "bad json")
			return
		}
		got, err := cli.CreateIssue(req.Context(), in)
		if err != nil {
			writeErr(w, 502, err.Error())
			return
		}
		r.auditEvent(req, auth.UserFrom(req), "jira.issue.create", got.Key)
		writeJSON(w, got)
		return
	}

	parts := strings.SplitN(rest, "/", 2)
	key := parts[0]
	action := ""
	if len(parts) == 2 {
		action = parts[1]
	}

	switch action {
	case "":
		switch req.Method {
		case http.MethodGet:
			d, err := cli.GetIssue(req.Context(), key)
			if err != nil {
				writeErr(w, 502, err.Error())
				return
			}
			writeJSON(w, d)
		case http.MethodPatch, http.MethodPut:
			r.handleJiraIssueUpdate(w, req, key)
		case http.MethodDelete:
			r.handleJiraIssueDelete(w, req, key)
		default:
			writeErr(w, 405, "method not allowed")
		}
	case "transitions":
		ts, err := cli.Transitions(req.Context(), key)
		if err != nil {
			writeErr(w, 502, err.Error())
			return
		}
		writeJSON(w, map[string]any{"transitions": ts})
	case "transition":
		if req.Method != http.MethodPost {
			writeErr(w, 405, "method not allowed")
			return
		}
		var body struct {
			TransitionID string `json:"transition_id"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			writeErr(w, 400, "bad json")
			return
		}
		if err := cli.Transition(req.Context(), key, body.TransitionID); err != nil {
			writeErr(w, 502, err.Error())
			return
		}
		r.auditEvent(req, auth.UserFrom(req), "jira.issue.transition", key+" -> "+body.TransitionID)
		writeJSON(w, map[string]string{"status": "ok"})
	case "comments":
		list, err := cli.Comments(req.Context(), key)
		if err != nil {
			writeErr(w, 502, err.Error())
			return
		}
		writeJSON(w, map[string]any{"comments": list})
	case "comment":
		if req.Method != http.MethodPost {
			writeErr(w, 405, "method not allowed")
			return
		}
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
		c, err := cli.AddComment(req.Context(), key, body.Body)
		if err != nil {
			writeErr(w, 502, err.Error())
			return
		}
		r.auditEvent(req, auth.UserFrom(req), "jira.issue.comment", key)
		writeJSON(w, c)
	case "watchers":
		r.handleJiraWatchers(w, req, key)
	case "worklog":
		r.handleJiraWorklog(w, req, key)
	case "changelog":
		r.handleJiraChangelog(w, req, key)
	case "attachments":
		r.handleJiraAttachmentUpload(w, req, key)
	case "clone":
		r.handleJiraIssueClone(w, req, key)
	case "votes":
		r.handleJiraVotes(w, req, key)
	case "ai-analyze":
		r.handleJiraAIAnalyze(w, req, key)
	case "work":
		r.handleJiraAIWork(w, req, key)
	default:
		// /{key}/comment/{id} → edit/delete one comment
		if strings.HasPrefix(action, "comment/") {
			r.handleJiraCommentByID(w, req, key, strings.TrimPrefix(action, "comment/"))
			return
		}
		writeErr(w, 404, "unknown action: "+action)
	}
}
