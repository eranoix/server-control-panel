package git

import (
	"net/http"
	"strconv"
	"strings"

	"server-control-panel/internal/config"
	"server-control-panel/internal/httpx"
)

// actionBody is the unified body of the write actions (each handler uses the
// subset it needs). It keeps decoding simple and uniform.
type actionBody struct {
	Hash             string `json:"hash"`
	Ref              string `json:"ref"`
	Name             string `json:"name"`
	NewName          string `json:"new_name"`
	Mode             string `json:"mode"`
	Remote           string `json:"remote"`
	Branch           string `json:"branch"`
	Message          string `json:"message"`
	Index            int    `json:"index"`
	NoCommit         bool   `json:"no_commit"`
	NoFF             bool   `json:"no_ff"`
	Force            bool   `json:"force"`
	SetUpstream      bool   `json:"set_upstream"`
	IncludeUntracked bool   `json:"include_untracked"`
	Prune            bool   `json:"prune"`
}

// runMutate runs a git action with lock + audit + a standardized response.
// extra is merged into the success response (e.g. the new hash).
func (s *svc) runMutate(w http.ResponseWriter, r *http.Request, repo config.GitRepo, caller, action string, args []string, extra map[string]any) {
	var out string
	err := withRepoWriteLock(repo.Path, func() error {
		res, e := run(r.Context(), repo.Path, args...)
		if e != nil {
			return e
		}
		out = strings.TrimSpace(res.Stdout + "\n" + res.Stderr)
		if res.Code != 0 {
			return &gitError{res.Stderr + res.Stdout}
		}
		return nil
	})
	if translateLockErr(w, err) {
		return
	}
	if err != nil {
		httpx.WriteErr(w, http.StatusBadRequest, gitMsg(err, action+" failed"))
		return
	}
	httpx.AuditEvent(s.audit, r, caller, "git."+action, repo.ID)
	resp := map[string]any{"ok": true, "output": out}
	for k, v := range extra {
		resp[k] = v
	}
	httpx.WriteJSON(w, resp)
}

// stashRef assembles a validated "stash@{N}".
func stashRef(n int) (string, bool) {
	if n < 0 || n > 9999 {
		return "", false
	}
	return "stash@{" + strconv.Itoa(n) + "}", true
}

// ---- tags ----

func (s *svc) handleTagCreate(w http.ResponseWriter, r *http.Request) {
	caller, repo, ok := s.writeGate(w, r)
	if !ok {
		return
	}
	var b actionBody
	if err := decodeBody(r, &b); err != nil || !validRef(b.Name) {
		httpx.WriteErr(w, http.StatusBadRequest, "invalid tag name")
		return
	}
	target := "HEAD"
	if b.Ref != "" {
		if !validRef(b.Ref) {
			httpx.WriteErr(w, http.StatusBadRequest, "invalid ref")
			return
		}
		target = b.Ref
	}
	args := []string{"tag", b.Name, target}
	if strings.TrimSpace(b.Message) != "" {
		args = []string{"tag", "-a", b.Name, "-m", b.Message, target}
	}
	s.runMutate(w, r, repo, caller, "tag_create", args, map[string]any{"tag": b.Name})
}

func (s *svc) handleTagDelete(w http.ResponseWriter, r *http.Request) {
	caller, repo, ok := s.writeGate(w, r)
	if !ok {
		return
	}
	var b actionBody
	if err := decodeBody(r, &b); err != nil || !validRef(b.Name) {
		httpx.WriteErr(w, http.StatusBadRequest, "invalid tag")
		return
	}
	s.runMutate(w, r, repo, caller, "tag_delete", []string{"tag", "-d", b.Name}, nil)
}

// ---- commit-level: cherry-pick / revert / merge / rebase / reset / drop ----

func (s *svc) handleCherryPick(w http.ResponseWriter, r *http.Request) {
	caller, repo, ok := s.writeGate(w, r)
	if !ok {
		return
	}
	var b actionBody
	if err := decodeBody(r, &b); err != nil || !validRef(b.Hash) {
		httpx.WriteErr(w, http.StatusBadRequest, "invalid hash")
		return
	}
	args := []string{"cherry-pick"}
	if b.NoCommit {
		args = append(args, "-n")
	}
	args = append(args, b.Hash)
	s.runMutate(w, r, repo, caller, "cherry_pick", args, nil)
}

