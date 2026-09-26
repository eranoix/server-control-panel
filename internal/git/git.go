package git

import (
	"net/http"
	"strconv"
	"strings"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/config"
	"server-control-panel/internal/httpx"
	"server-control-panel/internal/secrets"
)

// svc carries the dependencies shared by the routes (read in git.go, write in
// write.go, PR in pr.go). cfg.GitRepos is treated as immutable post-load
// (read, never written at runtime). vault is the credential vault (it may be
// nil when secrets are disabled) — used only to read the GitHub token for the
// PR routes.
type svc struct {
	cfg   *config.Config
	audit *auth.AuditLog
	vault *secrets.Store
}

// Handler assembles the Git client's sub-mux (mounted at /api/git/ by api.go).
// EVERY route opens with httpx.MustPrimary — a primary-only surface. The
// mould = files.Handler (NewServeMux + HandleFunc), but reusing
// httpx.{WriteJSON,WriteErr,AuditEvent} (exported) instead of its own helpers.
func Handler(cfg *config.Config, audit *auth.AuditLog, vault *secrets.Store) http.Handler {
	s := &svc{cfg: cfg, audit: audit, vault: vault}
	mux := http.NewServeMux()
	// Reading
	mux.HandleFunc("/repos", s.handleRepos)
	mux.HandleFunc("/graph", s.handleGraph)
	mux.HandleFunc("/status", s.handleStatus)
	mux.HandleFunc("/branches", s.handleBranches)
	mux.HandleFunc("/diff", s.handleDiff)
	mux.HandleFunc("/file", s.handleFile)
	mux.HandleFunc("/show", s.handleShow)
	mux.HandleFunc("/tags", s.handleTags)
	mux.HandleFunc("/stashes", s.handleStashes)
	mux.HandleFunc("/remotes", s.handleRemotes)
	mux.HandleFunc("/identities", s.handleIdentities)
	mux.HandleFunc("/pr-url", s.handlePRURL)
	// Inspection (read3.go): blame, file history, compare refs
	mux.HandleFunc("/blame", s.handleBlame)
	mux.HandleFunc("/filelog", s.handleFileLog)
	mux.HandleFunc("/compare", s.handleCompare)
	// Basic writing (write.go)
	mux.HandleFunc("/write", s.handleWrite)
	mux.HandleFunc("/stage", s.handleStage)
	mux.HandleFunc("/unstage", s.handleUnstage)
	mux.HandleFunc("/apply", s.handleApply)
	mux.HandleFunc("/commit", s.handleCommit)
	mux.HandleFunc("/branch/create", s.handleBranchCreate)
	mux.HandleFunc("/checkout", s.handleCheckout)
	mux.HandleFunc("/discard", s.handleDiscard)
	// Advanced actions (actions.go)
	mux.HandleFunc("/tag/create", s.handleTagCreate)
	mux.HandleFunc("/tag/delete", s.handleTagDelete)
	mux.HandleFunc("/cherry-pick", s.handleCherryPick)
	mux.HandleFunc("/revert", s.handleRevert)
	mux.HandleFunc("/merge", s.handleMerge)
	mux.HandleFunc("/rebase", s.handleRebase)
	mux.HandleFunc("/reset", s.handleReset)
	mux.HandleFunc("/drop", s.handleDrop)
	mux.HandleFunc("/branch/rename", s.handleBranchRename)
	mux.HandleFunc("/branch/delete", s.handleBranchDelete)
	mux.HandleFunc("/stash/push", s.handleStashPush)
	mux.HandleFunc("/stash/apply", s.handleStashApply)
	mux.HandleFunc("/stash/pop", s.handleStashPop)
	mux.HandleFunc("/stash/drop", s.handleStashDrop)
	mux.HandleFunc("/stash/branch", s.handleStashBranch)
	mux.HandleFunc("/clean", s.handleClean)
	mux.HandleFunc("/reset-uncommitted", s.handleResetUncommitted)
	mux.HandleFunc("/fetch", s.handleFetch)
	mux.HandleFunc("/pull", s.handlePull)
	mux.HandleFunc("/push", s.handlePush)
	// Advanced ops (actions2.go): reflog/undo, interactive rebase, conflicts, cherry-pick onto
	mux.HandleFunc("/reflog", s.handleReflog)
	mux.HandleFunc("/reflog/reset", s.handleReflogReset)
	mux.HandleFunc("/rebase/todo", s.handleRebaseTodo)
	mux.HandleFunc("/rebase/interactive", s.handleRebaseInteractive)
	mux.HandleFunc("/rebase/abort", s.handleRebaseAbort)
	mux.HandleFunc("/rebase/continue", s.handleRebaseContinue)
	mux.HandleFunc("/conflicts", s.handleConflicts)
	mux.HandleFunc("/conflict/sides", s.handleConflictSides)
	mux.HandleFunc("/conflict/resolve", s.handleConflictResolve)
	mux.HandleFunc("/cherry-pick-onto", s.handleCherryPickOnto)
	// Worktrees (worktrees.go)
	mux.HandleFunc("/worktrees", s.handleWorktrees)
	mux.HandleFunc("/worktree/add", s.handleWorktreeAdd)
	mux.HandleFunc("/worktree/remove", s.handleWorktreeRemove)
	// Branches/tags/stashes/submodules (actions3.go)
	mux.HandleFunc("/remote/prune", s.handleRemotePrune)
	mux.HandleFunc("/tag/push", s.handleTagPush)
	mux.HandleFunc("/stash/show", s.handleStashShow)
	mux.HandleFunc("/submodules", s.handleSubmodules)
	mux.HandleFunc("/submodule/update", s.handleSubmoduleUpdate)
	mux.HandleFunc("/patch", s.handlePatch)
	// Pull Requests (pr.go) — native, through the GitHub API
	mux.HandleFunc("/pr/list", s.handlePRList)
	mux.HandleFunc("/pr/get", s.handlePRGet)
	mux.HandleFunc("/pr/create", s.handlePRCreate)
	mux.HandleFunc("/pr/update", s.handlePRUpdate)
	mux.HandleFunc("/pr/merge", s.handlePRMerge)
	mux.HandleFunc("/pr/comment", s.handlePRComment)
	mux.HandleFunc("/pr/review", s.handlePRReview)
	return mux
}

