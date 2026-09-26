package git

import (
	"net/http"
	"strconv"
	"strings"

	"server-control-panel/internal/httpx"
)

// actions3.go — branches/tags/stashes/submodules.

// ---- POST /remote/prune (removes remote refs that are gone) ----

func (s *svc) handleRemotePrune(w http.ResponseWriter, r *http.Request) {
	caller, repo, ok := s.writeGate(w, r)
	if !ok {
		return
	}
	var body struct {
		Remote string `json:"remote"`
	}
	_ = decodeBody(r, &body)
	remote := body.Remote
	if remote == "" {
		remote = "origin"
	}
	if !validRef(remote) {
		httpx.WriteErr(w, http.StatusBadRequest, "invalid remote")
		return
	}
	err := withRepoWriteLock(repo.Path, func() error {
		res, e := run(r.Context(), repo.Path, "remote", "prune", remote)
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
		httpx.WriteErr(w, http.StatusBadRequest, gitMsg(err, "prune failed"))
		return
	}
	httpx.AuditEvent(s.audit, r, caller, "git.remote_prune", repo.ID+":"+remote)
	httpx.WriteJSON(w, map[string]any{"ok": true})
}

// ---- POST /tag/push (pushes one tag to the remote) ----

func (s *svc) handleTagPush(w http.ResponseWriter, r *http.Request) {
	caller, repo, ok := s.writeGate(w, r)
	if !ok {
		return
	}
	var body struct {
		Name   string `json:"name"`
		Remote string `json:"remote"`
	}
	if err := decodeBody(r, &body); err != nil {
		httpx.WriteErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	if !validRef(body.Name) {
		httpx.WriteErr(w, http.StatusBadRequest, "invalid tag")
		return
	}
	remote := body.Remote
	if remote == "" {
		remote = "origin"
	}
	if !validRef(remote) {
		httpx.WriteErr(w, http.StatusBadRequest, "invalid remote")
		return
	}
	err := withRepoWriteLock(repo.Path, func() error {
		res, e := run(r.Context(), repo.Path, "push", remote, "refs/tags/"+body.Name)
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
		httpx.WriteErr(w, http.StatusBadRequest, gitMsg(err, "tag push failed"))
		return
	}
	httpx.AuditEvent(s.audit, r, caller, "git.tag_push", repo.ID+":"+body.Name+"→"+remote)
	httpx.WriteJSON(w, map[string]any{"ok": true})
}

// ---- GET /stash/show?index= (preview of a stash's content) ----

func (s *svc) handleStashShow(w http.ResponseWriter, r *http.Request) {
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
	idx := atoiDefault(r.URL.Query().Get("index"), 0)
	if idx < 0 || idx > 1000 {
		httpx.WriteErr(w, http.StatusBadRequest, "invalid index")
		return
	}
	ref := "stash@{" + strconv.Itoa(idx) + "}"
	res, err := run(r.Context(), repo.Path, "stash", "show", "-p", ref)
	if err != nil {
		httpx.WriteErr(w, http.StatusInternalServerError, "git stash show failed")
		return
	}
	httpx.WriteJSON(w, map[string]any{"index": idx, "diff": res.Stdout})
}

// ---- GET /patch?hash= (generates a commit's .patch: format-patch -1 --stdout) ----

func (s *svc) handlePatch(w http.ResponseWriter, r *http.Request) {
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
	res, err := run(r.Context(), repo.Path, "format-patch", "-1", "--stdout", hash)
	if err != nil || res.Code != 0 {
		httpx.WriteErr(w, http.StatusUnprocessableEntity, "could not generate the patch")
		return
	}
	httpx.WriteJSON(w, map[string]any{"hash": hash, "patch": res.Stdout})
}

// ---- GET /submodules e POST /submodule/update ----

type submoduleInfo struct {
	Path   string `json:"path"`
	Hash   string `json:"hash"`
	Status string `json:"status"` // " "=ok, "-"=not initialized, "+"=out of sync, "U"=conflict
}

func (s *svc) handleSubmodules(w http.ResponseWriter, r *http.Request) {
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
	res, err := run(r.Context(), repo.Path, "submodule", "status")
	out := []submoduleInfo{}
	if err == nil && res.Code == 0 {
		for _, ln := range splitLines(res.Stdout) {
			if ln == "" {
				continue
			}
			// format: "<status><sha> <path> (<desc>)" — status is the 1st char
			st := string(ln[0])
			rest := strings.TrimSpace(ln[1:])
			fields := strings.Fields(rest)
			if len(fields) < 2 {
				continue
			}
			out = append(out, submoduleInfo{Status: strings.TrimSpace(st), Hash: shortHash(fields[0]), Path: fields[1]})
		}
	}
	httpx.WriteJSON(w, map[string]any{"submodules": out})
}

func (s *svc) handleSubmoduleUpdate(w http.ResponseWriter, r *http.Request) {
	caller, repo, ok := s.writeGate(w, r)
	if !ok {
		return
	}
	err := withRepoWriteLock(repo.Path, func() error {
		res, e := run(r.Context(), repo.Path, "submodule", "update", "--init", "--recursive")
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
		httpx.WriteErr(w, http.StatusBadRequest, gitMsg(err, "submodule update failed"))
		return
	}
	httpx.AuditEvent(s.audit, r, caller, "git.submodule_update", repo.ID)
	httpx.WriteJSON(w, map[string]any{"ok": true})
}
