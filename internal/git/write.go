package git

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"server-control-panel/internal/config"
	"server-control-panel/internal/httpx"
)

const maxFileBytes = 8 << 20

func jailPath(repoRoot, rel string) (string, error) {
	if !validRelPath(rel) {
		return "", os.ErrInvalid
	}
	root := filepath.Clean(repoRoot)
	abs := filepath.Clean(filepath.Join(root, rel))
	if abs != root && !strings.HasPrefix(abs, root+string(filepath.Separator)) {
		return "", os.ErrPermission
	}
	return abs, nil
}

var (
	errIsDir    = errors.New("is a directory")
	errTooLarge = errors.New("file too large for the editor")
	errBinary   = errors.New("binary file — not editable as text")
)

func readRepoFile(repoRoot, rel string) (string, error) {
	abs, err := jailPath(repoRoot, rel)
	if err != nil {
		return "", err
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if fi.IsDir() {
		return "", errIsDir
	}
	if fi.Size() > maxFileBytes {
		return "", errTooLarge
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	probe := b
	if len(probe) > 8192 {
		probe = probe[:8192]
	}
	if bytes.IndexByte(probe, 0) >= 0 {
		return "", errBinary
	}
	return string(b), nil
}

func (s *svc) writeGate(w http.ResponseWriter, r *http.Request) (string, config.GitRepo, bool) {
	if r.Method != http.MethodPost {
		httpx.WriteErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return "", config.GitRepo{}, false
	}
	caller, ok := s.gate(w, r)
	if !ok {
		return "", config.GitRepo{}, false
	}
	repo, ok := s.repoParam(w, r)
	if !ok {
		return "", config.GitRepo{}, false
	}
	if !canWrite(repo) {
		httpx.AuditEvent(s.audit, r, caller, "git.write_denied", repo.ID+" (read-only)")
		httpx.WriteErr(w, http.StatusForbidden, "repository is read-only")
		return "", config.GitRepo{}, false
	}
	return caller, repo, true
}

func decodeBody(r *http.Request, dst any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxFileBytes+1<<16))
	return dec.Decode(dst)
}

func translateLockErr(w http.ResponseWriter, err error) bool {
	if err == errLocked {
		httpx.WriteErr(w, http.StatusConflict, "repository is busy — try again")
		return true
	}
	return false
}