// gate runs MustPrimary and returns (caller, ok). ok=false means the error
// response has already been written.
func (s *svc) gate(w http.ResponseWriter, r *http.Request) (string, bool) {
	return httpx.MustPrimary(w, r, s.cfg, s.audit)
}

// repoParam resolves the repo from the "repo" query (id), writing a 404 when
// it is not in the allowlist or is not a git repo on disk.
func (s *svc) repoParam(w http.ResponseWriter, r *http.Request) (config.GitRepo, bool) {
	id := r.URL.Query().Get("repo")
	repo, ok := resolveRepo(s.cfg, id)
	if !ok {
		httpx.WriteErr(w, http.StatusNotFound, "repo not found in the allowlist")
		return config.GitRepo{}, false
	}
	return repo, true
}

// requireGet refuses methods != GET.
func requireGet(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodGet {
		httpx.WriteErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return false
	}
	return true
}

// ---- GET /repos ----

func (s *svc) handleRepos(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.gate(w, r); !ok {
		return
	}
	if !requireGet(w, r) {
		return
	}
	ctx := r.Context()
	// GitRepos==nil → effectiveRepos returns only the seed; we filter down to the
	// ones that exist on disk. An empty list → [] (200), never a panic/500.
	out := []repoStatus{}
	for _, repo := range effectiveRepos(s.cfg) {
		if !isGitRepo(repo.Path) {
			continue
		}
		out = append(out, describeRepo(ctx, repo))
	}
	httpx.WriteJSON(w, map[string]any{"repos": out})
}

// ---- GET /graph ----

