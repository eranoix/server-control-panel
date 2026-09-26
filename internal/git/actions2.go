package git

import (
	"net/http"
	"os"
	"strconv"
	"strings"

	"server-control-panel/internal/httpx"
)

// actions2.go — advanced operations: reflog/undo, interactive rebase (driven
// without an editor), conflict resolution and cherry-pick onto
// (drag-and-drop). All write-gated (POST + primary + writable repo) and under
// withRepoWriteLock — they never leave the index/rebase hanging for another session.

// ---- GET /reflog ----

type reflogEntry struct {
	Sel     string `json:"sel"`   // HEAD@{N}
	Short   string `json:"short"` // short hash
	Action  string `json:"action"`
	Subject string `json:"subject"`
}

func (s *svc) handleReflog(w http.ResponseWriter, r *http.Request) {
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
	limit := clampLimit(atoiDefault(r.URL.Query().Get("limit"), 100), 100)
	res, err := run(r.Context(), repo.Path, "reflog",
		"--max-count="+strconv.Itoa(limit),
		"--format=%gd%x00%h%x00%gs")
	if err != nil {
		httpx.WriteErr(w, http.StatusInternalServerError, "git reflog failed")
		return
	}
	out := []reflogEntry{}
	for _, ln := range splitLines(res.Stdout) {
		f := strings.Split(ln, "\x00")
		if len(f) < 3 {
			continue
		}
		action, subject := f[2], ""
		if i := strings.Index(f[2], ": "); i >= 0 {
			action, subject = f[2][:i], f[2][i+2:]
		}
		out = append(out, reflogEntry{Sel: f[0], Short: f[1], Action: action, Subject: subject})
	}
	httpx.WriteJSON(w, map[string]any{"reflog": out})
}

// ---- POST /reflog/reset (undo: reset to a reflog entry) ----