func (s *svc) handleWrite(w http.ResponseWriter, r *http.Request) {
	caller, repo, ok := s.writeGate(w, r)
	if !ok {
		return
	}
	var body struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := decodeBody(r, &body); err != nil {
		httpx.WriteErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	abs, err := jailPath(repo.Path, body.Path)
	if err != nil {
		httpx.WriteErr(w, http.StatusBadRequest, "invalid path")
		return
	}
	if len(body.Content) > maxFileBytes {
		httpx.WriteErr(w, http.StatusRequestEntityTooLarge, "file too large")
		return
	}
	err = withRepoWriteLock(repo.Path, func() error {
		if mkerr := os.MkdirAll(filepath.Dir(abs), 0o755); mkerr != nil {
			return mkerr
		}
		return os.WriteFile(abs, []byte(body.Content), 0o644)
	})
	if translateLockErr(w, err) {
		return
	}
	if err != nil {
		httpx.WriteErr(w, http.StatusInternalServerError, "failed to save")
		return
	}
	httpx.AuditEvent(s.audit, r, caller, "git.write", repo.ID+":"+body.Path)
	httpx.WriteJSON(w, map[string]any{"ok": true, "path": body.Path})
}

func (s *svc) handleStage(w http.ResponseWriter, r *http.Request)   { s.stageOp(w, r, true) }
func (s *svc) handleUnstage(w http.ResponseWriter, r *http.Request) { s.stageOp(w, r, false) }

func (s *svc) stageOp(w http.ResponseWriter, r *http.Request, stage bool) {
	caller, repo, ok := s.writeGate(w, r)
	if !ok {
		return
	}
	var body struct {
		Paths []string `json:"paths"`
	}
	if err := decodeBody(r, &body); err != nil {
		httpx.WriteErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	paths, perr := sanitizePaths(body.Paths)
	if perr != nil {
		httpx.WriteErr(w, http.StatusBadRequest, "invalid path")
		return
	}
	if len(paths) == 0 {
		httpx.WriteErr(w, http.StatusBadRequest, "no path given")
		return
	}
	var args []string
	action := "git.stage"
	if stage {
		args = append([]string{"add", "--"}, paths...)
	} else {
		args = append([]string{"restore", "--staged", "--"}, paths...)
		action = "git.unstage"
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
		httpx.WriteErr(w, http.StatusBadRequest, gitMsg(err, "stage failed"))
		return
	}
	httpx.AuditEvent(s.audit, r, caller, action, repo.ID)
	httpx.WriteJSON(w, map[string]any{"ok": true})
}

func (s *svc) handleCommit(w http.ResponseWriter, r *http.Request) {
	caller, repo, ok := s.writeGate(w, r)
	if !ok {
		return
	}
	var body struct {
		Message    string   `json:"message"`
		IdentityID string   `json:"identity_id"`
		Amend      bool     `json:"amend"`
		Signoff    bool     `json:"signoff"`
		CoAuthors  []string `json:"coauthors"`
	}
	if err := decodeBody(r, &body); err != nil {
		httpx.WriteErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	msg := strings.TrimSpace(body.Message)
	if msg == "" && !body.Amend {
		httpx.WriteErr(w, http.StatusBadRequest, "empty message")
		return
	}
	if len(body.CoAuthors) > 0 && msg != "" {
		var trailers []string
		for _, ca := range body.CoAuthors {
			ca = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(ca, "\n", " "), "\r", " "))
			if ca == "" || !strings.Contains(ca, "<") || !strings.Contains(ca, ">") {
				continue
			}
			trailers = append(trailers, "Co-authored-by: "+ca)
		}
		if len(trailers) > 0 {
			msg = msg + "\n\n" + strings.Join(trailers, "\n")
		}
	}

	name, email := repo.ExpName, repo.ExpEmail
	if body.IdentityID != "" {
		id, ok := resolveIdentity(s.cfg, body.IdentityID)
		if !ok {
			httpx.WriteErr(w, http.StatusBadRequest, "unknown identity")
			return
		}
		name, email = id.Name, id.Email
	}
	if email == "" || name == "" {
		httpx.WriteErr(w, http.StatusUnprocessableEntity, "no identity configured — pick an account")
		return
	}
	identityWarn := repo.ExpEmail != "" && !strings.EqualFold(email, repo.ExpEmail)

	var newHash string
	err := withRepoWriteLock(repo.Path, func() error {
		args := []string{"-c", "user.name=" + name, "-c", "user.email=" + email, "commit"}
		if body.Amend {
			args = append(args, "--amend", "--no-edit")
		}
		if body.Signoff {
			args = append(args, "-s")
		}
		if msg != "" {
			args = append(args, "-m", msg)
		}
		res, e := run(r.Context(), repo.Path, args...)
		if e != nil {
			return e
		}
		if res.Code != 0 {
			return &gitError{res.Stderr + res.Stdout}
		}
		if h, _ := run(r.Context(), repo.Path, "rev-parse", "--short", "HEAD"); h.Code == 0 {
			newHash = strings.TrimSpace(h.Stdout)
		}
		return nil
	})
	if translateLockErr(w, err) {
		return
	}
	if err != nil {
		httpx.WriteErr(w, http.StatusBadRequest, gitMsg(err, "commit failed"))
		return
	}
	signed := formatIdentity(name, email)
	action := "git.commit"
	if identityWarn {
		action = "git.commit_identity_override"
	}
	httpx.AuditEvent(s.audit, r, caller, action, repo.ID+" "+newHash+" as "+signed)
	httpx.WriteJSON(w, map[string]any{
		"ok": true, "hash": newHash, "signed_as": signed, "identity_warn": identityWarn,
	})
}

func (s *svc) handleBranchCreate(w http.ResponseWriter, r *http.Request) {
	caller, repo, ok := s.writeGate(w, r)
	if !ok {
		return
	}
	var body struct {
		Name     string `json:"name"`
		Checkout bool   `json:"checkout"`
	}
	if err := decodeBody(r, &body); err != nil {
		httpx.WriteErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	if !validRef(body.Name) {
		httpx.WriteErr(w, http.StatusBadRequest, "invalid branch name")
		return
	}
	args := []string{"branch", "--", body.Name}
	if body.Checkout {
		args = []string{"checkout", "-b", body.Name}
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
		httpx.WriteErr(w, http.StatusBadRequest, gitMsg(err, "creating the branch failed"))
		return
	}
	httpx.AuditEvent(s.audit, r, caller, "git.branch_create", repo.ID+":"+body.Name)
	httpx.WriteJSON(w, map[string]any{"ok": true, "branch": body.Name, "checked_out": body.Checkout})
}

func (s *svc) handleCheckout(w http.ResponseWriter, r *http.Request) {
	caller, repo, ok := s.writeGate(w, r)
	if !ok {
		return
	}
	var body struct {
		Ref string `json:"ref"`
	}
	if err := decodeBody(r, &body); err != nil {
		httpx.WriteErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	if !validRef(body.Ref) {
		httpx.WriteErr(w, http.StatusBadRequest, "invalid ref")
		return
	}
	err := withRepoWriteLock(repo.Path, func() error {
		res, e := run(r.Context(), repo.Path, "checkout", body.Ref)
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
		httpx.WriteErr(w, http.StatusBadRequest, gitMsg(err, "checkout failed"))
		return
	}
	httpx.AuditEvent(s.audit, r, caller, "git.checkout", repo.ID+":"+body.Ref)
	httpx.WriteJSON(w, map[string]any{"ok": true, "ref": body.Ref})
}

func (s *svc) handleDiscard(w http.ResponseWriter, r *http.Request) {
	caller, repo, ok := s.writeGate(w, r)
	if !ok {
		return
	}
	var body struct {
		Paths []string `json:"paths"`
	}
	if err := decodeBody(r, &body); err != nil {
		httpx.WriteErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	paths, perr := sanitizePaths(body.Paths)
	if perr != nil || len(paths) == 0 {
		httpx.WriteErr(w, http.StatusBadRequest, "invalid path")
		return
	}
	err := withRepoWriteLock(repo.Path, func() error {
		res, e := run(r.Context(), repo.Path, append([]string{"restore", "--"}, paths...)...)
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
		httpx.WriteErr(w, http.StatusBadRequest, gitMsg(err, "discard failed"))
		return
	}
	httpx.AuditEvent(s.audit, r, caller, "git.discard", repo.ID)
	httpx.WriteJSON(w, map[string]any{"ok": true})
}

func (s *svc) handleApply(w http.ResponseWriter, r *http.Request) {
	caller, repo, ok := s.writeGate(w, r)
	if !ok {
		return
	}
	var body struct {
		Patch   string `json:"patch"`
		Reverse bool   `json:"reverse"`
	}
	if err := decodeBody(r, &body); err != nil {
		httpx.WriteErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	if strings.TrimSpace(body.Patch) == "" {
		httpx.WriteErr(w, http.StatusBadRequest, "empty patch")
		return
	}
	if len(body.Patch) > maxFileBytes {
		httpx.WriteErr(w, http.StatusRequestEntityTooLarge, "patch too large")
		return
	}
	args := []string{"apply", "--cached", "--recount", "--whitespace=nowarn"}
	if body.Reverse {
		args = append(args, "--reverse")
	}
	patch := strings.ReplaceAll(body.Patch, "\r\n", "\n")
	if !strings.HasSuffix(patch, "\n") {
		patch += "\n"
	}
	err := withRepoWriteLock(repo.Path, func() error {
		res, e := runStdin(r.Context(), repo.Path, patch, args...)
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
		httpx.WriteErr(w, http.StatusUnprocessableEntity, gitMsg(err, "git apply failed"))
		return
	}
	action := "git.stage_hunk"
	if body.Reverse {
		action = "git.unstage_hunk"
	}
	httpx.AuditEvent(s.audit, r, caller, action, repo.ID)
	httpx.WriteJSON(w, map[string]any{"ok": true})
}

func sanitizePaths(in []string) ([]string, error) {
	out := make([]string, 0, len(in))
	for _, p := range in {
		if !validRelPath(p) {
			return nil, os.ErrInvalid
		}
		out = append(out, p)
	}
	return out, nil
}

type gitError struct{ stderr string }

func (e *gitError) Error() string { return e.stderr }

func gitMsg(err error, fallback string) string {
	ge, ok := err.(*gitError)
	if !ok {
		return fallback
	}
	for _, ln := range strings.Split(ge.stderr, "\n") {
		ln = strings.TrimSpace(ln)
		if ln != "" {
			if len(ln) > 200 {
				ln = ln[:200]
			}
			return ln
		}
	}
	return fallback
}