// Commit is a node of the graph. Lane is assigned server-side by layoutGraph
// so that the front end's SVG renderer can stay dumb (it only draws) and the
// layout stays testable by a Go unit test.
type Commit struct {
	Hash    string   `json:"hash"`
	Short   string   `json:"short"`
	Parents []string `json:"parents"`
	Author  string   `json:"author"`
	Email   string   `json:"email"`
	Date    string   `json:"date"`
	Refs    []string `json:"refs"`
	Subject string   `json:"subject"`
	Lane    int      `json:"lane"`
}

// Edge links a commit (the child, at row FromRow/FromLane) to its parent. If
// the parent is not inside the returned window (dangling at the edge of the
// limit), ToRow=-1 and Dangling=true — the renderer draws a truncated stub,
// never a line running off into nothing.
type Edge struct {
	FromRow  int  `json:"from_row"`
	ToRow    int  `json:"to_row"`
	FromLane int  `json:"from_lane"`
	ToLane   int  `json:"to_lane"`
	Dangling bool `json:"dangling"`
}

// Graph is the payload of /graph: commits already carrying a lane, edges, the
// maximum lane, and whether the limit truncated anything.
type Graph struct {
	Commits   []Commit `json:"commits"`
	Edges     []Edge   `json:"edges"`
	MaxLane   int      `json:"max_lane"`
	Truncated bool     `json:"truncated"`
}