func (s *svc) handleReflogReset(w http.ResponseWriter, r *http.Request) {
	caller, repo, ok := s.writeGate(w, r)
	if !ok {
		return
	}
	var body struct {
		Index int    `json:"index"` // HEAD@{Index}
		Mode  string `json:"mode"`  // soft|mixed|hard
	}
	if err := decodeBody(r, &body); err != nil {
		httpx.WriteErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	if body.Index < 0 || body.Index > 1000 {
		httpx.WriteErr(w, http.StatusBadRequest, "invalid index")
		return
	}
	mode := "--mixed"
	switch body.Mode {
	case "soft":
		mode = "--soft"
	case "hard":
		mode = "--hard"
	case "mixed", "":
		mode = "--mixed"
	default:
		httpx.WriteErr(w, http.StatusBadRequest, "invalid mode")
		return
	}
	ref := "HEAD@{" + strconv.Itoa(body.Index) + "}"
	err := withRepoWriteLock(repo.Path, func() error {
		res, e := run(r.Context(), repo.Path, "reset", mode, ref)
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
		httpx.WriteErr(w, http.StatusBadRequest, gitMsg(err, "reset failed"))
		return
	}
	httpx.AuditEvent(s.audit, r, caller, "git.reflog_reset", repo.ID+" "+ref+" "+mode)
	httpx.WriteJSON(w, map[string]any{"ok": true, "ref": ref, "mode": mode})
}

// ---- GET /rebase/todo?base= (commits base..HEAD in application order) ----

func (s *svc) handleRebaseTodo(w http.ResponseWriter, r *http.Request) {
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
	base := r.URL.Query().Get("base")
	if !validRef(base) {
		httpx.WriteErr(w, http.StatusBadRequest, "invalid base")
		return
	}
	res, err := run(r.Context(), repo.Path, "log", "--reverse",
		"--format=%H%x00%h%x00%s", base+"..HEAD")
	if err != nil || res.Code != 0 {
		httpx.WriteErr(w, http.StatusUnprocessableEntity, "could not list commits (is the base valid?)")
		return
	}
	type todoCommit struct {
		Hash    string `json:"hash"`
		Short   string `json:"short"`
		Subject string `json:"subject"`
	}
	out := []todoCommit{}
	for _, ln := range splitLines(res.Stdout) {
		f := strings.Split(ln, "\x00")
		if len(f) < 3 {
			continue
		}
		out = append(out, todoCommit{Hash: f[0], Short: f[1], Subject: f[2]})
	}
	httpx.WriteJSON(w, map[string]any{"base": base, "commits": out})
}

// ---- POST /rebase/interactive ----

// rebaseStep is one line of the interactive rebase todo. Reorder = array order.
type rebaseStep struct {
	Action string `json:"action"` // pick|squash|fixup|drop
	Hash   string `json:"hash"`
}

var rebaseActions = map[string]bool{"pick": true, "squash": true, "fixup": true, "drop": true}

// handleRebaseInteractive drives `git rebase -i <base>` WITHOUT opening an
// editor: GIT_SEQUENCE_EDITOR copies our todo over the generated one, and
// GIT_EDITOR=true accepts squash messages as they are (combined). It supports
// reordering + pick/squash/fixup/drop. (reword is left out: use Amend.) On a
// conflict it leaves the rebase in progress for resolution through /conflicts (or /rebase/abort).
func (s *svc) handleRebaseInteractive(w http.ResponseWriter, r *http.Request) {
	caller, repo, ok := s.writeGate(w, r)
	if !ok {
		return
	}
	var body struct {
		Base string       `json:"base"`
		Todo []rebaseStep `json:"todo"`
	}
	if err := decodeBody(r, &body); err != nil {
		httpx.WriteErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	if !validRef(body.Base) {
		httpx.WriteErr(w, http.StatusBadRequest, "invalid base")
		return
	}
	if len(body.Todo) == 0 || len(body.Todo) > 1000 {
		httpx.WriteErr(w, http.StatusBadRequest, "todo is empty or too large")
		return
	}
	var lines []string
	picks := 0
	for _, st := range body.Todo {
		if !rebaseActions[st.Action] {
			httpx.WriteErr(w, http.StatusBadRequest, "invalid rebase action: "+st.Action)
			return
		}
		if !validRef(st.Hash) {
			httpx.WriteErr(w, http.StatusBadRequest, "invalid hash in the todo")
			return
		}
		if st.Action == "drop" {
			lines = append(lines, "drop "+st.Hash)
			continue
		}
		if st.Action == "pick" {
			picks++
		}
		lines = append(lines, st.Action+" "+st.Hash)
	}
	if picks == 0 {
		httpx.WriteErr(w, http.StatusBadRequest, "the todo needs at least one pick")
		return
	}
	todo := strings.Join(lines, "\n") + "\n"

	tmp, terr := os.CreateTemp("", "vpsm-rebase-*.txt")
	if terr != nil {
		httpx.WriteErr(w, http.StatusInternalServerError, "failed to prepare the rebase")
		return
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, werr := tmp.WriteString(todo); werr != nil {
		tmp.Close()
		httpx.WriteErr(w, http.StatusInternalServerError, "failed to write the todo")
		return
	}
	tmp.Close()

	env := []string{
		"GIT_SEQUENCE_EDITOR=cp " + tmpPath, // git runs: cp <tmp> <todofile>
		"GIT_EDITOR=true",                   // does not open a message editor
	}
	var conflict bool
	err := withRepoWriteLock(repo.Path, func() error {
		res, e := runEnv(r.Context(), repo.Path, env, "rebase", "-i", body.Base)
		if e != nil {
			return e
		}
		if res.Code != 0 {
			conflict = true
			return &gitError{res.Stderr + res.Stdout}
		}
		return nil
	})
	if translateLockErr(w, err) {
		return
	}
	if err != nil {
		httpx.WriteJSON(w, map[string]any{"ok": false, "conflict": conflict, "error": gitMsg(err, "rebase failed")})
		return
	}
	httpx.AuditEvent(s.audit, r, caller, "git.rebase_interactive", repo.ID+" onto "+body.Base)
	httpx.WriteJSON(w, map[string]any{"ok": true})
}

// ---- POST /rebase/abort e /rebase/continue ----

func (s *svc) handleRebaseAbort(w http.ResponseWriter, r *http.Request) {
	s.rebaseControl(w, r, "--abort", "git.rebase_abort")
}
func (s *svc) handleRebaseContinue(w http.ResponseWriter, r *http.Request) {
	s.rebaseControl(w, r, "--continue", "git.rebase_continue")
}

func (s *svc) rebaseControl(w http.ResponseWriter, r *http.Request, flag, action string) {
	caller, repo, ok := s.writeGate(w, r)
	if !ok {
		return
	}
	env := []string{"GIT_EDITOR=true"}
	var conflict bool
	err := withRepoWriteLock(repo.Path, func() error {
		res, e := runEnv(r.Context(), repo.Path, env, "rebase", flag)
		if e != nil {
			return e
		}
		if res.Code != 0 {
			conflict = flag == "--continue"
			return &gitError{res.Stderr + res.Stdout}
		}
		return nil
	})
	if translateLockErr(w, err) {
		return
	}
	if err != nil {
		httpx.WriteJSON(w, map[string]any{"ok": false, "conflict": conflict, "error": gitMsg(err, "rebase failed")})
		return
	}
	httpx.AuditEvent(s.audit, r, caller, action, repo.ID)
	httpx.WriteJSON(w, map[string]any{"ok": true})
}

// ---- GET /conflicts e /conflict/sides ----

func (s *svc) handleConflicts(w http.ResponseWriter, r *http.Request) {
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
	// diff --name-only --diff-filter=U lists the files in conflict.
	res, _ := run(r.Context(), repo.Path, "diff", "--name-only", "--diff-filter=U", "-z")
	files := []string{}
	for _, p := range strings.Split(res.Stdout, "\x00") {
		if p != "" {
			files = append(files, p)
		}
	}
	// Detects a rebase/merge in progress (so the UI can show continue/abort).
	gitDir, _ := resolveGitDir(repo.Path)
	rebasing := false
	merging := false
	if gitDir != "" {
		for _, d := range []string{"rebase-merge", "rebase-apply"} {
			if _, e := os.Stat(gitDir + "/" + d); e == nil {
				rebasing = true
			}
		}
		if _, e := os.Stat(gitDir + "/MERGE_HEAD"); e == nil {
			merging = true
		}
	}
	httpx.WriteJSON(w, map[string]any{"files": files, "rebasing": rebasing, "merging": merging})
}

func (s *svc) handleConflictSides(w http.ResponseWriter, r *http.Request) {
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
	path := r.URL.Query().Get("path")
	if !validRelPath(path) {
		httpx.WriteErr(w, http.StatusBadRequest, "invalid path")
		return
	}
	side := func(stage string) string {
		res, err := run(r.Context(), repo.Path, "show", stage+":"+path)
		if err != nil || res.Code != 0 {
			return ""
		}
		return res.Stdout
	}
	httpx.WriteJSON(w, map[string]any{
		"path":   path,
		"base":   side(":1"),
		"ours":   side(":2"),
		"theirs": side(":3"),
	})
}

// ---- POST /conflict/resolve ----

func (s *svc) handleConflictResolve(w http.ResponseWriter, r *http.Request) {
	caller, repo, ok := s.writeGate(w, r)
	if !ok {
		return
	}
	var body struct {
		Path     string `json:"path"`
		Strategy string `json:"strategy"` // ours|theirs|mark
		Content  string `json:"content"`  // used when strategy=content
	}
	if err := decodeBody(r, &body); err != nil {
		httpx.WriteErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	if !validRelPath(body.Path) {
		httpx.WriteErr(w, http.StatusBadRequest, "invalid path")
		return
	}
	err := withRepoWriteLock(repo.Path, func() error {
		switch body.Strategy {
		case "ours", "theirs":
			res, e := run(r.Context(), repo.Path, "checkout", "--"+body.Strategy, "--", body.Path)
			if e != nil {
				return e
			}
			if res.Code != 0 {
				return &gitError{res.Stderr}
			}
		case "content":
			abs, je := jailPath(repo.Path, body.Path)
			if je != nil {
				return je
			}
			if e := os.WriteFile(abs, []byte(body.Content), 0o644); e != nil {
				return e
			}
		case "mark":
			// nothing beyond the add below (the user edited and removed the markers)
		default:
			return &gitError{"invalid strategy"}
		}
		res, e := run(r.Context(), repo.Path, "add", "--", body.Path)
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
		httpx.WriteErr(w, http.StatusBadRequest, gitMsg(err, "resolution failed"))
		return
	}
	httpx.AuditEvent(s.audit, r, caller, "git.conflict_resolve", repo.ID+":"+body.Path+" ("+body.Strategy+")")
	httpx.WriteJSON(w, map[string]any{"ok": true})
}

// ---- POST /cherry-pick-onto (drag commit → branch) ----

// handleCherryPickOnto checks out the target branch and cherry-picks the
// commit — the semantics of "dragging a commit onto a branch" (GitKraken). On
// a conflict it leaves it for resolution through /conflicts. It does not use -f on the checkout (it fails cleanly when dirty).
func (s *svc) handleCherryPickOnto(w http.ResponseWriter, r *http.Request) {
	caller, repo, ok := s.writeGate(w, r)
	if !ok {
		return
	}
	var body struct {
		Hash   string `json:"hash"`
		Branch string `json:"branch"`
	}
	if err := decodeBody(r, &body); err != nil {
		httpx.WriteErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	if !validRef(body.Hash) || !validRef(body.Branch) {
		httpx.WriteErr(w, http.StatusBadRequest, "invalid hash/branch")
		return
	}
	var conflict bool
	err := withRepoWriteLock(repo.Path, func() error {
		if res, e := run(r.Context(), repo.Path, "checkout", body.Branch); e != nil || res.Code != 0 {
			if e != nil {
				return e
			}
			return &gitError{res.Stderr}
		}
		res, e := run(r.Context(), repo.Path, "cherry-pick", body.Hash)
		if e != nil {
			return e
		}
		if res.Code != 0 {
			conflict = true
			return &gitError{res.Stderr + res.Stdout}
		}
		return nil
	})
	if translateLockErr(w, err) {
		return
	}
	if err != nil {
		httpx.WriteJSON(w, map[string]any{"ok": false, "conflict": conflict, "error": gitMsg(err, "cherry-pick failed")})
		return
	}
	httpx.AuditEvent(s.audit, r, caller, "git.cherry_pick_onto", repo.ID+" "+body.Hash+" → "+body.Branch)
	httpx.WriteJSON(w, map[string]any{"ok": true, "branch": body.Branch})
}
