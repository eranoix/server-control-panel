// handlers_scheduler.go — HTTP layer for the cron-driven scheduler (F2).
//
// Routes wired in NewRouter:
//
//	GET    /api/scheduler/jobs                  list (filtered by owner if non-primary)
//	POST   /api/scheduler/jobs                  create
//	GET    /api/scheduler/jobs/{id}             one job snapshot
//	PUT    /api/scheduler/jobs/{id}             update
//	DELETE /api/scheduler/jobs/{id}             delete
//	POST   /api/scheduler/jobs/{id}/run-now     enqueue immediately
//	GET    /api/scheduler/preview?expr=...&n=5  next-fires preview for the editor
//
// Authz: any authed user can manage their own jobs; primary sees and edits
// all. A job with run_as_root=true requires primary on save.
package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/scheduler"
)

func (r *Router) handleSchedulerJobs(w http.ResponseWriter, req *http.Request) {
	if r.scheduler == nil {
		writeErr(w, 503, "scheduler unavailable")
		return
	}
	user := auth.UserFrom(req)
	if user == "" {
		writeErr(w, 401, "unauthorized")
		return
	}
	switch req.Method {
	case http.MethodGet:
		owner := ""
		if !r.isPrimary(user) {
			owner = user
		}
		writeJSON(w, map[string]any{"jobs": r.scheduler.List(owner)})
	case http.MethodPost:
		var in scheduler.Job
		if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
			writeErr(w, 400, "bad json")
			return
		}
		in.ID = "" // always create
		in.Owner = user
		injectSchedOwner(&in)
		if !r.authorizeKind(w, req, user, in.Kind) {
			return
		}
		if in.ThenKind != "" && !r.authorizeKind(w, req, user, in.ThenKind) {
			return
		}
		if in.RunAsRoot && !r.isPrimary(user) {
			writeErr(w, 403, "run_as_root requires primary")
			return
		}
		got, err := r.scheduler.Save(in)
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		r.auditEvent(req, user, "scheduler.create", got.ID+":"+got.Name)
		writeJSON(w, got)
	default:
		writeErr(w, 405, "method not allowed")
	}
}

func (r *Router) handleSchedulerJobByID(w http.ResponseWriter, req *http.Request) {
	if r.scheduler == nil {
		writeErr(w, 503, "scheduler unavailable")
		return
	}
	user := auth.UserFrom(req)
	if user == "" {
		writeErr(w, 401, "unauthorized")
		return
	}
	rest := strings.TrimPrefix(req.URL.Path, "/api/scheduler/jobs/")
	if rest == "" {
		writeErr(w, 400, "id required")
		return
	}
	parts := strings.SplitN(rest, "/", 2)
	id := parts[0]
	action := ""
	if len(parts) == 2 {
		action = parts[1]
	}
	cur, err := r.scheduler.Get(id)
	if err != nil {
		writeErr(w, 404, "not found")
		return
	}
	if !r.isPrimary(user) && cur.Owner != user {
		writeErr(w, 403, "forbidden")
		return
	}

	switch action {
	case "":
		switch req.Method {
		case http.MethodGet:
			writeJSON(w, cur)
		case http.MethodPut, http.MethodPatch:
			var in scheduler.Job
			if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
				writeErr(w, 400, "bad json")
				return
			}
			in.ID = id
			in.Owner = cur.Owner
			in.Created = cur.Created
			injectSchedOwner(&in)
			if !r.authorizeKind(w, req, user, in.Kind) {
				return
			}
			if in.RunAsRoot && !r.isPrimary(user) {
				writeErr(w, 403, "run_as_root requires primary")
				return
			}
			got, err := r.scheduler.Save(in)
			if err != nil {
				writeErr(w, 400, err.Error())
				return
			}
			r.auditEvent(req, user, "scheduler.update", id)
			writeJSON(w, got)
		case http.MethodDelete:
			if err := r.scheduler.Delete(id); err != nil {
				writeErr(w, 500, err.Error())
				return
			}
			r.auditEvent(req, user, "scheduler.delete", id)
			writeJSON(w, map[string]string{"status": "deleted"})
		default:
			writeErr(w, 405, "method not allowed")
		}
	case "run-now":
		if req.Method != http.MethodPost {
			writeErr(w, 405, "method not allowed")
			return
		}
		// Revalidate the kind on every manual fire: the create/update gate
		// above is not enough on its own, since an orphaned job (kind became
		// primary-only after it was saved, or saved before this fix) could
		// still be triggered here. cur.Owner == user or primary is already
		// enforced at :94; this adds the kind-authz the queue requires.
		if !r.authorizeKind(w, req, user, cur.Kind) {
			return
		}
		qid, err := r.scheduler.RunNow(id)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		r.auditEvent(req, user, "scheduler.run_now", id+" -> "+qid)
		writeJSON(w, map[string]string{"queue_job_id": qid})
	case "history":
		// Latest runs of this schedule (queue jobs carrying the job's source).
		if r.queue == nil {
			writeJSON(w, map[string]any{"runs": []any{}})
			return
		}
		sources := map[string]bool{
			"scheduler:" + id: true, "scheduler-manual:" + id: true, "scheduler-chain:" + id: true,
		}
		runs := r.queue.ListBySource(sources, 20)
		out := make([]map[string]any, 0, len(runs))
		for _, j := range runs {
			out = append(out, map[string]any{
				"id": j.ID, "status": j.Status, "kind": j.Kind, "source": j.Source,
				"queued": j.Queued, "started": j.Started, "finished": j.Finished, "error": j.Error,
			})
		}
		writeJSON(w, map[string]any{"runs": out})
	default:
		writeErr(w, 404, "unknown action")
	}
}

func (r *Router) handleSchedulerPreview(w http.ResponseWriter, req *http.Request) {
	if r.scheduler == nil {
		writeErr(w, 503, "scheduler unavailable")
		return
	}
	expr := req.URL.Query().Get("expr")
	n, _ := strconv.Atoi(req.URL.Query().Get("n"))
	fires, err := r.scheduler.NextFires(expr, n)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	out := make([]int64, 0, len(fires))
	for _, t := range fires {
		out = append(out, t.Unix())
	}
	writeJSON(w, map[string]any{"fires": out})
}

// injectSchedOwner pins the owner inside the args of the per-user kinds (today only
// session_backup). The runner does not receive the job's owner — it arrives through the args, as
// with jira_ai_analysis — so the HTTP layer writes that owner on save. We ALWAYS overwrite
// with the owner already decided server-side (in.Owner), never trusting what the
// client sent: that way a user cannot schedule a backup of someone else's sessions
// by forging "owner" in the JSON. Idempotent (re-injected on every save).
func injectSchedOwner(in *scheduler.Job) {
	if in.Kind != "session_backup" {
		return
	}
	m := map[string]any{}
	if len(in.Args) > 0 {
		_ = json.Unmarshal(in.Args, &m)
	}
	m["owner"] = in.Owner
	if b, err := json.Marshal(m); err == nil {
		in.Args = b
	}
}
