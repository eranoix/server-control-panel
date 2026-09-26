package git

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"server-control-panel/internal/httpx"
	"server-control-panel/internal/scope"
)

// ---- GitHub token (vault) ----

// credLineRe captures the token from a line in ~/.git-credentials format:
// https://[user:]TOKEN@github.com  (group 1 = TOKEN). It accepts it with or without a user.
var credLineRe = regexp.MustCompile(`^https?://(?:[^/@]*:)?([^@/]+)@github\.com`)

// bareTokenRe recognizes a bare PAT (classic ghp_/gho_/ghs_ or fine-grained
// github_pat_).
var bareTokenRe = regexp.MustCompile(`^(ghp_|gho_|ghs_|ghu_|github_pat_)[A-Za-z0-9_]+$`)

// parseGitHubToken extracts a usable token from several shapes: a
// git-credentials file (https://user:token@github.com lines), a bare PAT, or
// a single-line value with no spaces. It returns "" when it cannot.
func parseGitHubToken(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if m := credLineRe.FindStringSubmatch(line); m != nil {
			return strings.TrimSpace(m[1])
		}
	}
	if bareTokenRe.MatchString(raw) {
		return raw
	}
	// a single-line value that does NOT look like a URL/credential → assume it is the token
	if !strings.ContainsAny(raw, " \n\t") && !strings.Contains(raw, "://") && !strings.Contains(raw, "@") {
		return raw
	}
	return ""
}

// gitCredentialToken asks git ITSELF for the repo's https credential, through
// `git credential fill`. It is git's canonical mechanism: it honours the
// credential.helper configured IN THE REPO. In northwind-web the local config
// resets the helpers and uses the work store (the sam@northwind account);
// in the personal repos it uses the global helper (northwind-dev). Deterministic
// and per-repo. The VALUE (the token) is never logged. "" when git returns no password.
func gitCredentialToken(ctx context.Context, repoPath string) string {
	c, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(c, "git", "-C", repoPath, "credential", "fill")
	cmd.Stdin = strings.NewReader("protocol=https\nhost=github.com\n\n")
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "password=") {
			return strings.TrimSpace(strings.TrimPrefix(line, "password="))
		}
	}
	return ""
}

// githubToken resolves the GitHub token FOR THE REPO: first through git
// credential fill (per-repo, the right account — work for northwind, personal
// for northwind-dev); failing that, it falls back to the git_credentials vault entry (personal). The value is never logged.
func (s *svc) githubToken(repoPath string) (string, bool) {
	if t := gitCredentialToken(context.Background(), repoPath); t != "" {
		return t, true
	}
	if s.vault == nil || s.cfg == nil || s.cfg.Primary == "" {
		return "", false
	}
	u, err := scope.New(s.cfg.Primary)
	if err != nil {
		return "", false
	}
	raw, ok := scope.NewUserVault(s.vault, u).Get("git_credentials")
	if !ok {
		return "", false
	}
	tok := parseGitHubToken(raw)
	return tok, tok != ""
}

// ---- call to the GitHub API ----

func ghRequest(ctx context.Context, token, method, apiPath string, body []byte) (int, []byte, error) {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, "https://api.github.com"+apiPath, rdr)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "vps-manager-git")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{Timeout: 25 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	return resp.StatusCode, b, nil
}