func (s *svc) handleRevert(w http.ResponseWriter, r *http.Request) {
	caller, repo, ok := s.writeGate(w, r)
	if !ok {
		return
	}
	var b actionBody
	if err := decodeBody(r, &b); err != nil || !validRef(b.Hash) {
		httpx.WriteErr(w, http.StatusBadRequest, "invalid hash")
		return
	}
	args := []string{"revert"}
	if b.NoCommit {
		args = append(args, "-n")
	} else {
		args = append(args, "--no-edit")
	}
	args = append(args, b.Hash)
	s.runMutate(w, r, repo, caller, "revert", args, nil)
}

func (s *svc) handleMerge(w http.ResponseWriter, r *http.Request) {
	caller, repo, ok := s.writeGate(w, r)
	if !ok {
		return
	}
	var b actionBody
	if err := decodeBody(r, &b); err != nil || !validRef(b.Ref) {
		httpx.WriteErr(w, http.StatusBadRequest, "invalid ref")
		return
	}
	args := []string{"merge"}
	if b.NoFF {
		args = append(args, "--no-ff")
	}
	if b.NoCommit {
		args = append(args, "--no-commit")
	}
	args = append(args, b.Ref)
	s.runMutate(w, r, repo, caller, "merge", args, nil)
}

func (s *svc) handleRebase(w http.ResponseWriter, r *http.Request) {
	caller, repo, ok := s.writeGate(w, r)
	if !ok {
		return
	}
	var b actionBody
	if err := decodeBody(r, &b); err != nil || !validRef(b.Ref) {
		httpx.WriteErr(w, http.StatusBadRequest, "invalid ref")
		return
	}
	s.runMutate(w, r, repo, caller, "rebase", []string{"rebase", b.Ref}, nil)
}

func (s *svc) handleReset(w http.ResponseWriter, r *http.Request) {
	caller, repo, ok := s.writeGate(w, r)
	if !ok {
		return
	}
	var b actionBody
	if err := decodeBody(r, &b); err != nil || !validRef(b.Hash) {
		httpx.WriteErr(w, http.StatusBadRequest, "invalid hash")
		return
	}
	mode := b.Mode
	if mode != "soft" && mode != "mixed" && mode != "hard" {
		mode = "mixed"
	}
	s.runMutate(w, r, repo, caller, "reset", []string{"reset", "--" + mode, b.Hash}, nil)
}

func (s *svc) handleDrop(w http.ResponseWriter, r *http.Request) {
	caller, repo, ok := s.writeGate(w, r)
	if !ok {
		return
	}
	var b actionBody
	if err := decodeBody(r, &b); err != nil || !validRef(b.Hash) {
		httpx.WriteErr(w, http.StatusBadRequest, "invalid hash")
		return
	}
	// Removes a commit by rewriting history: rebase --onto <hash>^ <hash>.
	s.runMutate(w, r, repo, caller, "drop", []string{"rebase", "--onto", b.Hash + "^", b.Hash}, nil)
}

// ---- branch ----

func (s *svc) handleBranchRename(w http.ResponseWriter, r *http.Request) {
	caller, repo, ok := s.writeGate(w, r)
	if !ok {
		return
	}
	var b actionBody
	if err := decodeBody(r, &b); err != nil || !validRef(b.NewName) {
		httpx.WriteErr(w, http.StatusBadRequest, "invalid new name")
		return
	}
	args := []string{"branch", "-m"}
	if b.Name != "" {
		if !validRef(b.Name) {
			httpx.WriteErr(w, http.StatusBadRequest, "invalid branch")
			return
		}
		args = append(args, b.Name)
	}
	args = append(args, b.NewName)
	s.runMutate(w, r, repo, caller, "branch_rename", args, nil)
}

func (s *svc) handleBranchDelete(w http.ResponseWriter, r *http.Request) {
	caller, repo, ok := s.writeGate(w, r)
	if !ok {
		return
	}
	var b actionBody
	if err := decodeBody(r, &b); err != nil || !validRef(b.Name) {
		httpx.WriteErr(w, http.StatusBadRequest, "invalid branch")
		return
	}
	flag := "-d"
	if b.Force {
		flag = "-D"
	}
	s.runMutate(w, r, repo, caller, "branch_delete", []string{"branch", flag, b.Name}, nil)
}

// ---- stash ----

