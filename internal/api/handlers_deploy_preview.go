package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/deploy"
)

// handlers_deploy_preview.go — manual teardown of a preview env. The TTL is
// handled by the scheduled runner deploy_preview_reap; this is the "tear it down" button.

// POST /api/deploy/app/preview/teardown {name, slug}
func (r *Router) handleDeployPreviewTeardown(w http.ResponseWriter, req *http.Request) {
	if _, ok := r.mustPrimary(w, req); !ok {
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
		Slug string `json:"slug"`
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
	if body.Slug == "" {
		writeErr(w, 400, "slug is required")
		return
	}
	ctx, cancel := context.WithTimeout(req.Context(), 5*time.Minute)
	defer cancel()
	if err := deploy.TeardownPreview(ctx, st, app, body.Slug, discardWriter{}); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	r.auditEvent(req, auth.UserFrom(req), "deploy.preview.teardown", body.Name+"/"+body.Slug)
	writeJSON(w, map[string]any{"ok": true})
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
