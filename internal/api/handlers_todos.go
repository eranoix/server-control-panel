// handlers_todos.go — HTTP layer for the per-user maintenance checklist.
//
// All endpoints require auth; the JWT sub becomes the scope and decides
// which todos.json file is read. Primary doesn't get cross-user view here
// — every operator manages their own checklist.
//
// Routes wired in NewRouter:
//
//	GET    /api/todos                       list + summary
//	POST   /api/todos                       create
//	PATCH  /api/todos/{id}                  update fields
//	DELETE /api/todos/{id}                  delete
//	POST   /api/todos/{id}/done             mark done (rolls recurring)
//	POST   /api/todos/{id}/snooze           body {until_unix}
//	GET    /api/todos/seed                  suggested seed from host state
//	POST   /api/todos/seed                  persist suggested items
package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/scope"
	"server-control-panel/internal/todos"
)

func (r *Router) todoStoreFor(req *http.Request) (*todos.Store, scope.User, bool, error) {
	user := auth.UserFrom(req)
	if user == "" {
		return nil, "", false, errUnauth
	}
	u, err := scope.New(user)
	if err != nil {
		return nil, "", false, err
	}
	paths := scope.PathsFor(r.cfg.DataDir, u)
	return todos.NewStore(paths.Root), u, r.isPrimary(user), nil
}

var errUnauth = newAPIErr("unauthorized")

type apiErr struct{ s string }

func (e *apiErr) Error() string  { return e.s }
func newAPIErr(s string) *apiErr { return &apiErr{s: s} }

func (r *Router) handleTodos(w http.ResponseWriter, req *http.Request) {
	store, _, _, err := r.todoStoreFor(req)
	if err != nil {
		writeErr(w, 401, err.Error())
		return
	}
	switch req.Method {
	case http.MethodGet:
		list, err := store.List()
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		writeJSON(w, map[string]any{
			"todos":   list,
			"summary": todos.SummaryOf(list, time.Now()),
		})
	case http.MethodPost:
		var in todos.Todo
		if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
			writeErr(w, 400, "bad json")
			return
		}
		got, err := store.Create(in)
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		r.auditEvent(req, auth.UserFrom(req), "todo.create", got.ID+":"+got.Title)
		writeJSON(w, got)
	default:
		writeErr(w, 405, "method not allowed")
	}
}

// handleTodoByID dispatches /api/todos/{id}[/done|/snooze]
func (r *Router) handleTodoByID(w http.ResponseWriter, req *http.Request) {
	store, _, _, err := r.todoStoreFor(req)
	if err != nil {
		writeErr(w, 401, err.Error())
		return
	}
	rest := strings.TrimPrefix(req.URL.Path, "/api/todos/")
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

	switch action {
	case "":
		// PATCH or DELETE on bare /api/todos/{id}
		switch req.Method {
		case http.MethodPatch, http.MethodPut:
			var in todos.Todo
			if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
				writeErr(w, 400, "bad json")
				return
			}
			got, err := store.Update(id, in)
			if err != nil {
				if err == todos.ErrNotFound {
					writeErr(w, 404, "not found")
					return
				}
				writeErr(w, 400, err.Error())
				return
			}
			r.auditEvent(req, auth.UserFrom(req), "todo.update", id)
			writeJSON(w, got)
		case http.MethodDelete:
			if err := store.Delete(id); err != nil {
				if err == todos.ErrNotFound {
					writeErr(w, 404, "not found")
					return
				}
				writeErr(w, 500, err.Error())
				return
			}
			r.auditEvent(req, auth.UserFrom(req), "todo.delete", id)
			writeJSON(w, map[string]string{"status": "deleted"})
		default:
			writeErr(w, 405, "method not allowed")
		}
	case "done":
		if req.Method != http.MethodPost {
			writeErr(w, 405, "method not allowed")
			return
		}
		got, err := store.MarkDone(id)
		if err != nil {
			if err == todos.ErrNotFound {
				writeErr(w, 404, "not found")
				return
			}
			writeErr(w, 500, err.Error())
			return
		}
		r.auditEvent(req, auth.UserFrom(req), "todo.done", id)
		writeJSON(w, got)
	case "snooze":
		if req.Method != http.MethodPost {
			writeErr(w, 405, "method not allowed")
			return
		}
		var body struct {
			UntilUnix int64 `json:"until_unix"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			writeErr(w, 400, "bad json")
			return
		}
		got, err := store.Snooze(id, body.UntilUnix)
		if err != nil {
			if err == todos.ErrNotFound {
				writeErr(w, 404, "not found")
				return
			}
			writeErr(w, 400, err.Error())
			return
		}
		r.auditEvent(req, auth.UserFrom(req), "todo.snooze", id)
		writeJSON(w, got)
	default:
		writeErr(w, 404, "unknown action")
	}
}

// handleTodosSeed: GET returns the SuggestSeed candidates; POST inserts them.
func (r *Router) handleTodosSeed(w http.ResponseWriter, req *http.Request) {
	store, _, _, err := r.todoStoreFor(req)
	if err != nil {
		writeErr(w, 401, err.Error())
		return
	}
	candidates := todos.SuggestSeed(req.Context(), r.cfg.DataDir)

	switch req.Method {
	case http.MethodGet:
		writeJSON(w, map[string]any{"candidates": candidates})
	case http.MethodPost:
		var inserted []todos.Todo
		for _, c := range candidates {
			got, err := store.Create(c)
			if err != nil {
				continue
			}
			inserted = append(inserted, *got)
		}
		r.auditEvent(req, auth.UserFrom(req), "todo.seed", "n="+itoa(len(inserted)))
		writeJSON(w, map[string]any{"inserted": inserted})
	default:
		writeErr(w, 405, "method not allowed")
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [10]byte
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
