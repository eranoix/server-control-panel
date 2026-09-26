package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/deploy"
)

// handlers_deploy.go — the Heroku-style PaaS API. ALL routes are primary-only:
// a deploy runs `docker compose`/nginx as root and the repo/env are arbitrary
// paths — the same RCE surface as handleComposeAction.
//
// Each deploy's log is served by polling the persisted file
// (<DataDir>/deploy/<app>/<id>.log), which BOTH the push AND the queue runner
// write — so the UI follows any deploy the same way.

func (r *Router) deployStoreOrNil(w http.ResponseWriter) *deploy.Store {
	if r.deployStore == nil {
		writeErr(w, 503, "deploy subsystem unavailable")
		return nil
	}
	return r.deployStore
}

// GET /api/deploy/apps → the list of apps (history included, pruned at 30).
func (r *Router) handleDeployApps(w http.ResponseWriter, req *http.Request) {
	if _, ok := r.mustPrimary(w, req); !ok {
		return
	}
	st := r.deployStoreOrNil(w)
	if st == nil {
		return
	}
	switch req.Method {
	case http.MethodGet:
		apps, err := st.List()
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		if apps == nil {
			apps = []deploy.App{}
		}
		writeJSON(w, map[string]any{"apps": apps, "repo_root": deploy.AppsRoot})
	case http.MethodPost:
		var body struct {
			Name        string `json:"name"`
			Port        int    `json:"port"`
			Domain      string `json:"domain"`
			Branch      string `json:"branch"`
			ComposeFile string `json:"compose_file"`
			Autodeploy  *bool  `json:"autodeploy"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			writeErr(w, 400, "bad json")
			return
		}
		auto := true
		if body.Autodeploy != nil {
			auto = *body.Autodeploy
		}
		app, err := st.Create(deploy.App{
			Name: body.Name, Port: body.Port, Domain: body.Domain,
			Branch: body.Branch, ComposeFile: body.ComposeFile, Autodeploy: auto,
		})
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		r.auditEvent(req, auth.UserFrom(req), "deploy.app.create", app.Name)
		writeJSON(w, map[string]any{"app": app, "repo_path": deploy.RepoPath(app.Name)})
	default:
		writeErr(w, 405, "method not allowed")
	}
}

// GET /api/deploy/app?name=<n> → one app's detail.
func (r *Router) handleDeployApp(w http.ResponseWriter, req *http.Request) {
	if _, ok := r.mustPrimary(w, req); !ok {
		return
	}
	st := r.deployStoreOrNil(w)
	if st == nil {
		return
	}
	name := req.URL.Query().Get("name")
	app, ok, err := st.Get(name)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if !ok {
		writeErr(w, 404, "app does not exist")
		return
	}
	writeJSON(w, map[string]any{"app": app, "repo_path": deploy.RepoPath(app.Name)})
}

// POST /api/deploy/app/deploy {name,ref?,commit?} → enqueues app_deploy.
func (r *Router) handleDeployTrigger(w http.ResponseWriter, req *http.Request) {
	user, ok := r.mustPrimary(w, req)
	if !ok {
		return
	}
	st := r.deployStoreOrNil(w)
	if st == nil {
		return
	}
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	var body struct {
		Name   string `json:"name"`
		Ref    string `json:"ref"`
		Commit string `json:"commit"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	app, found, err := st.Get(body.Name)
	if err != nil || !found {
		writeErr(w, 404, "app does not exist")
		return
	}
	id := deploy.NewID()
	r.enqueueDeploy(w, req, user, app.Name, deploy.Spec{
		App: app.Name, Ref: body.Ref, Commit: body.Commit, Trigger: "ui", DeployID: id,
	})
}

// POST /api/deploy/app/rollback {name,to?} → enqueues a deploy of the previous commit.
func (r *Router) handleDeployRollback(w http.ResponseWriter, req *http.Request) {
	user, ok := r.mustPrimary(w, req)
	if !ok {
		return
	}
	st := r.deployStoreOrNil(w)
	if st == nil {
		return
	}
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	var body struct {
		Name string `json:"name"`
		To   string `json:"to"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	app, found, err := st.Get(body.Name)
	if err != nil || !found {
		writeErr(w, 404, "app does not exist")
		return
	}
	target := body.To
	if target == "" {
		target = deploy.PreviousCommit(app)
	}
	if target == "" {
		writeErr(w, 400, "no previous deploy to roll back to")
		return
	}
	id := deploy.NewID()
	r.enqueueDeploy(w, req, user, app.Name, deploy.Spec{
		App: app.Name, Commit: target, Trigger: "rollback", DeployID: id,
	})
}

// enqueueDeploy enqueues an app_deploy job and answers {job, deploy_id}.
func (r *Router) enqueueDeploy(w http.ResponseWriter, req *http.Request, user, app string, spec deploy.Spec) {
	if r.queue == nil {
		writeErr(w, 503, "queue unavailable")
		return
	}
	args, _ := json.Marshal(map[string]any{
		"app": spec.App, "ref": spec.Ref, "commit": spec.Commit,
		"preview": spec.Preview, "trigger": spec.Trigger, "deploy_id": spec.DeployID,
	})
	job, err := r.queue.Enqueue("app_deploy", args, user, "deploy-ui")
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	r.auditEvent(req, user, "deploy."+spec.Trigger, app)
	writeJSON(w, map[string]any{"job": job.ID, "deploy_id": spec.DeployID})
}

// POST /api/deploy/app/destroy {name} → tears everything down and removes it.
func (r *Router) handleDeployDestroy(w http.ResponseWriter, req *http.Request) {
	user, ok := r.mustPrimary(w, req)
	if !ok {
		return
	}
	st := r.deployStoreOrNil(w)
	if st == nil {
		return
	}
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	ctx, cancel := context.WithTimeout(req.Context(), 10*time.Minute)
	defer cancel()
	if err := st.Destroy(ctx, body.Name, io.Discard); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	r.auditEvent(req, user, "deploy.app.destroy", body.Name)
	writeJSON(w, map[string]any{"ok": true})
}

// GET/POST /api/deploy/app/env — reads/edits env (production or preview).
func (r *Router) handleDeployEnv(w http.ResponseWriter, req *http.Request) {
	user, ok := r.mustPrimary(w, req)
	if !ok {
		return
	}
	st := r.deployStoreOrNil(w)
	if st == nil {
		return
	}
	name := req.URL.Query().Get("name")
	app, found, err := st.Get(name)
	if err != nil || !found {
		writeErr(w, 404, "app does not exist")
		return
	}
	if req.Method == http.MethodGet {
		writeJSON(w, map[string]any{"env": nz(app.Env), "preview_env": nz(app.PreviewEnv)})
		return
	}
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	var body struct {
		Preview bool              `json:"preview"`
		Set     map[string]string `json:"set"`
		Unset   []string          `json:"unset"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	// UpdateEnv does the read-modify-write under ONE lock (avoids lost updates).
	updated, err := st.UpdateEnv(name, body.Preview, body.Set, body.Unset)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	r.auditEvent(req, user, "deploy.app.env", name)
	writeJSON(w, map[string]any{"env": nz(updated.Env), "preview_env": nz(updated.PreviewEnv)})
}

// GET /api/deploy/app/log?name=<n>&deploy=<id>&offset=<n> → a chunk of the log.
func (r *Router) handleDeployLog(w http.ResponseWriter, req *http.Request) {
	if _, ok := r.mustPrimary(w, req); !ok {
		return
	}
	st := r.deployStoreOrNil(w)
	if st == nil {
		return
	}
	name := req.URL.Query().Get("name")
	id := req.URL.Query().Get("deploy")
	if name == "" || id == "" || !deploy.ValidName(name) || !validDeployID(id) {
		writeErr(w, 400, "invalid name/deploy")
		return
	}
	offset, _ := strconv.ParseInt(req.URL.Query().Get("offset"), 10, 64)
	path := st.LogPath(name, id)
	f, err := os.Open(path)
	if err != nil {
		// the log does not exist yet (the deploy is queued) → an empty chunk, not an error.
		writeJSON(w, map[string]any{"data": "", "offset": offset, "eof": false})
		return
	}
	defer f.Close()
	if offset > 0 {
		_, _ = f.Seek(offset, io.SeekStart)
	}
	buf := make([]byte, 64*1024)
	n, _ := f.Read(buf)
	writeJSON(w, map[string]any{"data": string(buf[:n]), "offset": offset + int64(n)})
}

func nz(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

// validDeployID: ids are "d<timestamp>-<hex>"; it blocks path traversal in the log.
func validDeployID(s string) bool {
	if len(s) < 2 || len(s) > 40 || s[0] != 'd' {
		return false
	}
	for _, c := range s[1:] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c == '-') {
			return false
		}
	}
	return true
}