func (s *svc) handleGraph(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.gate(w, r); !ok {
		return
	}
	if !requireGet(w, r) {
		return
	}
	repo, ok := s.repoParam(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	limit := clampLimit(atoiDefault(q.Get("limit"), 200), 200)

	// Filters: ref scope + author/date. It asks for limit+1 so that truncation
	// can be detected (the "load more" button).
	args := []string{"log", "--date-order", "--max-count=" + strconv.Itoa(limit+1)}
	if a := q.Get("author"); a != "" && len(a) <= 200 {
		args = append(args, "--author="+a)
	}
	if s := q.Get("since"); s != "" && len(s) <= 64 {
		args = append(args, "--since="+s)
	}
	if u := q.Get("until"); u != "" && len(u) <= 64 {
		args = append(args, "--until="+u)
	}
	args = append(args, "--pretty=format:%H%x00%P%x00%an%x00%ae%x00%aI%x00%D%x00%s")
	// Ref scope (mutually exclusive, in this order of priority):
	switch {
	case q.Get("current") == "1":
		args = append(args, "HEAD")
	case q.Get("branch") != "":
		if !validRef(q.Get("branch")) {
			httpx.WriteErr(w, http.StatusBadRequest, "invalid branch")
			return
		}
		args = append(args, q.Get("branch"))
	case q.Get("no_remotes") == "1":
		args = append(args, "--branches", "--tags")
	default:
		args = append(args, "--all")
	}
	res, err := run(r.Context(), repo.Path, args...)
	if err != nil {
		httpx.WriteErr(w, http.StatusInternalServerError, "git log failed")
		return
	}
	if res.Code != 0 {
		// A repo with no commits (HEAD nonexistent) → an empty graph, not an error.
		httpx.WriteJSON(w, Graph{Commits: []Commit{}, Edges: []Edge{}})
		return
	}
	commits := parseLog(res.Stdout)
	truncated := len(commits) > limit
	if truncated {
		commits = commits[:limit]
	}
	g := layoutGraph(commits)
	g.Truncated = truncated
	httpx.WriteJSON(w, g)
}

// parseLog converts the output of `git log --pretty=format:%H\0%P\0%an\0%aI\0%D\0%s`
// into []Commit (without lanes). Records are separated by \n; fields by \x00.
func parseLog(out string) []Commit {
	out = strings.TrimRight(out, "\n")
	if out == "" {
		return []Commit{}
	}
	lines := strings.Split(out, "\n")
	commits := make([]Commit, 0, len(lines))
	for _, ln := range lines {
		if ln == "" {
			continue
		}
		f := strings.Split(ln, "\x00")
		if len(f) < 7 {
			continue
		}
		c := Commit{
			Hash:    f[0],
			Short:   shortHash(f[0]),
			Author:  f[2],
			Email:   f[3],
			Date:    f[4],
			Refs:    parseRefs(f[5]),
			Subject: f[6],
		}
		if p := strings.TrimSpace(f[1]); p != "" {
			c.Parents = strings.Fields(p)
		}
		commits = append(commits, c)
	}
	return commits
}

func shortHash(h string) string {
	if len(h) > 8 {
		return h[:8]
	}
	return h
}

// parseRefs breaks the %D field ("HEAD -> branch, origin/main, tag: v1") into
// a clean list. Empty on commits with no decoration.
func parseRefs(d string) []string {
	d = strings.TrimSpace(d)
	if d == "" {
		return nil
	}
	parts := strings.Split(d, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// layoutGraph assigns deterministic lanes and generates edges. The classic
// lane algorithm: it walks the commits top to bottom (already in date-order);
// each lane "waits for" the hash of the next commit that will land in it. The
// first parent stays in the child's lane; extra parents (merges) get lanes of
// their own; when two lanes wait for the same commit they converge (one
// survives, the others are released). Edges to parents outside the window are
// truncated (Dangling); they never point off into nothing.
func layoutGraph(commits []Commit) Graph {
	rowOf := make(map[string]int, len(commits))
	for i, c := range commits {
		rowOf[c.Hash] = i
	}

	lanes := []string{} // lanes[k] = the hash lane k is waiting for ("" = free)
	edges := []Edge{}
	maxLane := 0

	reserve := func(val string) int {
		for k := range lanes {
			if lanes[k] == "" {
				lanes[k] = val
				return k
			}
		}
		lanes = append(lanes, val)
		return len(lanes) - 1
	}
	indexOf := func(val string) int {
		for k := range lanes {
			if lanes[k] == val {
				return k
			}
		}
		return -1
	}

	for i := range commits {
		c := &commits[i]

		// The commit's lane = the first lane waiting for it; extra lanes that were
		// also waiting for it converge (and are released).
		myLane := -1
		for k := range lanes {
			if lanes[k] == c.Hash {
				if myLane == -1 {
					myLane = k
				} else {
					lanes[k] = ""
				}
			}
		}
		if myLane == -1 {
			// An unawaited tip (a branch head): allocate a lane, reserving it.
			myLane = reserve(c.Hash)
		}
		c.Lane = myLane
		if myLane > maxLane {
			maxLane = myLane
		}

		if len(c.Parents) == 0 {
			lanes[myLane] = "" // the lane ends here (root)
			continue
		}
		for pi, p := range c.Parents {
			var pLane int
			if pi == 0 {
				lanes[myLane] = p // the first parent stays in the child's lane
				pLane = myLane
			} else if ex := indexOf(p); ex >= 0 {
				pLane = ex // a lane is already waiting for this parent → converge
			} else {
				pLane = reserve(p) // merge: the extra parent gets a new lane
			}
			if pLane > maxLane {
				maxLane = pLane
			}
			e := Edge{FromRow: i, FromLane: myLane, ToLane: pLane}
			if pr, ok := rowOf[p]; ok {
				e.ToRow = pr
			} else {
				e.ToRow = -1
				e.Dangling = true
			}
			edges = append(edges, e)
		}
	}

	if commits == nil {
		commits = []Commit{}
	}
	return Graph{Commits: commits, Edges: edges, MaxLane: maxLane}
}

// ---- GET /status ----

func (s *svc) handleStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.gate(w, r); !ok {
		return
	}
	if !requireGet(w, r) {
		return
	}
	repo, ok := s.repoParam(w, r)
	if !ok {
		return
	}
	res, err := run(r.Context(), repo.Path, "status", "--porcelain=v2", "--branch", "-z")
	if err != nil || res.Code != 0 {
		httpx.WriteErr(w, http.StatusInternalServerError, "git status failed")
		return
	}
	branch, detached, entries := parseStatusV2(res.Stdout)
	httpx.WriteJSON(w, map[string]any{
		"branch":   branch,
		"detached": detached,
		"entries":  entries,
		"writable": canWrite(repo),
	})
}

type statusEntry struct {
	Path      string `json:"path"`
	Orig      string `json:"orig,omitempty"`
	Index     string `json:"index"`    // status staged (X)
	Worktree  string `json:"worktree"` // status unstaged (Y)
	Untracked bool   `json:"untracked,omitempty"`
}

// parseStatusV2 interprets `status --porcelain=v2 --branch -z`. Tokens are
// separated by NUL; '1'/'2' lines carry the path to the end of the record
// (and '2' consumes one extra token = origPath); '?' = untracked; '#' = headers.
func parseStatusV2(out string) (branch string, detached bool, entries []statusEntry) {
	entries = []statusEntry{}
	toks := strings.Split(out, "\x00")
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		if t == "" {
			continue
		}
		switch t[0] {
		case '#':
			fields := strings.Fields(t)
			if len(fields) >= 3 && fields[1] == "branch.head" {
				if fields[2] == "(detached)" {
					detached = true
				} else {
					branch = fields[2]
				}
			}
		case '1':
			fields := strings.SplitN(t, " ", 9)
			if len(fields) < 9 {
				continue
			}
			xy := fields[1]
			entries = append(entries, statusEntry{
				Path: fields[8], Index: string(xy[0]), Worktree: string(xy[1]),
			})
		case '2':
			fields := strings.SplitN(t, " ", 10)
			if len(fields) < 10 {
				continue
			}
			xy := fields[1]
			e := statusEntry{Path: fields[9], Index: string(xy[0]), Worktree: string(xy[1])}
			if i+1 < len(toks) { // origPath = the next token
				e.Orig = toks[i+1]
				i++
			}
			entries = append(entries, e)
		case 'u':
			fields := strings.SplitN(t, " ", 11)
			if len(fields) < 11 {
				continue
			}
			entries = append(entries, statusEntry{
				Path: fields[10], Index: "U", Worktree: "U",
			})
		case '?':
			entries = append(entries, statusEntry{
				Path: strings.TrimSpace(t[1:]), Index: "?", Worktree: "?", Untracked: true,
			})
		}
	}
	return branch, detached, entries
}

// ---- GET /branches ----

func (s *svc) handleBranches(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.gate(w, r); !ok {
		return
	}
	if !requireGet(w, r) {
		return
	}
	repo, ok := s.repoParam(w, r)
	if !ok {
		return
	}
	res, err := run(r.Context(), repo.Path, "for-each-ref",
		"--sort=-committerdate",
		"--format=%(refname:short)%00%(objectname:short)%00%(HEAD)%00%(upstream:short)%00%(upstream:track)%00%(committerdate:relative)%00%(contents:subject)",
		"refs/heads")
	if err != nil || res.Code != 0 {
		httpx.WriteErr(w, http.StatusInternalServerError, "git branches failed")
		return
	}
	type branchInfo struct {
		Name     string `json:"name"`
		Short    string `json:"short"`
		Current  bool   `json:"current"`
		Upstream string `json:"upstream,omitempty"`
		Ahead    int    `json:"ahead"`
		Behind   int    `json:"behind"`
		Gone     bool   `json:"gone"`
		Rel      string `json:"rel"`
		Subject  string `json:"subject"`
	}
	out := []branchInfo{}
	for _, ln := range strings.Split(strings.TrimRight(res.Stdout, "\n"), "\n") {
		if ln == "" {
			continue
		}
		f := strings.Split(ln, "\x00")
		if len(f) < 7 {
			continue
		}
		bi := branchInfo{Name: f[0], Short: f[1], Current: f[2] == "*", Upstream: f[3], Rel: f[5], Subject: f[6]}
		bi.Ahead, bi.Behind, bi.Gone = parseTrack(f[4])
		out = append(out, bi)
	}
	httpx.WriteJSON(w, map[string]any{"branches": out})
}

// ---- GET /diff ----

// handleDiff returns the patch of a path: working tree, staged (?staged=1),
// or from a specific commit (?hash=). Always with the path AFTER "--".
func (s *svc) handleDiff(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.gate(w, r); !ok {
		return
	}
	if !requireGet(w, r) {
		return
	}
	repo, ok := s.repoParam(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	path := q.Get("path")
	if path != "" && !validRelPath(path) {
		httpx.WriteErr(w, http.StatusBadRequest, "invalid path")
		return
	}
	var args []string
	if hash := q.Get("hash"); hash != "" {
		if !validRef(hash) {
			httpx.WriteErr(w, http.StatusBadRequest, "invalid hash")
			return
		}
		// Diff of the commit vs its first parent (^! covers the root commit).
		args = []string{"diff", hash + "^!", "--"}
	} else if q.Get("staged") == "1" || q.Get("staged") == "true" {
		args = []string{"diff", "--cached", "--"}
	} else {
		args = []string{"diff", "--"}
	}
	if path != "" {
		args = append(args, path)
	}
	res, err := run(r.Context(), repo.Path, args...)
	if err != nil {
		httpx.WriteErr(w, http.StatusInternalServerError, "git diff failed")
		return
	}
	httpx.WriteJSON(w, map[string]any{"diff": res.Stdout})
}

// ---- GET /file ----

// handleFile returns a file's content: working tree (no ?ref=) or as of a
// commit's state (?ref=<hash>). For Monaco to open/edit/view.
func (s *svc) handleFile(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.gate(w, r); !ok {
		return
	}
	if !requireGet(w, r) {
		return
	}
	repo, ok := s.repoParam(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	path := q.Get("path")
	if !validRelPath(path) {
		httpx.WriteErr(w, http.StatusBadRequest, "invalid path")
		return
	}
	if ref := q.Get("ref"); ref != "" {
		if !validRef(ref) {
			httpx.WriteErr(w, http.StatusBadRequest, "invalid ref")
			return
		}
		res, err := run(r.Context(), repo.Path, "show", ref+":"+path)
		if err != nil {
			httpx.WriteErr(w, http.StatusInternalServerError, "git show failed")
			return
		}
		if res.Code != 0 {
			httpx.WriteErr(w, http.StatusNotFound, "file does not exist at that ref")
			return
		}
		httpx.WriteJSON(w, map[string]any{"content": res.Stdout, "ref": ref, "path": path})
		return
	}
	// Working tree: read from disk, jailed to the repo root.
	content, err := readRepoFile(repo.Path, path)
	if err != nil {
		switch err {
		case errIsDir, errTooLarge, errBinary:
			httpx.WriteErr(w, http.StatusUnprocessableEntity, err.Error())
		default:
			httpx.WriteErr(w, http.StatusNotFound, "file not found")
		}
		return
	}
	httpx.WriteJSON(w, map[string]any{"content": content, "path": path})
}

// ---- GET /show ----

// handleShow returns a commit's metadata + the changed files (name-status),
// for "click the commit → list of files".
func (s *svc) handleShow(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.gate(w, r); !ok {
		return
	}
	if !requireGet(w, r) {
		return
	}
	repo, ok := s.repoParam(w, r)
	if !ok {
		return
	}
	hash := r.URL.Query().Get("hash")
	if !validRef(hash) {
		httpx.WriteErr(w, http.StatusBadRequest, "invalid hash")
		return
	}
	// Metadata.
	meta, err := run(r.Context(), repo.Path, "show", "-s",
		"--pretty=format:%H%x00%an%x00%ae%x00%aI%x00%P%x00%s%x00%b", hash)
	if err != nil || meta.Code != 0 {
		httpx.WriteErr(w, http.StatusNotFound, "commit not found")
		return
	}
	f := strings.Split(meta.Stdout, "\x00")
	detail := map[string]any{"hash": hash}
	if len(f) >= 7 {
		detail = map[string]any{
			"hash": f[0], "author_name": f[1], "author_email": f[2],
			"date": f[3], "parents": strings.Fields(f[4]),
			"subject": f[5], "body": f[6],
		}
	}
	// Changed files (vs the first parent; a root commit uses --root).
	fres, _ := run(r.Context(), repo.Path, "diff-tree", "--no-commit-id",
		"--name-status", "-r", "-z", "--root", hash)
	files := parseNameStatusZ(fres.Stdout)
	// numstat (+/− per file) merged by path.
	nres, _ := run(r.Context(), repo.Path, "diff-tree", "--no-commit-id",
		"--numstat", "-r", "-z", "--root", hash)
	stats := parseNumstatZ(nres.Stdout)
	var totA, totD int
	for i := range files {
		if st, ok := stats[files[i].Path]; ok {
			files[i].Add = st[0]
			files[i].Del = st[1]
			totA += st[0]
			totD += st[1]
		}
	}
	detail["files"] = files
	detail["additions"] = totA
	detail["deletions"] = totD
	httpx.WriteJSON(w, detail)
}

type changedFile struct {
	Status string `json:"status"`
	Path   string `json:"path"`
	Orig   string `json:"orig,omitempty"`
	Add    int    `json:"add"`
	Del    int    `json:"del"`
}

// parseNumstatZ interprets `diff-tree --numstat -z`: <add>\t<del>\t<path>,
// with renames bringing <add>\t<del>\0<old>\0<new>. Binaries carry "-".
func parseNumstatZ(out string) map[string][2]int {
	m := map[string][2]int{}
	toks := strings.Split(out, "\x00")
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		if t == "" || !strings.Contains(t, "\t") {
			continue
		}
		parts := strings.SplitN(t, "\t", 3)
		if len(parts) < 3 {
			continue
		}
		add, _ := strconv.Atoi(parts[0])
		del, _ := strconv.Atoi(parts[1])
		path := parts[2]
		if path == "" && i+2 < len(toks) { // rename: the path comes in the next 2 tokens
			path = toks[i+2]
			i += 2
		}
		if path != "" {
			m[path] = [2]int{add, del}
		}
	}
	return m
}

// parseNameStatusZ interprets `diff-tree --name-status -z`: <status>\0<path>
// pairs, with renames bringing <Rxxx>\0<orig>\0<new>.
func parseNameStatusZ(out string) []changedFile {
	toks := strings.Split(out, "\x00")
	files := []changedFile{}
	for i := 0; i < len(toks); i++ {
		st := toks[i]
		if st == "" {
			continue
		}
		code := st[0]
		if (code == 'R' || code == 'C') && i+2 < len(toks) {
			files = append(files, changedFile{Status: st, Orig: toks[i+1], Path: toks[i+2]})
			i += 2
		} else if i+1 < len(toks) {
			files = append(files, changedFile{Status: st, Path: toks[i+1]})
			i++
		}
	}
	return files
}

// parseTrack interprets the %(upstream:track) field: "[ahead 1, behind 2]",
// "[ahead 3]", "[behind 4]", "[gone]" or "". It returns (ahead, behind, gone).
func parseTrack(s string) (ahead, behind int, gone bool) {
	s = strings.Trim(strings.TrimSpace(s), "[]")
	if s == "" {
		return 0, 0, false
	}
	if s == "gone" {
		return 0, 0, true
	}
	for _, part := range strings.Split(s, ",") {
		fields := strings.Fields(part)
		if len(fields) != 2 {
			continue
		}
		n, _ := strconv.Atoi(fields[1])
		switch fields[0] {
		case "ahead":
			ahead = n
		case "behind":
			behind = n
		}
	}
	return ahead, behind, gone
}

// atoiDefault does a tolerant Atoi (default on error/empty).
func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}
