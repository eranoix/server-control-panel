package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"server-control-panel/internal/aiprompts"
	"server-control-panel/internal/auth"
	"server-control-panel/internal/jira"
	"server-control-panel/internal/jiraai"
	ptysvc "server-control-panel/internal/pty"
	"server-control-panel/internal/scope"
)

func (r *Router) jiraRepoMapFor(owner string) map[string]string {
	if r.secrets == nil {
		return nil
	}
	u, err := scope.New(owner)
	if err != nil {
		return nil
	}
	raw, _ := scope.NewUserVault(r.secrets, u).Get("jira_project_repos")
	if raw == "" {
		return nil
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil
	}
	return m
}

func (r *Router) handleJiraAIAnalyze(w http.ResponseWriter, req *http.Request, key string) {
	if r.queue == nil {
		writeErr(w, 503, "queue unavailable")
		return
	}
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	owner := auth.UserFrom(req)
	if owner == "" {
		writeErr(w, 401, "unauthorized")
		return
	}
	if _, _, err := r.jiraClientFor(req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	args, _ := json.Marshal(jiraai.Args{IssueKey: key, Owner: owner})
	j, err := r.queue.Enqueue("jira_ai_analysis", args, owner, "user:ai-analyze:"+key)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	r.auditEvent(req, owner, "jira.ai.analyze", key+" → job "+j.ID)
	writeJSON(w, map[string]string{"job_id": j.ID, "issue_key": key})
}

func (r *Router) handleJiraAIWork(w http.ResponseWriter, req *http.Request, key string) {
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	owner := auth.UserFrom(req)
	if owner == "" {
		writeErr(w, 401, "unauthorized")
		return
	}
	cli, err := r.jiraClientForOwner(owner)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	issue, err := cli.GetIssue(req.Context(), key)
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	repoPath := ""
	if issue.Project != nil {
		if m := r.jiraRepoMapFor(owner); m != nil {
			repoPath = m[issue.Project.Key]
		}
	}

	sessionName := "panel-" + owner + "-jira-" + strings.ReplaceAll(strings.ToLower(key), "_", "-")

	go func() {
		from, to, err := tryJiraTransition(context.Background(), cli, key, "indeterminate")
		if err == nil && from != to {
			r.auditEvent(req, owner, "jira.ai.work.transition", key+" "+from+" → "+to)
		}
	}()

	if sessionExists(sessionName) {
		healCwd := r.agentCWD.Get(sessionName)
		if healCwd == "" {
			healCwd, _ = ensureTicketWorktree(req.Context(), repoPath, key)
		}
		if !claudeProjectHasMessages(r.forkConfigDir(), healCwd) {
			_ = ptysvc.SessionKill(sessionName)
			time.Sleep(200 * time.Millisecond)
			r.auditEvent(req, owner, "jira.ai.work.selfheal",
				key+" → "+sessionName+" (empty session, recreated with the prompt)")
		}
	}
	if sessionExists(sessionName) {
		if r.sessionOwn != nil {
			_ = r.sessionOwn.Claim(sessionName, owner)
		}
		r.startJiraWorkWatcher(owner, key, sessionName)
		if r.agentCWD != nil && r.agentCWD.Get(sessionName) == "" {
			cwd, _ := ensureTicketWorktree(req.Context(), repoPath, key)
			r.agentCWD.Put(sessionName, cwd)
		}
		r.auditEvent(req, owner, "jira.ai.work.reattach", key+" → "+sessionName)
		writeJSON(w, map[string]any{
			"session": sessionName, "repo": repoPath, "reattached": true,
		})
		return
	}

	agentCwd, usedWorktree := ensureTicketWorktree(req.Context(), repoPath, key)
	prompt := r.buildWorkPromptFromDetail(issue, agentCwd)
	created, err := ptysvc.SpawnJiraWorkSession(sessionName, agentCwd, prompt, r.forkConfigDir(), r.interactiveModel(""))
	if err != nil {
		writeErr(w, 500, "spawn session: "+err.Error())
		return
	}
	if r.sessionOwn != nil {
		_ = r.sessionOwn.Claim(created, owner)
	}
	r.agentCWD.Put(created, agentCwd)
	r.startJiraWorkWatcher(owner, key, created)
	r.auditEvent(req, owner, "jira.ai.work.start", key+" → "+created+" cwd="+agentCwd)
	writeJSON(w, map[string]any{
		"session": created, "repo": agentCwd, "worktree": usedWorktree, "reattached": false,
	})
}

func (r *Router) handleAIPrompts(w http.ResponseWriter, req *http.Request) {
	owner, ok := r.mustPrimary(w, req)
	if !ok {
		return
	}
	if r.aiPrompts == nil {
		writeErr(w, 503, "prompt registry unavailable")
		return
	}
	switch req.Method {
	case http.MethodGet:
		writeJSON(w, map[string]any{"prompts": r.aiPrompts.List()})
	case http.MethodPut:
		var body struct {
			ID    string `json:"id"`
			Value string `json:"value"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			writeErr(w, 400, "bad json")
			return
		}
		if strings.TrimSpace(body.ID) == "" {
			writeErr(w, 400, "id required")
			return
		}
		if fails := r.aiPrompts.Set(aiprompts.ID(body.ID), body.Value); len(fails) > 0 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnprocessableEntity)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error":   "prompt rejected by the compliance guard",
				"reasons": fails,
			})
			return
		}
		r.auditEvent(req, owner, "ai.prompts.update", body.ID)
		writeJSON(w, map[string]any{"status": "ok", "prompts": r.aiPrompts.List()})
	default:
		writeErr(w, 405, "method not allowed")
	}
}

func tryJiraTransition(ctx context.Context, cli *jira.Client, key, targetCat string) (oldStatus, newStatus string, err error) {
	issue, err := cli.GetIssue(ctx, key)
	if err != nil {
		return "", "", err
	}
	oldStatus = issue.Status.Name
	if issue.Status.StatusCategory.Key == targetCat {
		return oldStatus, oldStatus, nil
	}
	trans, err := cli.Transitions(ctx, key)
	if err != nil {
		return oldStatus, "", err
	}
	for _, t := range trans {
		if t.ToCat == targetCat {
			if err := cli.Transition(ctx, key, t.ID); err != nil {
				return oldStatus, "", err
			}
			return oldStatus, t.ToName, nil
		}
	}
	return oldStatus, "", fmt.Errorf("no transition for category %q", targetCat)
}

func (r *Router) buildWorkPromptFromDetail(d *jira.IssueDetail, repoPath string) string {
	var b strings.Builder
	b.WriteString("I am working on Jira ticket ")
	b.WriteString(d.Key)
	if d.Project != nil {
		b.WriteString(" of project ")
		b.WriteString(d.Project.Key)
	}
	b.WriteString(".\n\n")
	if repoPath != "" {
		b.WriteString("You are already in the project directory (")
		b.WriteString(repoPath)
		b.WriteString("). Use your tools freely.\n\n")
	}
	b.WriteString("**Title:** ")
	b.WriteString(d.Summary)
	b.WriteString("\n\n")
	if d.IssueType != nil {
		b.WriteString("**Type:** ")
		b.WriteString(d.IssueType.Name)
		b.WriteString("\n")
	}
	if d.Priority != nil {
		b.WriteString("**Priority:** ")
		b.WriteString(d.Priority.Name)
		b.WriteString("\n")
	}
	if len(d.Labels) > 0 {
		b.WriteString("**Labels:** ")
		b.WriteString(strings.Join(d.Labels, ", "))
		b.WriteString("\n")
	}
	b.WriteString("\n**Full ticket description** ")
	b.WriteString("(includes any previous AI analysis):\n\n")
	if strings.TrimSpace(d.Description) == "" {
		b.WriteString("_(no description — ask for whatever you need)_\n")
	} else {
		b.WriteString(d.Description)
		b.WriteString("\n")
	}
	b.WriteString("\n---\n\n")
	b.WriteString(strings.TrimSpace(r.aiPrompts.WorkTrailer()))
	b.WriteString("\n")
	return b.String()
}

func sessionExists(name string) bool {
	alive, _ := ptysvc.SessionHas(name)
	return alive
}

var _ = fmt.Sprintf