// ghErrMsg extracts the "message" out of a GitHub error body.
func ghErrMsg(status int, body []byte) string {
	var e struct {
		Message string `json:"message"`
		Errors  []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	_ = json.Unmarshal(body, &e)
	msg := e.Message
	if len(e.Errors) > 0 && e.Errors[0].Message != "" {
		msg += ": " + e.Errors[0].Message
	}
	if msg == "" {
		msg = "GitHub HTTP " + http.StatusText(status)
	}
	return msg
}

// githubOwnerRepo extracts (owner, repo, true) from a GitHub remote.
func githubOwnerRepo(remoteURL string) (string, string, bool) {
	m := remoteRe.FindStringSubmatch(strings.TrimSpace(remoteURL))
	if m == nil || !strings.Contains(strings.ToLower(m[1]), "github") {
		return "", "", false
	}
	parts := strings.SplitN(strings.TrimSuffix(m[2], "/"), "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// prCtx resolves repo+origin+owner/repo(GitHub)+token, writing the
// appropriate HTTP error and returning ok=false on any failure.
func (s *svc) prCtx(w http.ResponseWriter, r *http.Request) (owner, name, token string, ok bool) {
	repo, ok := s.repoParam(w, r)
	if !ok {
		return
	}
	res, err := run(r.Context(), repo.Path, "remote", "get-url", "origin")
	if err != nil || res.Code != 0 {
		httpx.WriteErr(w, http.StatusUnprocessableEntity, "repo has no origin remote")
		return "", "", "", false
	}
	owner, name, isgh := githubOwnerRepo(res.Stdout)
	if !isgh {
		httpx.WriteErr(w, http.StatusUnprocessableEntity, "native PR is supported only for GitHub repos")
		return "", "", "", false
	}
	token, has := s.githubToken(repo.Path)
	if !has {
		httpx.WriteErr(w, http.StatusUnprocessableEntity, "GitHub token not found (git credential / vault)")
		return "", "", "", false
	}
	return owner, name, token, true
}

// ---- simplified payloads ----

type prUser struct {
	Login  string `json:"login"`
	Avatar string `json:"avatar_url"`
}
type prRef struct {
	Ref string `json:"ref"`
	Sha string `json:"sha"`
}
type prLabel struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}
type prItem struct {
	Number             int       `json:"number"`
	Title              string    `json:"title"`
	State              string    `json:"state"`
	Draft              bool      `json:"draft"`
	Body               string    `json:"body"`
	HTMLURL            string    `json:"html_url"`
	User               prUser    `json:"user"`
	Head               prRef     `json:"head"`
	Base               prRef     `json:"base"`
	UpdatedAt          string    `json:"updated_at"`
	MergedAt           string    `json:"merged_at"`
	Comments           int       `json:"comments"`
	Additions          int       `json:"additions"`
	Deletions          int       `json:"deletions"`
	ChangedFiles       int       `json:"changed_files"`
	Mergeable          *bool     `json:"mergeable"`
	MergeableState     string    `json:"mergeable_state"`
	Merged             bool      `json:"merged"`
	Labels             []prLabel `json:"labels"`
	RequestedReviewers []prUser  `json:"requested_reviewers"`
}

// ---- GET /pr/list ----

func (s *svc) handlePRList(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.gate(w, r); !ok {
		return
	}
	if !requireGet(w, r) {
		return
	}
	owner, name, token, ok := s.prCtx(w, r)
	if !ok {
		return
	}
	state := r.URL.Query().Get("state")
	if state != "open" && state != "closed" && state != "all" {
		state = "open"
	}
	status, body, err := ghRequest(r.Context(), token, "GET",
		"/repos/"+owner+"/"+name+"/pulls?state="+state+"&per_page=100&sort=updated&direction=desc", nil)
	if err != nil {
		httpx.WriteErr(w, http.StatusBadGateway, "failed to talk to GitHub")
		return
	}
	if status < 200 || status >= 300 {
		httpx.WriteErr(w, http.StatusBadGateway, "GitHub: "+ghErrMsg(status, body))
		return
	}
	var prs []prItem
	json.Unmarshal(body, &prs)
	if prs == nil {
		prs = []prItem{}
	}
	httpx.WriteJSON(w, map[string]any{"prs": prs, "owner": owner, "repo": name})
}

// ---- GET /pr/get ----

func (s *svc) handlePRGet(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.gate(w, r); !ok {
		return
	}
	if !requireGet(w, r) {
		return
	}
	owner, name, token, ok := s.prCtx(w, r)
	if !ok {
		return
	}
	num := r.URL.Query().Get("number")
	if !regexpNum.MatchString(num) {
		httpx.WriteErr(w, http.StatusBadRequest, "invalid number")
		return
	}
	status, body, err := ghRequest(r.Context(), token, "GET", "/repos/"+owner+"/"+name+"/pulls/"+num, nil)
	if err != nil || status < 200 || status >= 300 {
		httpx.WriteErr(w, http.StatusBadGateway, "GitHub: "+ghErrMsg(status, body))
		return
	}
	var pr prItem
	json.Unmarshal(body, &pr)
	// files
	fstatus, fbody, _ := ghRequest(r.Context(), token, "GET", "/repos/"+owner+"/"+name+"/pulls/"+num+"/files?per_page=100", nil)
	var files []struct {
		Filename  string `json:"filename"`
		Status    string `json:"status"`
		Additions int    `json:"additions"`
		Deletions int    `json:"deletions"`
		Patch     string `json:"patch"`
	}
	if fstatus >= 200 && fstatus < 300 {
		json.Unmarshal(fbody, &files)
	}
	// REVIEW comments (per line/file = conversation threads on the diff)
	rcstatus, rcbody, _ := ghRequest(r.Context(), token, "GET", "/repos/"+owner+"/"+name+"/pulls/"+num+"/comments?per_page=100", nil)
	var reviewComments []struct {
		User     prUser `json:"user"`
		Body     string `json:"body"`
		Path     string `json:"path"`
		Line     int    `json:"line"`
		DiffHunk string `json:"diff_hunk"`
		Created  string `json:"created_at"`
	}
	if rcstatus >= 200 && rcstatus < 300 {
		json.Unmarshal(rcbody, &reviewComments)
	}
	// conversation comments (issues API — a PR is an issue)
	cstatus, cbody, _ := ghRequest(r.Context(), token, "GET", "/repos/"+owner+"/"+name+"/issues/"+num+"/comments?per_page=100", nil)
	var comments []struct {
		User      prUser `json:"user"`
		Body      string `json:"body"`
		CreatedAt string `json:"created_at"`
	}
	if cstatus >= 200 && cstatus < 300 {
		json.Unmarshal(cbody, &comments)
	}
	// reviews (approvals / change requests)
	rstatus, rbody, _ := ghRequest(r.Context(), token, "GET", "/repos/"+owner+"/"+name+"/pulls/"+num+"/reviews?per_page=100", nil)
	var reviews []struct {
		User        prUser `json:"user"`
		State       string `json:"state"`
		Body        string `json:"body"`
		SubmittedAt string `json:"submitted_at"`
	}
	if rstatus >= 200 && rstatus < 300 {
		json.Unmarshal(rbody, &reviews)
	}
	// the PR's commits
	mstatus, mbody, _ := ghRequest(r.Context(), token, "GET", "/repos/"+owner+"/"+name+"/pulls/"+num+"/commits?per_page=100", nil)
	var rawCommits []struct {
		Sha    string `json:"sha"`
		Commit struct {
			Message string `json:"message"`
			Author  struct {
				Name string `json:"name"`
				Date string `json:"date"`
			} `json:"author"`
		} `json:"commit"`
	}
	if mstatus >= 200 && mstatus < 300 {
		json.Unmarshal(mbody, &rawCommits)
	}
	commits := make([]map[string]any, 0, len(rawCommits))
	for _, c := range rawCommits {
		subj := c.Commit.Message
		if i := strings.IndexByte(subj, '\n'); i >= 0 {
			subj = subj[:i]
		}
		commits = append(commits, map[string]any{"sha": shortHash(c.Sha), "subject": subj, "author": c.Commit.Author.Name, "date": c.Commit.Author.Date})
	}
	// checks/CI (combines commit status + the head sha's check-runs)
	checks := prChecks(r.Context(), token, owner, name, pr.Head.Sha)
	httpx.WriteJSON(w, map[string]any{
		"pr": pr, "files": files, "comments": comments,
		"reviews": reviews, "commits": commits, "checks": checks,
		"review_comments": reviewComments,
	})
}

// prChecks summarizes the CI state of the head sha: it combines the "combined
// status" (legacy statuses) with the check-runs (GitHub Actions etc.) into a single summary.
func prChecks(ctx context.Context, token, owner, name, sha string) map[string]any {
	out := map[string]any{"total": 0, "passed": 0, "failed": 0, "pending": 0, "state": "none"}
	if sha == "" {
		return out
	}
	total, passed, failed, pending := 0, 0, 0, 0
	// combined status (Travis-like / contexts)
	if st, body, err := ghRequest(ctx, token, "GET", "/repos/"+owner+"/"+name+"/commits/"+sha+"/status", nil); err == nil && st >= 200 && st < 300 {
		var cs struct {
			Statuses []struct {
				State string `json:"state"`
			} `json:"statuses"`
		}
		json.Unmarshal(body, &cs)
		for _, s := range cs.Statuses {
			total++
			switch s.State {
			case "success":
				passed++
			case "failure", "error":
				failed++
			default:
				pending++
			}
		}
	}
	// check-runs (GitHub Actions)
	if st, body, err := ghRequest(ctx, token, "GET", "/repos/"+owner+"/"+name+"/commits/"+sha+"/check-runs?per_page=100", nil); err == nil && st >= 200 && st < 300 {
		var cr struct {
			CheckRuns []struct {
				Status     string `json:"status"`
				Conclusion string `json:"conclusion"`
			} `json:"check_runs"`
		}
		json.Unmarshal(body, &cr)
		for _, c := range cr.CheckRuns {
			total++
			if c.Status != "completed" {
				pending++
			} else if c.Conclusion == "success" || c.Conclusion == "neutral" || c.Conclusion == "skipped" {
				passed++
			} else {
				failed++
			}
		}
	}
	state := "none"
	if total > 0 {
		state = "pending"
		if failed > 0 {
			state = "failure"
		} else if pending == 0 {
			state = "success"
		}
	}
	return map[string]any{"total": total, "passed": passed, "failed": failed, "pending": pending, "state": state}
}

// prWriteCtx = writeGate (writable repo) + resolves owner/repo(GitHub)+token.
func (s *svc) prWriteCtx(w http.ResponseWriter, r *http.Request) (caller, owner, name, token string, ok bool) {
	caller, repo, ok := s.writeGate(w, r)
	if !ok {
		return
	}
	res, err := run(r.Context(), repo.Path, "remote", "get-url", "origin")
	if err != nil || res.Code != 0 {
		httpx.WriteErr(w, http.StatusUnprocessableEntity, "repo has no origin remote")
		return "", "", "", "", false
	}
	o, n, isgh := githubOwnerRepo(res.Stdout)
	if !isgh {
		httpx.WriteErr(w, http.StatusUnprocessableEntity, "native PR is GitHub only")
		return "", "", "", "", false
	}
	t, has := s.githubToken(repo.Path)
	if !has {
		httpx.WriteErr(w, http.StatusUnprocessableEntity, "GitHub token not found (git credential / vault)")
		return "", "", "", "", false
	}
	return caller, o, n, t, true
}

// ---- POST /pr/update (edit title/body/base; close/reopen through state) ----

func (s *svc) handlePRUpdate(w http.ResponseWriter, r *http.Request) {
	caller, owner, name, token, ok := s.prWriteCtx(w, r)
	if !ok {
		return
	}
	var b struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		Body   string `json:"body"`
		Base   string `json:"base"`
		State  string `json:"state"` // "open" | "closed"
	}
	if err := decodeBody(r, &b); err != nil || b.Number <= 0 {
		httpx.WriteErr(w, http.StatusBadRequest, "invalid number/body")
		return
	}
	patch := map[string]any{}
	if strings.TrimSpace(b.Title) != "" {
		patch["title"] = b.Title
	}
	if b.Body != "" {
		patch["body"] = b.Body
	}
	if b.Base != "" {
		if !validRef(b.Base) {
			httpx.WriteErr(w, http.StatusBadRequest, "invalid base")
			return
		}
		patch["base"] = b.Base
	}
	if b.State == "open" || b.State == "closed" {
		patch["state"] = b.State
	}
	if len(patch) == 0 {
		httpx.WriteErr(w, http.StatusBadRequest, "nothing to update")
		return
	}
	payload, _ := json.Marshal(patch)
	status, body, err := ghRequest(r.Context(), token, "PATCH", "/repos/"+owner+"/"+name+"/pulls/"+strconv.Itoa(b.Number), payload)
	if err != nil || status < 200 || status >= 300 {
		httpx.WriteErr(w, http.StatusBadGateway, "GitHub: "+ghErrMsg(status, body))
		return
	}
	var pr prItem
	json.Unmarshal(body, &pr)
	httpx.AuditEvent(s.audit, r, caller, "git.pr_update", owner+"/"+name+" #"+strconv.Itoa(b.Number))
	httpx.WriteJSON(w, map[string]any{"ok": true, "pr": pr})
}

// ---- POST /pr/merge ----

func (s *svc) handlePRMerge(w http.ResponseWriter, r *http.Request) {
	caller, owner, name, token, ok := s.prWriteCtx(w, r)
	if !ok {
		return
	}
	var b struct {
		Number       int    `json:"number"`
		Method       string `json:"method"` // merge | squash | rebase
		Title        string `json:"title"`
		DeleteBranch bool   `json:"delete_branch"`
		Head         string `json:"head"`
	}
	if err := decodeBody(r, &b); err != nil || b.Number <= 0 {
		httpx.WriteErr(w, http.StatusBadRequest, "invalid number")
		return
	}
	if b.Method != "merge" && b.Method != "squash" && b.Method != "rebase" {
		b.Method = "merge"
	}
	m := map[string]any{"merge_method": b.Method}
	if strings.TrimSpace(b.Title) != "" {
		m["commit_title"] = b.Title
	}
	payload, _ := json.Marshal(m)
	status, body, err := ghRequest(r.Context(), token, "PUT", "/repos/"+owner+"/"+name+"/pulls/"+strconv.Itoa(b.Number)+"/merge", payload)
	if err != nil || status < 200 || status >= 300 {
		httpx.WriteErr(w, http.StatusBadGateway, "GitHub: "+ghErrMsg(status, body))
		return
	}
	deleted := false
	if b.DeleteBranch && validRef(b.Head) {
		// delete the source branch after the merge (best-effort)
		ds, _, _ := ghRequest(r.Context(), token, "DELETE", "/repos/"+owner+"/"+name+"/git/refs/heads/"+b.Head, nil)
		deleted = ds >= 200 && ds < 300
	}
	httpx.AuditEvent(s.audit, r, caller, "git.pr_merge", owner+"/"+name+" #"+strconv.Itoa(b.Number)+" ("+b.Method+")")
	httpx.WriteJSON(w, map[string]any{"ok": true, "branch_deleted": deleted})
}

// ---- POST /pr/review (approve / request changes / comment) ----

func (s *svc) handlePRReview(w http.ResponseWriter, r *http.Request) {
	caller, owner, name, token, ok := s.prWriteCtx(w, r)
	if !ok {
		return
	}
	var b struct {
		Number int    `json:"number"`
		Event  string `json:"event"` // APPROVE | REQUEST_CHANGES | COMMENT
		Body   string `json:"body"`
	}
	if err := decodeBody(r, &b); err != nil || b.Number <= 0 {
		httpx.WriteErr(w, http.StatusBadRequest, "invalid number")
		return
	}
	if b.Event != "APPROVE" && b.Event != "REQUEST_CHANGES" && b.Event != "COMMENT" {
		httpx.WriteErr(w, http.StatusBadRequest, "invalid event")
		return
	}
	if b.Event != "APPROVE" && strings.TrimSpace(b.Body) == "" {
		httpx.WriteErr(w, http.StatusBadRequest, "a comment is required for this review type")
		return
	}
	payload, _ := json.Marshal(map[string]any{"event": b.Event, "body": b.Body})
	status, body, err := ghRequest(r.Context(), token, "POST", "/repos/"+owner+"/"+name+"/pulls/"+strconv.Itoa(b.Number)+"/reviews", payload)
	if err != nil || status < 200 || status >= 300 {
		// GitHub refuses (422) to approve your own PR; the raw message
		// ("Unprocessable Entity") is confusing. We give the real cause + the way out:
		// in a repo with no review rule, approval is not even needed — Merge is enough.
		if b.Event == "APPROVE" && status == http.StatusUnprocessableEntity {
			httpx.WriteErr(w, http.StatusUnprocessableEntity,
				"GitHub does not allow approving your own PR. If there is no required-review rule, skip the approval and use the Merge button directly. (detail: "+ghErrMsg(status, body)+")")
			return
		}
		httpx.WriteErr(w, http.StatusBadGateway, "GitHub: "+ghErrMsg(status, body))
		return
	}
	httpx.AuditEvent(s.audit, r, caller, "git.pr_review", owner+"/"+name+" #"+strconv.Itoa(b.Number)+" "+b.Event)
	httpx.WriteJSON(w, map[string]any{"ok": true})
}

// ---- POST /pr/comment (a comment in the conversation) ----

func (s *svc) handlePRComment(w http.ResponseWriter, r *http.Request) {
	caller, owner, name, token, ok := s.prWriteCtx(w, r)
	if !ok {
		return
	}
	var b struct {
		Number int    `json:"number"`
		Body   string `json:"body"`
	}
	if err := decodeBody(r, &b); err != nil || b.Number <= 0 || strings.TrimSpace(b.Body) == "" {
		httpx.WriteErr(w, http.StatusBadRequest, "invalid number/comment")
		return
	}
	payload, _ := json.Marshal(map[string]any{"body": b.Body})
	status, body, err := ghRequest(r.Context(), token, "POST", "/repos/"+owner+"/"+name+"/issues/"+strconv.Itoa(b.Number)+"/comments", payload)
	if err != nil || status < 200 || status >= 300 {
		httpx.WriteErr(w, http.StatusBadGateway, "GitHub: "+ghErrMsg(status, body))
		return
	}
	httpx.AuditEvent(s.audit, r, caller, "git.pr_comment", owner+"/"+name+" #"+strconv.Itoa(b.Number))
	httpx.WriteJSON(w, map[string]any{"ok": true})
}

var regexpNum = regexp.MustCompile(`^[0-9]{1,9}$`)

// ---- POST /pr/create ----

func (s *svc) handlePRCreate(w http.ResponseWriter, r *http.Request) {
	caller, repo, ok := s.writeGate(w, r) // creating a PR requires a writable repo
	if !ok {
		return
	}
	owner, name, token, ok := func() (string, string, string, bool) {
		res, err := run(r.Context(), repo.Path, "remote", "get-url", "origin")
		if err != nil || res.Code != 0 {
			httpx.WriteErr(w, http.StatusUnprocessableEntity, "repo has no origin remote")
			return "", "", "", false
		}
		o, n, isgh := githubOwnerRepo(res.Stdout)
		if !isgh {
			httpx.WriteErr(w, http.StatusUnprocessableEntity, "native PR is GitHub only")
			return "", "", "", false
		}
		t, has := s.githubToken(repo.Path)
		if !has {
			httpx.WriteErr(w, http.StatusUnprocessableEntity, "GitHub token not found (git credential / vault)")
			return "", "", "", false
		}
		return o, n, t, true
	}()
	if !ok {
		return
	}
	var body struct {
		Title     string   `json:"title"`
		Body      string   `json:"body"`
		Head      string   `json:"head"`
		Base      string   `json:"base"`
		Draft     bool     `json:"draft"`
		Reviewers []string `json:"reviewers"`
	}
	if err := decodeBody(r, &body); err != nil {
		httpx.WriteErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	if strings.TrimSpace(body.Title) == "" || !validRef(body.Head) || !validRef(body.Base) {
		httpx.WriteErr(w, http.StatusBadRequest, "title/head/base are required and must be valid")
		return
	}
	payload, _ := json.Marshal(map[string]any{
		"title": body.Title, "body": body.Body, "head": body.Head, "base": body.Base, "draft": body.Draft,
	})
	status, respBody, err := ghRequest(r.Context(), token, "POST", "/repos/"+owner+"/"+name+"/pulls", payload)
	if err != nil {
		httpx.WriteErr(w, http.StatusBadGateway, "failed to talk to GitHub")
		return
	}
	if status < 200 || status >= 300 {
		httpx.WriteErr(w, http.StatusBadGateway, "GitHub: "+ghErrMsg(status, respBody))
		return
	}
	var pr prItem
	json.Unmarshal(respBody, &pr)
	// request reviewers (best-effort: an error here does not fail the create)
	var cleanRev []string
	for _, rv := range body.Reviewers {
		rv = strings.TrimSpace(rv)
		if rv != "" {
			cleanRev = append(cleanRev, rv)
		}
	}
	if len(cleanRev) > 0 && pr.Number > 0 {
		rp, _ := json.Marshal(map[string]any{"reviewers": cleanRev})
		ghRequest(r.Context(), token, "POST", "/repos/"+owner+"/"+name+"/pulls/"+strconv.Itoa(pr.Number)+"/requested_reviewers", rp)
	}
	httpx.AuditEvent(s.audit, r, caller, "git.pr_create", repo.ID+" #"+strconv.Itoa(pr.Number)+" "+body.Head+"→"+body.Base)
	httpx.WriteJSON(w, map[string]any{"ok": true, "pr": pr})
}
