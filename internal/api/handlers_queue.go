// handlers_queue.go — HTTP layer for the background-jobs queue.
//
// Routes wired in NewRouter:
//
//	POST   /api/queue                 body {kind, args, source?}  → enqueue
//	GET    /api/queue?status=&limit=                              → list
//	GET    /api/queue/{id}                                        → job snapshot
//	DELETE /api/queue/{id}                                        → remove finished job + log
//	POST   /api/queue/{id}/cancel                                 → signal cancel
//	POST   /api/queue/{id}/rerun                                  → restart same job in place
//	GET    /api/queue/{id}/log?n_bytes=                           → tail of persisted log
//	GET    /ws/queue/{id}                                         → live events + log
//
// Authz happens via Runner.AuthorizedFor on enqueue. The "shell" runner
// (arbitrary command) is primary-only; everything else is open to any
// authed user.
package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/queue"
)

func (r *Router) handleQueue(w http.ResponseWriter, req *http.Request) {
	if r.queue == nil {
		writeErr(w, 503, "queue unavailable")
		return
	}
	user := auth.UserFrom(req)
	if user == "" {
		writeErr(w, 401, "unauthorized")
		return
	}
	switch req.Method {
	case http.MethodGet:
		q := req.URL.Query()
		status := queue.Status(q.Get("status"))
		limit, _ := strconv.Atoi(q.Get("limit"))
		if limit <= 0 || limit > 500 {
			limit = 100
		}
		owner := ""
		// non-primary only sees their own jobs
		if !r.isPrimary(user) {
			owner = user
		} else if u := q.Get("owner"); u != "" {
			owner = u
		}
		writeJSON(w, map[string]any{"jobs": r.queue.List(owner, status, limit)})
	case http.MethodPost:
		var body struct {
			Kind   string          `json:"kind"`
			Args   json.RawMessage `json:"args"`
			Source string          `json:"source"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			writeErr(w, 400, "bad json")
			return
		}
		// Authz: look up the runner and ask if user may submit this kind.
		runner, ok := r.queueRunner(body.Kind)
		if !ok {
			writeErr(w, 400, "unknown kind: "+body.Kind)
			return
		}
		if !runner.AuthorizedFor(user, r.isPrimary(user)) {
			r.auditEvent(req, user, "queue.enqueue.denied", "kind="+body.Kind)
			writeErr(w, 403, "not authorized for kind "+body.Kind)
			return
		}
		source := body.Source
		if source == "" {
			source = "user"
		}
		j, err := r.queue.Enqueue(body.Kind, body.Args, user, source)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		r.auditEvent(req, user, "queue.enqueue", "kind="+body.Kind+" id="+j.ID)
		writeJSON(w, j)
	default:
		writeErr(w, 405, "method not allowed")
	}
}

func (r *Router) handleQueueByID(w http.ResponseWriter, req *http.Request) {
	if r.queue == nil {
		writeErr(w, 503, "queue unavailable")
		return
	}
	user := auth.UserFrom(req)
	if user == "" {
		writeErr(w, 401, "unauthorized")
		return
	}
	rest := strings.TrimPrefix(req.URL.Path, "/api/queue/")
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
	j, err := r.queue.Get(id)
	if err != nil {
		writeErr(w, 404, "not found")
		return
	}
	// non-primary only sees own
	if !r.isPrimary(user) && j.Owner != user {
		writeErr(w, 403, "forbidden")
		return
	}
	switch action {
	case "":
		switch req.Method {
		case http.MethodGet:
			writeJSON(w, j)
		case http.MethodDelete:
			if err := r.queue.Delete(id); err != nil {
				switch {
				case errors.Is(err, queue.ErrConflict):
					writeErr(w, 409, "cancel the job before deleting")
				case errors.Is(err, queue.ErrNotFound):
					writeErr(w, 404, "not found")
				default:
					writeErr(w, 500, err.Error())
				}
				return
			}
			r.auditEvent(req, user, "queue.delete", "id="+id)
			writeJSON(w, map[string]string{"status": "deleted"})
		default:
			writeErr(w, 405, "method not allowed")
		}
	case "rerun":
		if req.Method != http.MethodPost {
			writeErr(w, 405, "method not allowed")
			return
		}
		runner, ok := r.queueRunner(j.Kind)
		if !ok {
			writeErr(w, 400, "unknown kind: "+j.Kind)
			return
		}
		if !runner.AuthorizedFor(user, r.isPrimary(user)) {
			r.auditEvent(req, user, "queue.rerun.denied", "kind="+j.Kind)
			writeErr(w, 403, "not authorized for kind "+j.Kind)
			return
		}
		nj, err := r.queue.Rerun(id)
		if err != nil {
			switch {
			case errors.Is(err, queue.ErrConflict):
				writeErr(w, 409, "job is still active")
			case errors.Is(err, queue.ErrNotFound):
				writeErr(w, 404, "not found")
			default:
				writeErr(w, 500, err.Error())
			}
			return
		}
		r.auditEvent(req, user, "queue.rerun", "id="+id)
		writeJSON(w, nj)
	case "cancel":
		if req.Method != http.MethodPost {
			writeErr(w, 405, "method not allowed")
			return
		}
		if err := r.queue.Cancel(id); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		r.auditEvent(req, user, "queue.cancel", "id="+id)
		writeJSON(w, map[string]string{"status": "cancel-requested"})
	case "log":
		// Default to last 256KiB. Before this, omitting n_bytes (or
		// passing <=0) returned the ENTIRE log — a 100MB apt-upgrade
		// log would OOM the server's response buffer + the browser.
		maxBytes, _ := strconv.ParseInt(req.URL.Query().Get("n_bytes"), 10, 64)
		if maxBytes <= 0 {
			maxBytes = 256 * 1024
		} else if maxBytes > 10*1024*1024 {
			maxBytes = 10 * 1024 * 1024 // hard cap 10MiB
		}
		data, err := r.queue.ReadLog(id, maxBytes)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write(data)
	default:
		writeErr(w, 404, "unknown action")
	}
}

func (r *Router) handleQueueWS(w http.ResponseWriter, req *http.Request) {
	if r.queue == nil {
		writeErr(w, 503, "queue unavailable")
		return
	}
	user := auth.UserFrom(req)
	if user == "" {
		writeErr(w, 401, "unauthorized")
		return
	}
	id := strings.TrimPrefix(req.URL.Path, "/ws/queue/")
	if id == "" {
		writeErr(w, 400, "id required")
		return
	}
	j, err := r.queue.Get(id)
	if err != nil {
		writeErr(w, 404, "not found")
		return
	}
	if !r.isPrimary(user) && j.Owner != user {
		writeErr(w, 403, "forbidden")
		return
	}
	conn, err := wsUpgrader.Upgrade(w, req, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	conn.SetReadLimit(1024)
	_ = conn.SetReadDeadline(time.Now().Add(dockerWsPongWait))
	conn.SetPongHandler(func(string) error {
		_ = conn.SetReadDeadline(time.Now().Add(dockerWsPongWait))
		return nil
	})

	// Replay existing log (truncated to 64 KiB so reconnects don't OOM the client).
	if log, _ := r.queue.ReadLog(id, 64*1024); len(log) > 0 {
		_ = conn.SetWriteDeadline(time.Now().Add(dockerWsWriteWait))
		_ = conn.WriteJSON(queue.Event{Type: "log", JobID: id, LogLine: string(log), TS: time.Now().Unix()})
	}
	_ = conn.SetWriteDeadline(time.Now().Add(dockerWsWriteWait))
	_ = conn.WriteJSON(queue.Event{Type: "status", JobID: id, Status: j.Status, Progress: j.Progress, Step: j.Step, TS: time.Now().Unix()})

	events, unsub := r.queue.Subscribe(id)
	defer unsub()

	// reader goroutine cancels writer on close
	done := make(chan struct{})
	go func() {
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				close(done)
				return
			}
		}
	}()

	ping := time.NewTicker(dockerWsPingPeriod)
	defer ping.Stop()
	for {
		select {
		case <-done:
			return
		case <-ping.C:
			_ = conn.SetWriteDeadline(time.Now().Add(dockerWsWriteWait))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case ev, ok := <-events:
			if !ok {
				return
			}
			_ = conn.SetWriteDeadline(time.Now().Add(dockerWsWriteWait))
			if err := conn.WriteJSON(ev); err != nil {
				return
			}
			// On terminal status, send a final frame and close.
			if ev.Type == "status" && (ev.Status == queue.StatusDone || ev.Status == queue.StatusFailed || ev.Status == queue.StatusCancelled || ev.Status == queue.StatusInterrupted) {
				return
			}
		}
	}
}

// queueRunner exposes the runner for a kind without leaking the registry
// pointer outside the package. Stored in r.queueRunners; populated in
// NewRouter alongside r.queue.Register.
func (r *Router) queueRunner(kind string) (queue.Runner, bool) {
	r.cfgMu.Lock()
	defer r.cfgMu.Unlock()
	runner, ok := r.queueRunners[kind]
	return runner, ok
}
