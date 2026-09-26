package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/deploy"
)

// handlers_deploy_catalog.go — one-click service catalogue.

// GET /api/deploy/catalog → available templates.
func (r *Router) handleDeployCatalog(w http.ResponseWriter, req *http.Request) {
	if _, ok := r.mustPrimary(w, req); !ok {
		return
	}
	writeJSON(w, map[string]any{"templates": deploy.Catalog()})
}

// POST /api/deploy/catalog/create {template_id, name, port?, domain?, env{}}
// Creates the app + seeds the template's compose (fast), then ENQUEUES the deploy
// (build) so it streams into the log. Answers {app, job, deploy_id}.
func (r *Router) handleDeployCatalogCreate(w http.ResponseWriter, req *http.Request) {
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
		TemplateID string            `json:"template_id"`
		Name       string            `json:"name"`
		Port       int               `json:"port"`
		Domain     string            `json:"domain"`
		Env        map[string]string `json:"env"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	tmpl, found := deploy.TemplateByID(body.TemplateID)
	if !found {
		writeErr(w, 400, "unknown template")
		return
	}
	ctx, cancel := context.WithTimeout(req.Context(), 2*time.Minute)
	defer cancel()
	app, err := st.CreateAndSeed(ctx, tmpl, deploy.App{
		Name: body.Name, Port: body.Port, Domain: body.Domain,
	}, body.Env)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	r.auditEvent(req, auth.UserFrom(req), "deploy.catalog.create", app.Name+" ("+tmpl.ID+")")
	id := deploy.NewID()
	r.enqueueDeploy(w, req, user, app.Name, deploy.Spec{App: app.Name, Trigger: "ui", DeployID: id})
}
