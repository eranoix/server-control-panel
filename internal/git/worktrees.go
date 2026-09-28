package git

import (
	"net/http"
	"path/filepath"
	"regexp"
	"strings"

	"server-control-panel/internal/httpx"
)

var worktreeNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

type worktreeInfo struct {
	Path     string `json:"path"`
	Head     string `json:"head"`
	Branch   string `json:"branch"`
	Detached bool   `json:"detached"`
	Bare     bool   `json:"bare"`
	Locked   bool   `json:"locked"`
	Current  bool   `json:"current"`
}

func (s *svc) handleWorktrees(w http.ResponseWriter, r *http.Request) {
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
	res, err := run(r.Context(), repo.Path, "worktree", "list", "--porcelain")
	if err != nil || res.Code != 0 {
		httpx.WriteErr(w, http.StatusInternalServerError, "git worktree list failed")
		return
	}
	out := parseWorktrees(res.Stdout)
	self := filepath.Clean(repo.Path)
	for i := range out {
		if filepath.Clean(out[i].Path) == self {
			out[i].Current = true
		}
	}
	httpx.WriteJSON(w, map[string]any{"worktrees": out})
}

func parseWorktrees(out string) []worktreeInfo {
	res := []worktreeInfo{}
	var cur *worktreeInfo
	flush := func() {
		if cur != nil {
			res = append(res, *cur)
			cur = nil
		}
	}
	for _, ln := range strings.Split(out, "\n") {
		if ln == "" {
			flush()
			continue
		}
		key, val, _ := strings.Cut(ln, " ")
		switch key {
		case "worktree":
			flush()
			cur = &worktreeInfo{Path: val}
		case "HEAD":
			if cur != nil {
				cur.Head = shortHash(val)
			}
		case "branch":
			if cur != nil {
				cur.Branch = strings.TrimPrefix(val, "refs/heads/")
			}
		case "detached":
			if cur != nil {
				cur.Detached = true
			}
		case "bare":
			if cur != nil {
				cur.Bare = true
			}
		case "locked":
			if cur != nil {
				cur.Locked = true
			}
		}
	}
	flush()
	return res
}

func (s *svc) handleWorktreeAdd(w http.ResponseWriter, r *http.Request) {
	caller, repo, ok := s.writeGate(w, r)
	if !ok {
		return
	}
	var body struct {
		Name         string `json:"name"`
		Branch       string `json:"branch"`
		CreateBranch bool   `json:"create_branch"`
		Base         string `json:"base"`
	}
	if err := decodeBody(r, &body); err != nil {
		httpx.WriteErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	if !worktreeNameRe.MatchString(body.Name) {
		httpx.WriteErr(w, http.StatusBadRequest, "invalid name (use letters/digits/._-)")
		return
	}
	if !validRef(body.Branch) {
		httpx.WriteErr(w, http.StatusBadRequest, "invalid branch")
		return
	}
	if body.Base != "" && !validRef(body.Base) {
		httpx.WriteErr(w, http.StatusBadRequest, "invalid base")
		return
	}
	wtPath := filepath.Join(repo.Path, ".claude", "worktrees", body.Name)
	var args []string
	if body.CreateBranch {
		args = []string{"worktree", "add", "-b", body.Branch, wtPath}
		if body.Base != "" {
			args = append(args, body.Base)
		}
	} else {
		args = []string{"worktree", "add", wtPath, body.Branch}
	}
	err := withRepoWriteLock(repo.Path, func() error {
		res, e := run(r.Context(), repo.Path, args...)
		if e != nil {
			return e
		}
		if res.Code != 0 {
			return &gitError{res.Stderr}
		}
		return nil
	})
	if translateLockErr(w, err) {
		return
	}
	if err != nil {
		httpx.WriteErr(w, http.StatusBadRequest, gitMsg(err, "creating the worktree failed"))
		return
	}
	httpx.AuditEvent(s.audit, r, caller, "git.worktree_add", repo.ID+":"+wtPath+" ("+body.Branch+")")
	httpx.WriteJSON(w, map[string]any{"ok": true, "path": wtPath, "branch": body.Branch})
}

func (s *svc) handleWorktreeRemove(w http.ResponseWriter, r *http.Request) {
	caller, repo, ok := s.writeGate(w, r)
	if !ok {
		return
	}
	var body struct {
		Path  string `json:"path"`
		Force bool   `json:"force"`
	}
	if err := decodeBody(r, &body); err != nil {
		httpx.WriteErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	listed := run2List(r, repo.Path)
	target := filepath.Clean(body.Path)
	found := false
	for _, wt := range listed {
		if filepath.Clean(wt.Path) == target {
			found = true
			if wt.Current {
				httpx.WriteErr(w, http.StatusBadRequest, "cannot remove the current worktree")
				return
			}
			break
		}
	}
	if !found {
		httpx.WriteErr(w, http.StatusBadRequest, "path is not a worktree of this repo")
		return
	}
	args := []string{"worktree", "remove", target}
	if body.Force {
		args = []string{"worktree", "remove", "--force", target}
	}
	err := withRepoWriteLock(repo.Path, func() error {
		res, e := run(r.Context(), repo.Path, args...)
		if e != nil {
			return e
		}
		if res.Code != 0 {
			return &gitError{res.Stderr}
		}
		return nil
	})
	if translateLockErr(w, err) {
		return
	}
	if err != nil {
		httpx.WriteErr(w, http.StatusBadRequest, gitMsg(err, "removing the worktree failed"))
		return
	}
	httpx.AuditEvent(s.audit, r, caller, "git.worktree_remove", repo.ID+":"+target)
	httpx.WriteJSON(w, map[string]any{"ok": true})
}

func run2List(r *http.Request, repoPath string) []worktreeInfo {
	res, err := run(r.Context(), repoPath, "worktree", "list", "--porcelain")
	if err != nil || res.Code != 0 {
		return nil
	}
	out := parseWorktrees(res.Stdout)
	self := filepath.Clean(repoPath)
	for i := range out {
		if filepath.Clean(out[i].Path) == self {
			out[i].Current = true
		}
	}
	return out
}