func (s *svc) handleStashPush(w http.ResponseWriter, r *http.Request) {
	caller, repo, ok := s.writeGate(w, r)
	if !ok {
		return
	}
	var b actionBody
	decodeBody(r, &b)
	args := []string{"stash", "push"}
	if b.IncludeUntracked {
		args = append(args, "-u")
	}
	if strings.TrimSpace(b.Message) != "" {
		args = append(args, "-m", b.Message)
	}
	s.runMutate(w, r, repo, caller, "stash_push", args, nil)
}

func (s *svc) handleStashApply(w http.ResponseWriter, r *http.Request) { s.stashOp(w, r, "apply") }
func (s *svc) handleStashPop(w http.ResponseWriter, r *http.Request)   { s.stashOp(w, r, "pop") }
func (s *svc) handleStashDrop(w http.ResponseWriter, r *http.Request)  { s.stashOp(w, r, "drop") }

func (s *svc) stashOp(w http.ResponseWriter, r *http.Request, op string) {
	caller, repo, ok := s.writeGate(w, r)
	if !ok {
		return
	}
	var b actionBody
	decodeBody(r, &b)
	ref, ok2 := stashRef(b.Index)
	if !ok2 {
		httpx.WriteErr(w, http.StatusBadRequest, "invalid stash index")
		return
	}
	s.runMutate(w, r, repo, caller, "stash_"+op, []string{"stash", op, ref}, nil)
}

func (s *svc) handleStashBranch(w http.ResponseWriter, r *http.Request) {
	caller, repo, ok := s.writeGate(w, r)
	if !ok {
		return
	}
	var b actionBody
	if err := decodeBody(r, &b); err != nil || !validRef(b.Name) {
		httpx.WriteErr(w, http.StatusBadRequest, "invalid branch name")
		return
	}
	ref, ok2 := stashRef(b.Index)
	if !ok2 {
		httpx.WriteErr(w, http.StatusBadRequest, "invalid stash index")
		return
	}
	s.runMutate(w, r, repo, caller, "stash_branch", []string{"stash", "branch", b.Name, ref}, nil)
}

// ---- uncommitted ----

func (s *svc) handleClean(w http.ResponseWriter, r *http.Request) {
	caller, repo, ok := s.writeGate(w, r)
	if !ok {
		return
	}
	s.runMutate(w, r, repo, caller, "clean", []string{"clean", "-fd"}, nil)
}

func (s *svc) handleResetUncommitted(w http.ResponseWriter, r *http.Request) {
	caller, repo, ok := s.writeGate(w, r)
	if !ok {
		return
	}
	s.runMutate(w, r, repo, caller, "reset_uncommitted", []string{"reset", "--hard", "HEAD"}, nil)
}

// ---- remotes: fetch / pull / push ----

// handleFetch: fetch is safe (it only updates tracking refs) and is allowed
// even on a read-only repo — it uses the plain gate, not the writeGate.
func (s *svc) handleFetch(w http.ResponseWriter, r *http.Request) {
	caller, ok := s.gate(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		httpx.WriteErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	repo, ok := s.repoParam(w, r)
	if !ok {
		return
	}
	var b actionBody
	decodeBody(r, &b)
	args := []string{"fetch"}
	if b.Prune {
		args = append(args, "--prune")
	}
	if b.Remote != "" && validRef(b.Remote) {
		args = append(args, b.Remote)
	} else {
		args = append(args, "--all")
	}
	s.runMutate(w, r, repo, caller, "fetch", args, nil)
}

func (s *svc) handlePull(w http.ResponseWriter, r *http.Request) {
	caller, repo, ok := s.writeGate(w, r)
	if !ok {
		return
	}
	var b actionBody
	decodeBody(r, &b)
	args := []string{"pull"}
	if b.Remote != "" && b.Branch != "" && validRef(b.Remote) && validRef(b.Branch) {
		args = append(args, b.Remote, b.Branch)
	}
	s.runMutate(w, r, repo, caller, "pull", args, nil)
}

func (s *svc) handlePush(w http.ResponseWriter, r *http.Request) {
	caller, repo, ok := s.writeGate(w, r)
	if !ok {
		return
	}
	var b actionBody
	decodeBody(r, &b)
	args := []string{"push"}
	if b.Force {
		args = append(args, "--force-with-lease")
	}
	if b.SetUpstream {
		args = append(args, "-u")
	}
	if b.Remote != "" && b.Branch != "" && validRef(b.Remote) && validRef(b.Branch) {
		args = append(args, b.Remote, b.Branch)
	}
	s.runMutate(w, r, repo, caller, "push", args, nil)
}
