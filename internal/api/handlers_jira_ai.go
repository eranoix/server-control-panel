// handlers_jira_ai.go — wires the "Iniciar AI" button to the queue.
//
// One endpoint:
//
//	POST /api/jira/issue/{key}/ai-analyze    → enqueues a jira_ai_analysis
//	                                            job; returns {job_id}
//
// The actual work happens in internal/jiraai/runner.go, executed by the
// F3 queue worker pool. The UI subscribes to /ws/queue/{job_id} for the
// live audit transcript.
//
// Per-owner mapping: project_key → repo path is stored in the user's
// vault under `jira_project_repos` as JSON. handlers_jira.go's existing
// vault helpers do the read/write.
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

// jiraRepoMapFor returns the per-user project_key → repo_path map from
// the vault. nil/empty map = caller hasn't configured anything; the AI
// runner will still execute but without a cwd (generic advice).
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

// handleJiraAIAnalyze enqueues an AI audit of the issue. Routed via
// handleJiraIssue dispatch (action="ai-analyze").
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
	// Validate caller can talk to Jira before enqueuing — avoids stale
	// jobs piling up when credentials are missing/expired.
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

// handleJiraAIWork creates a dtach session "vpsm-<user>-jira-<KEY>" with
// claude already running, cwd=mapped repo, and the ticket context pasted
// as the first prompt. Returns the session name so the UI can open the
// terminal page directly on it.
//
// Synchronous (no queue): dtach create + dtach -p paste is fast, ~3s.
// Idempotent: if the session already exists, returns its name without
// re-creating (so the user reattaches to their existing work).
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
	// Resolve repo path from per-user map
	repoPath := ""
	if issue.Project != nil {
		if m := r.jiraRepoMapFor(owner); m != nil {
			repoPath = m[issue.Project.Key]
		}
	}

	sessionName := "vpsm-" + owner + "-jira-" + strings.ReplaceAll(strings.ToLower(key), "_", "-")

	// Auto-transition "→ Em andamento" — best-effort, does not block the spawn.
	// Done in the background so it does not delay the response (the Jira call takes ~600ms).
	// Silently skipped if the issue is already in that category or if there is no
	// mapped transition — that is no reason to fail.
	go func() {
		from, to, err := tryJiraTransition(context.Background(), cli, key, "indeterminate")
		if err == nil && from != to {
			r.auditEvent(req, owner, "jira.ai.work.transition", key+" "+from+" → "+to)
		}
	}()

	// Idempotent: if the session is already alive, claim ownership
	// (in case caller is primary and old owner moved on) and return.
	//
	// SELF-HEALING: a live session that never received a message is a leftover of the
	// paste bug (session created, claude running, empty input). Reattaching to it would
	// just drop the user back into the same stalled terminal, so it is killed here and
	// the normal flow just below recreates it with the prompt in argv. Since the gate is
	// "zero messages", recreating never discards work.
	if sessionExists(sessionName) {
		healCwd := r.agentCWD.Get(sessionName)
		if healCwd == "" {
			healCwd, _ = ensureTicketWorktree(req.Context(), repoPath, key)
		}
		if !claudeProjectHasMessages(r.forkConfigDir(), healCwd) {
			_ = ptysvc.SessionKill(sessionName)
			time.Sleep(200 * time.Millisecond) // deixa o socket sumir antes de recriar
			r.auditEvent(req, owner, "jira.ai.work.selfheal",
				key+" → "+sessionName+" (empty session, recreated with the prompt)")
		}
	}
	if sessionExists(sessionName) {
		if r.sessionOwn != nil {
			_ = r.sessionOwn.Claim(sessionName, owner)
		}
		// Reattach: garantir watcher rodando (cobre caso de o vps-manager
		// ter reiniciado entre clicks).
		r.startJiraWorkWatcher(owner, key, sessionName)
		// Best-effort: re-establishes the name→cwd map if it was
		// lost (e.g. the sidecar was deleted). The worktree is deterministic and idempotent.
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

	// #2: dedicated git worktree per agent (guarded/idempotent; falls back to
	// the shared repo when repoPath isn't a git repo).
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
	// Registers name→cwd so the hook + aggregator can resolve the
	// session from Claude Code's project-dir.
	r.agentCWD.Put(created, agentCwd)
	// Starts the watcher that will monitor the dtach session and detect when the
	// user signals "it's working" — at which point it moves to Done.
	r.startJiraWorkWatcher(owner, key, created)
	r.auditEvent(req, owner, "jira.ai.work.start", key+" → "+created+" cwd="+agentCwd)
	writeJSON(w, map[string]any{
		"session": created, "repo": agentCwd, "worktree": usedWorktree, "reattached": false,
	})
}

// handleAIPrompts — manages the runtime-editable AI prompts.
//
//	GET  /api/ai/prompts → lists each prompt (default + current value + contract)
//	PUT  /api/ai/prompts → saves an override {id, value} after the guard
//
// Admin-only (mustPrimary): editing these prompts changes how EVERY user's AS
// analysis behaves. The PUT runs the conformance guard
// (aiprompts.Validate via Set) and returns 422 + the list of failures if the template
// removes the {{OUTPUT_CONTRACT}} marker — without which the verification loop
// would never converge (runner.go's parsers read the managed contract).
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
			w.WriteHeader(http.StatusUnprocessableEntity) // 422
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

// tryJiraTransition applies the first transition whose target category
// matches targetCat ("new"/"indeterminate"/"done"). Idempotent: if the
// issue is already in that category, it returns (oldName, oldName, nil) without
// calling the API.
//
// Best-effort by design — if the project's workflow has no transition
// for the requested category (e.g. a kanban with no "Em andamento"), it returns an
// error but the caller may ignore it.
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

// buildWorkPromptFromDetail: short, action-oriented prompt fed to the
// Claude session right after spawn. Carries the full ticket context
// (title + description — which already has the AI analysis block
// appended from earlier "Iniciar AI" runs) so the assistant has zero
// guesswork about what's being asked.
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
		b.WriteString("**Tipo:** ")
		b.WriteString(d.IssueType.Name)
		b.WriteString("\n")
	}
	if d.Priority != nil {
		b.WriteString("**Prioridade:** ")
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
	// The trailer (final instruction) comes from the runtime-editable registry.
	b.WriteString("\n---\n\n")
	b.WriteString(strings.TrimSpace(r.aiPrompts.WorkTrailer()))
	b.WriteString("\n")
	return b.String()
}

// sessionExists returns true when a live session named exactly `name`
// exists. Backend-aware via ptysvc.SessionHas (dtach: live socket). Exact
// match by name — no prefix aliasing like the old has-session behaviour.
func sessionExists(name string) bool {
	// The engine is dtach: existence is the live socket (session.go).
	alive, _ := ptysvc.SessionHas(name)
	return alive
}

// fmt and other imports used above — keeps go vet happy if pruned during refactor.
var _ = fmt.Sprintf
